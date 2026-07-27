package db

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// mkBackfillHB is a small builder for a materialized-shape heartbeat.
// Kept local rather than reusing insertSeed's hbSeed so a schema change
// on the wire shape (model.HeartbeatPayload) breaks tests loudly.
func mkBackfillHB(sender, project, entity string, ts time.Time) model.HeartbeatPayload {
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

// TestInsertBackfillBatch_NoOverlap_WritesAll seeds a fresh user with no
// real heartbeats and inserts one session; every heartbeat should land
// with source=<tag>.
func TestInsertBackfillBatch_NoOverlap_WritesAll(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfnovlp")
	ctx := f.Ctx()
	sender := f.Sender()

	sess := BackfillSession{
		Start: time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second),
		End:   time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second),
		Heartbeats: []model.HeartbeatPayload{
			mkBackfillHB(sender, "p1", "main.go", time.Now().Add(-90*time.Minute).UTC()),
			mkBackfillHB(sender, "p1", "main.go", time.Now().Add(-88*time.Minute).UTC()),
		},
	}
	res, err := d.InsertBackfillBatch(ctx, BackfillBatch{
		Username:  sender,
		SourceTag: "backfill:git",
		Sessions:  []BackfillSession{sess},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if res.AcceptedHeartbeats != 2 || res.SkippedHeartbeats != 0 {
		t.Fatalf("res = %+v, want 2 accepted/0 skipped", res)
	}
	// Verify the rows landed with source set.
	var got int
	if err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='backfill:git'`,
		sender).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("row count = %d, want 2", got)
	}
}

// TestInsertBackfillBatch_OverlapWithReal_SkipsSession is the whole
// point of the feature: never double-count. Seed one real (source IS
// NULL) heartbeat inside the candidate window and confirm the session
// is skipped entirely.
func TestInsertBackfillBatch_OverlapWithReal_SkipsSession(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfovlp")
	ctx := f.Ctx()
	sender := f.Sender()
	f.Projects("p1")

	realTime := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second)
	// Insert a real Wakatime-style row via the seed helper (leaves source NULL).
	f.Seed(hbSeed{project: "p1", ts: realTime, gap: 60, entity: "editor.go"})

	sess := BackfillSession{
		Start: realTime.Add(-30 * time.Minute),
		End:   realTime.Add(30 * time.Minute),
		Heartbeats: []model.HeartbeatPayload{
			mkBackfillHB(sender, "p1", "main.go", realTime.Add(-10*time.Minute)),
			mkBackfillHB(sender, "p1", "main.go", realTime.Add(-8*time.Minute)),
		},
	}
	res, err := d.InsertBackfillBatch(ctx, BackfillBatch{
		Username:  sender,
		SourceTag: "backfill:git",
		Sessions:  []BackfillSession{sess},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if res.AcceptedHeartbeats != 0 || res.SkippedHeartbeats != 2 {
		t.Fatalf("res = %+v, want 0 accepted/2 skipped", res)
	}
	if res.Sessions[0].Reason != "overlap" {
		t.Errorf("Reason = %q, want overlap", res.Sessions[0].Reason)
	}
}

// TestInsertBackfillBatch_OverlapWithPriorBackfill_StillWrites verifies
// that a session overlapping only prior backfill rows (source != NULL)
// does NOT trigger the overlap skip — otherwise a rerun would drop
// everything. The unique_heartbeats constraint absorbs duplicates.
func TestInsertBackfillBatch_OverlapWithPriorBackfill_StillWrites(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfprior")
	ctx := f.Ctx()
	sender := f.Sender()

	hbs := []model.HeartbeatPayload{
		mkBackfillHB(sender, "p1", "main.go", time.Now().Add(-90*time.Minute).UTC().Truncate(time.Second)),
	}
	sess := BackfillSession{
		Start:      time.Now().Add(-95 * time.Minute).UTC().Truncate(time.Second),
		End:        time.Now().Add(-85 * time.Minute).UTC().Truncate(time.Second),
		Heartbeats: hbs,
	}
	// Run 1: should insert.
	if _, err := d.InsertBackfillBatch(ctx, BackfillBatch{
		Username:  sender,
		SourceTag: "backfill:git",
		Sessions:  []BackfillSession{sess},
	}); err != nil {
		t.Fatal(err)
	}
	// Run 2 with the same shape: session should NOT be skipped (prior
	// backfill row has source != NULL, so overlap check is False). The
	// ON CONFLICT constraint then absorbs the duplicate row.
	res, err := d.InsertBackfillBatch(ctx, BackfillBatch{
		Username:  sender,
		SourceTag: "backfill:git",
		Sessions:  []BackfillSession{sess},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SkippedHeartbeats != 0 {
		t.Errorf("second run skipped = %d, want 0", res.SkippedHeartbeats)
	}
	// Verify only ONE physical row exists.
	var got int
	if err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='backfill:git'`,
		sender).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("row count = %d, want 1 (idempotency)", got)
	}
}

