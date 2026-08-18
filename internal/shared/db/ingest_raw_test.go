// ingest_raw_test.go — SaveHeartbeatsRaw skips the phase-3 rollup/gap
// maintenance while still inserting the raw heartbeats (gaka-0oe.3). This is
// the DB-level guarantee the rollup-skip ingest dispatch relies on: an
// ingest-only tier's writes land but never touch hb_rollup_daily.
package db

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("SaveHeartbeatsRaw (gaka-0oe.3 rollup-skip)", func() {
	ginkgo.It("inserts the heartbeat but does NOT write hb_rollup_daily", func() {
		d := openTestDBG()
		ctx := context.Background()

		f := newSenderG(d, "rawskip")
		sender := f.Sender()

		baseHBs := scalarCountG(d, ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, sender)
		baseRollup := scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)

		base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
		proj := "rawskip_proj"
		hb := model.HeartbeatPayload{
			Sender: &sender, Project: &proj,
			Entity: "a.go", Type: model.FileType, TimeSent: float64(base.Unix()),
		}

		ids, err := d.SaveHeartbeatsRaw(ctx, []model.HeartbeatPayload{hb})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(HaveLen(1))
		Expect(ids[0]).NotTo(BeZero())

		// Phase 1+2 ran: the raw heartbeat landed.
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, sender)).
			To(Equal(baseHBs+1), "raw ingest must still insert the heartbeat")
		// Phase 3 was skipped: the rollup table is untouched.
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)).
			To(Equal(baseRollup), "raw ingest must NOT write hb_rollup_daily")
	})

	ginkgo.It("SaveHeartbeats (full path) DOES write hb_rollup_daily for the same fixture", func() {
		d := openTestDBG()
		ctx := context.Background()

		f := newSenderG(d, "rollupfull")
		sender := f.Sender()

		baseRollup := scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)

		base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)
		proj := "rollupfull_proj"
		hb := model.HeartbeatPayload{
			Sender: &sender, Project: &proj,
			Entity: "b.go", Type: model.FileType, TimeSent: float64(base.Unix()),
		}

		_, err := d.SaveHeartbeats(ctx, []model.HeartbeatPayload{hb})
		Expect(err).NotTo(HaveOccurred())
		// The full path materializes a rollup row — the exact behavior the raw
		// path deliberately skips.
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)).
			To(BeNumerically(">", baseRollup), "full ingest must write hb_rollup_daily")
	})
})
