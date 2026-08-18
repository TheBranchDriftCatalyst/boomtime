package db

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Guards the BOUNDED gap-recompute window (TALOS-kvg1). The ingest path only
// recomputes gap_seconds for the batch's own span plus the single beat that
// follows it — everything later keeps its predecessor and must not be touched.
// If that horizon is ever narrowed by mistake, the "following beat" assertion
// below fails; if it is widened back to unbounded, the perf regression returns
// (a 30-day-old backlog beat used to rescan 83k rows).
var _ = ginkgo.Describe("gap_seconds recompute window (TALOS-kvg1)", func() {
	ginkgo.It("an out-of-order insert fixes the FOLLOWING beat's gap and leaves later beats correct", func() {
		ctx := context.Background()
		d := openTestDBG()
		f := newSenderG(d, "gapwin")
		sender := f.Sender()
		proj := "gapwin_proj"

		base := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
		mk := func(off time.Duration, entity string) model.HeartbeatPayload {
			return model.HeartbeatPayload{
				Sender: &sender, Project: &proj,
				Entity: entity, Type: model.FileType,
				TimeSent: float64(base.Add(off).Unix()),
			}
		}
		gapAt := func(off time.Duration) *int {
			var g *int
			err := d.Pool.QueryRow(ctx,
				`SELECT gap_seconds FROM heartbeats WHERE sender=$1 AND time_sent=$2`,
				sender, base.Add(off)).Scan(&g)
			Expect(err).NotTo(HaveOccurred())
			return g
		}

		// Seed an ordered run: t+0, t+200, t+300.
		_, err := d.SaveHeartbeats(ctx, []model.HeartbeatPayload{
			mk(0, "a.go"),
			mk(200*time.Second, "b.go"),
			mk(300*time.Second, "c.go"),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(gapAt(0)).To(BeNil(), "first beat has no predecessor")
		Expect(*gapAt(200 * time.Second)).To(Equal(200))
		Expect(*gapAt(300 * time.Second)).To(Equal(100))

		// Backfill BETWEEN existing beats — the case the horizon exists for.
		_, err = d.SaveHeartbeats(ctx, []model.HeartbeatPayload{mk(100*time.Second, "d.go")})
		Expect(err).NotTo(HaveOccurred())

		Expect(*gapAt(100 * time.Second)).To(Equal(100), "inserted beat gaps to its predecessor")
		// THE assertion the bounded window must not break: this row is AFTER the
		// batch, so it is only correct because the horizon deliberately includes
		// the next existing beat.
		Expect(*gapAt(200 * time.Second)).To(Equal(100), "beat following the insert must be re-gapped")
		Expect(*gapAt(300 * time.Second)).To(Equal(100), "beats beyond the horizon stay correct")
	})
})