// TestDeleteBackfilledHeartbeats_PreservesRealRows verifies the danger-
// zone delete never touches source-NULL rows.
func TestDeleteBackfilledHeartbeats_PreservesRealRows(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfdel")
	ctx := f.Ctx()
	sender := f.Sender()
	f.Projects("p1")

	realTime := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	f.Seed(hbSeed{project: "p1", ts: realTime, entity: "real.go"})

	sess := BackfillSession{
		Start: time.Now().Add(-2 * time.Hour).UTC(),
		End:   time.Now().Add(-time.Hour).UTC(),
		Heartbeats: []model.HeartbeatPayload{
			mkBackfillHB(sender, "p1", "main.go", time.Now().Add(-90*time.Minute).UTC()),
		},
	}
	if _, err := d.InsertBackfillBatch(ctx, BackfillBatch{
		Username:  sender,
		SourceTag: "backfill:git",
		Sessions:  []BackfillSession{sess},
	}); err != nil {
		t.Fatal(err)
	}
	// Now delete backfill rows and verify the real row still exists.
	n, err := d.DeleteBackfilledHeartbeats(ctx, sender, "backfill:%")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 1 {
		t.Errorf("delete count = %d, want 1", n)
	}
	var realCount int
	if err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source IS NULL`,
		sender).Scan(&realCount); err != nil {
		t.Fatal(err)
	}
	if realCount != 1 {
		t.Errorf("real row count = %d, want 1 (delete leaked into real data)", realCount)
	}
}

// TestBackfillStatsFor_ReportsCounts inserts a couple of tagged rows
// and verifies the stats endpoint returns correct totals + per-source
// breakdown.
func TestBackfillStatsFor_ReportsCounts(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfstats")
	ctx := f.Ctx()
	sender := f.Sender()

	makeSess := func(tag string, when time.Time) BackfillSession {
		return BackfillSession{
			Start: when.Add(-5 * time.Minute),
			End:   when.Add(5 * time.Minute),
			Heartbeats: []model.HeartbeatPayload{
				mkBackfillHB(sender, "p1", "a.go", when),
			},
		}
	}
	_, err := d.InsertBackfillBatch(ctx, BackfillBatch{
		Username:  sender,
		SourceTag: "backfill:git",
		Sessions:  []BackfillSession{makeSess("backfill:git", time.Now().Add(-24*time.Hour).UTC().Truncate(time.Second))},
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := d.BackfillStatsFor(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRows != 1 {
		t.Errorf("TotalRows = %d, want 1", stats.TotalRows)
	}
	if stats.Sources["backfill:git"] != 1 {
		t.Errorf("Sources[backfill:git] = %d, want 1 (map=%v)", stats.Sources["backfill:git"], stats.Sources)
	}
}

// TestGetBackfillConfig_ReturnsDefaultsForNewUser verifies the "no
// row" path emits sensible defaults so the FE can render an editable
// form without a prior write.
func TestGetBackfillConfig_ReturnsDefaultsForNewUser(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfcfgnew")
	cfg, err := d.GetBackfillConfig(context.Background(), f.Sender())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClusterGapSec != 1800 || cfg.HeartbeatRateSec != 120 {
		t.Errorf("cfg = %+v, want defaults", cfg)
	}
	if cfg.SourceTag != "backfill:git" {
		t.Errorf("SourceTag = %q, want backfill:git", cfg.SourceTag)
	}
}

// TestSetBackfillConfig_Roundtrip verifies that a Set followed by a Get
// returns the same values (clamped where applicable).
func TestSetBackfillConfig_Roundtrip(t *testing.T) {
	d := openTestDB(t)
	f := newSender(t, d, "bfcfgrt")
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
	if err := d.SetBackfillConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetBackfillConfig(context.Background(), sender)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClusterGapSec != cfg.ClusterGapSec ||
		got.HeartbeatRateSec != cfg.HeartbeatRateSec ||
		got.SourceTag != cfg.SourceTag {
		t.Errorf("got=%+v want=%+v", got, cfg)
	}
	if got.LangMap["ts"] != "TypeScript" {
		t.Errorf("LangMap missing ts=TypeScript, got %+v", got.LangMap)
	}
}

// TestClampBackfillConfig_ForcesBackfillPrefix asserts that a caller
// setting SourceTag without the required prefix gets it repaired
// rather than 400'd. The DELETE / partial index / overlap filter all
// depend on the prefix so a "backfill-git" tag would fail silently.
func TestClampBackfillConfig_ForcesBackfillPrefix(t *testing.T) {
	c := clampBackfillConfig(BackfillConfig{SourceTag: "git-history"})
	if c.SourceTag != "backfill:git-history" {
		t.Errorf("SourceTag = %q, want backfill:git-history", c.SourceTag)
	}
}
