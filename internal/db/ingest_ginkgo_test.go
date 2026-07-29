// ingest_ginkgo_test.go — ginkgo mirror of ingest_test.go (gaka-0vp.13).
// 1:1 case map (2 stdlib TestXxx → 2 Its):
//   TestSaveHeartbeatsAtomicity   → "SaveHeartbeats atomicity"
//   TestSaveHeartbeatsBatchOrder  → "SaveHeartbeats returns ids in input order"
package db

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("SaveHeartbeats", func() {
	ginkgo.It("is atomic: an FK-failing batch leaves NO trace behind (gaka-4sq)", func() {
		d := openTestDBG()
		ctx := context.Background()

		f := newSenderG(d, "atomic")
		sender := f.Sender()

		// Baseline: pre-existing rows we can diff against.
		baselineHBs := scalarCountG(d, ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, sender)
		baselineProjects := scalarCountG(d, ctx, `SELECT count(*) FROM projects WHERE owner=$1`, sender)
		baselineRollup := scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)

		// A valid heartbeat + one that will fail FK (unknown sender = no users row).
		base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
		proj := "atomic_proj_new"
		good := model.HeartbeatPayload{
			Sender: &sender, Project: &proj,
			Entity: "a.go", Type: model.FileType, TimeSent: float64(base.Unix()),
		}
		unknownSender := "no_such_user_" + sender
		bad := good
		bad.Sender = &unknownSender
		bad.TimeSent = float64(base.Add(time.Minute).Unix())

		_, err := d.SaveHeartbeats(ctx, []model.HeartbeatPayload{good, bad})
		Expect(err).To(HaveOccurred(), "expected FK error on batch with unknown sender")

		// Every table the ingest touches must look exactly like it did before.
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, sender)).To(Equal(baselineHBs), "heartbeats leaked from failed batch")
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM projects WHERE owner=$1`, sender)).To(Equal(baselineProjects), "projects leaked from failed batch")
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)).To(Equal(baselineRollup), "rollup leaked from failed batch")

		// A follow-up clean batch must still succeed against the same fixture — proves
		// the failed tx didn't leave the pool in a bad state.
		ids, err := d.SaveHeartbeats(ctx, []model.HeartbeatPayload{good})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(HaveLen(1))
		Expect(ids[0]).NotTo(BeZero())
	})

	ginkgo.It("returns ids in input order (positional response contract)", func() {
		d := openTestDBG()
		ctx := context.Background()

		f := newSenderG(d, "order")
		sender := f.Sender()

		base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
		const n = 25
		batch := make([]model.HeartbeatPayload, n)
		for i := 0; i < n; i++ {
			p := "order_p_" + string(rune('a'+i%20))
			entity := "f" + string(rune('a'+i%20)) + ".go"
			batch[i] = model.HeartbeatPayload{
				Sender: &sender, Project: &p,
				Entity: entity, Type: model.FileType,
				TimeSent: float64(base.Add(time.Duration(i) * time.Minute).Unix()),
			}
		}

		ids, err := d.SaveHeartbeats(ctx, batch)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(HaveLen(n))

		// Cross-check: the ids must map back to the same input order when we look up
		// each row's time_sent in the DB.
		for i, id := range ids {
			var ts time.Time
			err := d.Pool.QueryRow(ctx, `SELECT time_sent FROM heartbeats WHERE id=$1`, id).Scan(&ts)
			Expect(err).NotTo(HaveOccurred())
			want := base.Add(time.Duration(i) * time.Minute)
			Expect(ts.Equal(want)).To(BeTrue(),
				"id[%d]=%d has time_sent=%s, want %s (batch order broken)", i, id, ts, want)
		}
	})
})
