// backfill_ginkgo_test.go — ginkgo mirror of backfill_test.go (gaka-0vp.13).
// 1:1 case map (8 stdlib TestXxx → 8 Its):
//   TestInsertBackfillBatch_NoOverlap_WritesAll              → It "no-overlap session writes every heartbeat with source"
//   TestInsertBackfillBatch_OverlapWithReal_SkipsSession     → It "overlap with real (source NULL) heartbeats skips session"
//   TestInsertBackfillBatch_OverlapWithPriorBackfill_StillWrites → It "overlap only with prior backfill still writes (idempotent)"
//   TestDeleteBackfilledHeartbeats_PreservesRealRows         → It "DeleteBackfilledHeartbeats preserves source-NULL real rows"
//   TestBackfillStatsFor_ReportsCounts                       → It "BackfillStatsFor reports per-source counts"
//   TestGetBackfillConfig_ReturnsDefaultsForNewUser          → It "GetBackfillConfig returns defaults when no row"
//   TestSetBackfillConfig_Roundtrip                          → It "SetBackfillConfig roundtrips values (+ lang map)"
//   TestClampBackfillConfig_ForcesBackfillPrefix             → It "clampBackfillConfig repairs missing backfill: prefix"
package db

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mkBackfillHBG mirrors mkBackfillHB (kept local rather than reused so a wire-
// shape change breaks tests loudly).
func mkBackfillHBG(sender, project, entity string, ts time.Time) model.HeartbeatPayload {
	s := sender
	p := project
	cat := "coding"
	ua := "backfill-test"
	return model.HeartbeatPayload{
		Entity:    entity,
		Type:      model.FileType,
		Sender:    &s,
		Project:   &p,
		Category:  &cat,
		UserAgent: ua,
		TimeSent:  float64(ts.Unix()),
	}
}

var _ = ginkgo.Describe("backfill", func() {
	ginkgo.It("InsertBackfillBatch with no overlap writes every heartbeat with source tag", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfnovlp")
		ctx := f.Ctx()
		sender := f.Sender()

		sess := BackfillSession{
			Start: time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second),
			End:   time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second),
			Heartbeats: []model.HeartbeatPayload{
				mkBackfillHBG(sender, "p1", "main.go", time.Now().Add(-90*time.Minute).UTC()),
				mkBackfillHBG(sender, "p1", "main.go", time.Now().Add(-88*time.Minute).UTC()),
			},
		}
		res, err := d.InsertBackfillBatch(ctx, BackfillBatch{
			Username:  sender,
			SourceTag: "backfill:git",
			Sessions:  []BackfillSession{sess},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.AcceptedHeartbeats).To(BeEquivalentTo(2))
		Expect(res.SkippedHeartbeats).To(BeEquivalentTo(0))

		var got int
		err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='backfill:git'`, sender).Scan(&got)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(2))
	})

	ginkgo.It("InsertBackfillBatch skips a session that overlaps a real (source NULL) heartbeat", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfovlp")
		ctx := f.Ctx()
		sender := f.Sender()
		f.Projects("p1")

		realTime := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second)
		f.Seed(hbSeed{project: "p1", ts: realTime, gap: 60, entity: "editor.go"})

		sess := BackfillSession{
			Start: realTime.Add(-30 * time.Minute),
			End:   realTime.Add(30 * time.Minute),
			Heartbeats: []model.HeartbeatPayload{
				mkBackfillHBG(sender, "p1", "main.go", realTime.Add(-10*time.Minute)),
				mkBackfillHBG(sender, "p1", "main.go", realTime.Add(-8*time.Minute)),
			},
		}
		res, err := d.InsertBackfillBatch(ctx, BackfillBatch{
			Username:  sender,
			SourceTag: "backfill:git",
			Sessions:  []BackfillSession{sess},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.AcceptedHeartbeats).To(BeEquivalentTo(0))
		Expect(res.SkippedHeartbeats).To(BeEquivalentTo(2))
		Expect(res.Sessions[0].Reason).To(Equal("overlap"))
	})

	ginkgo.It("overlap only with prior backfill (source != NULL) still writes; ON CONFLICT absorbs the dup (idempotent)", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfprior")
		ctx := f.Ctx()
		sender := f.Sender()

		hbs := []model.HeartbeatPayload{
			mkBackfillHBG(sender, "p1", "main.go", time.Now().Add(-90*time.Minute).UTC().Truncate(time.Second)),
		}
		sess := BackfillSession{
			Start:      time.Now().Add(-95 * time.Minute).UTC().Truncate(time.Second),
			End:        time.Now().Add(-85 * time.Minute).UTC().Truncate(time.Second),
			Heartbeats: hbs,
		}
		_, err := d.InsertBackfillBatch(ctx, BackfillBatch{
			Username:  sender,
			SourceTag: "backfill:git",
			Sessions:  []BackfillSession{sess},
		})
		Expect(err).NotTo(HaveOccurred())
		res, err := d.InsertBackfillBatch(ctx, BackfillBatch{
			Username:  sender,
			SourceTag: "backfill:git",
			Sessions:  []BackfillSession{sess},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.SkippedHeartbeats).To(BeEquivalentTo(0))

		var got int
		err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='backfill:git'`, sender).Scan(&got)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(1), "idempotency")
	})

	ginkgo.It("DeleteBackfilledHeartbeats preserves source-NULL real rows", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfdel")
		ctx := f.Ctx()
		sender := f.Sender()
		f.Projects("p1")

		realTime := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
		f.Seed(hbSeed{project: "p1", ts: realTime, entity: "real.go"})

		sess := BackfillSession{
			Start: time.Now().Add(-2 * time.Hour).UTC(),
			End:   time.Now().Add(-time.Hour).UTC(),
			Heartbeats: []model.HeartbeatPayload{
				mkBackfillHBG(sender, "p1", "main.go", time.Now().Add(-90*time.Minute).UTC()),
			},
		}
		_, err := d.InsertBackfillBatch(ctx, BackfillBatch{
			Username:  sender,
			SourceTag: "backfill:git",
			Sessions:  []BackfillSession{sess},
		})
		Expect(err).NotTo(HaveOccurred())

		n, err := d.DeleteBackfilledHeartbeats(ctx, sender, "backfill:%")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1))

		var realCount int
		err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source IS NULL`, sender).Scan(&realCount)
		Expect(err).NotTo(HaveOccurred())
		Expect(realCount).To(Equal(1), "delete leaked into real data")
	})

	ginkgo.It("BackfillStatsFor reports total + per-source counts", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfstats")
		ctx := f.Ctx()
		sender := f.Sender()

		when := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
		sess := BackfillSession{
			Start: when.Add(-5 * time.Minute),
			End:   when.Add(5 * time.Minute),
			Heartbeats: []model.HeartbeatPayload{
				mkBackfillHBG(sender, "p1", "a.go", when),
			},
		}
		_, err := d.InsertBackfillBatch(ctx, BackfillBatch{
			Username:  sender,
			SourceTag: "backfill:git",
			Sessions:  []BackfillSession{sess},
		})
		Expect(err).NotTo(HaveOccurred())
		stats, err := d.BackfillStatsFor(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(stats.TotalRows).To(BeEquivalentTo(1))
		Expect(stats.Sources["backfill:git"]).To(BeEquivalentTo(1))
	})

	ginkgo.It("GetBackfillConfig returns sensible defaults when no row exists", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfcfgnew")
		cfg, err := d.GetBackfillConfig(context.Background(), f.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ClusterGapSec).To(BeEquivalentTo(1800))
		Expect(cfg.HeartbeatRateSec).To(BeEquivalentTo(120))
		Expect(cfg.SourceTag).To(Equal("backfill:git"))
	})

	ginkgo.It("SetBackfillConfig roundtrips values (including LangMap)", func() {
		d := openTestDBG()
		f := newSenderG(d, "bfcfgrt")
		sender := f.Sender()
		cfg := BackfillConfig{
			Username:          sender,
			ClusterGapSec:     600,
			PreCommitLeadSec:  300,
			PostCommitTailSec: 60,
			HeartbeatRateSec:  60,
			AuthorEmails:      []string{"me@x.com"},
			SourceTag:         "backfill:git",
			LangMap:           map[string]string{"ts": "TypeScript"},
		}
		Expect(d.SetBackfillConfig(context.Background(), cfg)).To(Succeed())
		got, err := d.GetBackfillConfig(context.Background(), sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ClusterGapSec).To(Equal(cfg.ClusterGapSec))
		Expect(got.HeartbeatRateSec).To(Equal(cfg.HeartbeatRateSec))
		Expect(got.SourceTag).To(Equal(cfg.SourceTag))
		Expect(got.LangMap["ts"]).To(Equal("TypeScript"))
	})

	ginkgo.It("clampBackfillConfig repairs a SourceTag missing the required backfill: prefix", func() {
		c := clampBackfillConfig(BackfillConfig{SourceTag: "git-history"})
		Expect(c.SourceTag).To(Equal("backfill:git-history"))
	})
})
