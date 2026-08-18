package jobs

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// newTestStore opens the pool directly (via db.MigrateURL) rather than through
// testutil — testutil imports internal/admin, which imports this package, so
// pulling it into a jobs test would be an import cycle. db is low-level and
// imports neither, so this stays acyclic.
func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("BOOM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("BOOM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if err := db.MigrateURL(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE jobs, job_schedules`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return NewStore(pool), ctx
}

func TestStoreEnqueueClaimComplete(t *testing.T) {
	s, ctx := newTestStore(t)

	id, err := s.Enqueue(ctx, "demo", "", json.RawMessage(`{"a":1}`), 2, time.Time{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == 0 {
		t.Fatal("Enqueue returned id 0")
	}

	job, ok, err := s.ClaimNext(ctx, "w1", nil, nil)
	if err != nil || !ok {
		t.Fatalf("ClaimNext: ok=%v err=%v", ok, err)
	}
	if job.ID != id || job.Kind != "demo" || job.Attempts != 1 || job.Status != StatusRunning {
		t.Fatalf("claimed job wrong: %+v", job)
	}
	if string(job.Payload) != `{"a": 1}` && string(job.Payload) != `{"a":1}` {
		t.Fatalf("payload = %s", job.Payload)
	}

	// The only job is now running → a second claim finds nothing.
	if _, ok, _ := s.ClaimNext(ctx, "w2", nil, nil); ok {
		t.Fatal("second ClaimNext should find nothing")
	}
	if err := s.Complete(ctx, id); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestStoreRetryThenTerminal(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 3, time.Time{})

	job, _, _ := s.ClaimNext(ctx, "w1", nil, nil)
	// Fail with a past retryAt → immediately re-claimable, attempt preserved.
	past := time.Now().Add(-time.Minute)
	if err := s.Fail(ctx, job.ID, "boom", &past); err != nil {
		t.Fatalf("Fail (retry): %v", err)
	}
	job2, ok, _ := s.ClaimNext(ctx, "w1", nil, nil)
	if !ok || job2.ID != id || job2.Attempts != 2 {
		t.Fatalf("retry not re-claimed: ok=%v %+v", ok, job2)
	}

	// Terminal fail → never claimable again.
	if err := s.Fail(ctx, job2.ID, "boom again", nil); err != nil {
		t.Fatalf("Fail (terminal): %v", err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); ok {
		t.Fatal("terminal-failed job should not be claimable")
	}
}

func TestStoreRunAtGatesClaim(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.Enqueue(ctx, "later", "", nil, 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "w1", nil, nil); ok {
		t.Fatal("a job with a future run_at must not be claimed yet")
	}
}

func TestStoreClaimByID(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 1, time.Time{})

	job, ok, err := s.ClaimByID(ctx, id, "w1")
	if err != nil || !ok || job.ID != id || job.Attempts != 1 {
		t.Fatalf("ClaimByID: ok=%v err=%v %+v", ok, err, job)
	}
	// A running job can't be re-claimed by id.
	if _, ok, _ := s.ClaimByID(ctx, id, "w2"); ok {
		t.Fatal("re-claim of a running job by id should fail")
	}
}

func TestClaimNextKindRouting(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.Enqueue(ctx, "light", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue light: %v", err)
	}
	if _, err := s.Enqueue(ctx, "heavy", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue heavy: %v", err)
	}

	// Server excludes "heavy" → it claims only "light", never "heavy".
	j, ok, err := s.ClaimNext(ctx, "server", nil, []string{"heavy"})
	if err != nil || !ok || j.Kind != "light" {
		t.Fatalf("exclude=[heavy] claimed %+v (ok=%v err=%v), want light", j, ok, err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "server", nil, []string{"heavy"}); ok {
		t.Fatal("server (exclude heavy) must not claim the heavy job")
	}

	// ScaledJob includes "heavy" → it claims it.
	j2, ok, err := s.ClaimNext(ctx, "scaledjob", []string{"heavy"}, nil)
	if err != nil || !ok || j2.Kind != "heavy" {
		t.Fatalf("include=[heavy] claimed %+v (ok=%v err=%v), want heavy", j2, ok, err)
	}
}

func TestListLatestPerOwnerAndHasPending(t *testing.T) {
	s, ctx := newTestStore(t)
	// Label "a": an older done job + a newer queued one. Label "b": one queued.
	older, err := s.Enqueue(ctx, "label-image", "a", nil, 1, time.Time{})
	if err != nil {
		t.Fatalf("enqueue older: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status='done', created_at = now() - interval '1 hour' WHERE id=$1`, older); err != nil {
		t.Fatalf("age older: %v", err)
	}
	newer, err := s.Enqueue(ctx, "label-image", "a", nil, 1, time.Time{})
	if err != nil {
		t.Fatalf("enqueue newer: %v", err)
	}
	if _, err := s.Enqueue(ctx, "label-image", "b", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}

	rows, err := s.ListLatestPerOwner(ctx, "label-image")
	if err != nil {
		t.Fatalf("ListLatestPerOwner: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 distinct owners, got %d: %+v", len(rows), rows)
	}
	byOwner := map[string]Job{}
	for _, j := range rows {
		byOwner[j.Owner] = j
	}
	if byOwner["a"].ID != newer {
		t.Fatalf("owner a latest = %d, want the newer %d", byOwner["a"].ID, newer)
	}

	// HasPending: "a" has the queued newer, "b" is queued, "c" has nothing.
	if ok, _ := s.HasPending(ctx, "label-image", "a"); !ok {
		t.Fatal("owner a should have a pending job")
	}
	if ok, _ := s.HasPending(ctx, "label-image", "c"); ok {
		t.Fatal("owner c should have no pending job")
	}
}

// TestMarkCancelled covers the three cases admin-cancel depends on: a QUEUED job
// flipped to cancelled is skipped by ClaimNext (so it never runs), MarkCancelled
// applies to a RUNNING job, and it is a guarded no-op against a TERMINAL job (no
// clobbering a done/failed row back to cancelled).
func TestMarkCancelled(t *testing.T) {
	s, ctx := newTestStore(t)

	// --- queued → cancelled → never claimable ---
	qid, err := s.Enqueue(ctx, "demo", "", nil, 1, time.Time{})
	if err != nil {
		t.Fatalf("Enqueue queued: %v", err)
	}
	ok, err := s.MarkCancelled(ctx, qid)
	if err != nil || !ok {
		t.Fatalf("MarkCancelled(queued): ok=%v err=%v, want ok=true", ok, err)
	}
	if _, claimed, _ := s.ClaimNext(ctx, "w1", nil, nil); claimed {
		t.Fatal("a cancelled (was-queued) job must not be claimable by ClaimNext")
	}
	if j, _, _ := s.Get(ctx, qid); j.Status != StatusCancelled || j.Error != "cancelled by admin" {
		t.Fatalf("cancelled job = status %q error %q, want cancelled + 'cancelled by admin'", j.Status, j.Error)
	}

	// --- running → cancelled (running is non-terminal) ---
	// A slightly-past run_at avoids sub-ms host-vs-DB clock skew making a
	// just-enqueued row read as not-yet-due; it's still the realistic state a
	// worker claims moments later.
	rid, _ := s.Enqueue(ctx, "demo", "", nil, 1, time.Now().Add(-time.Second))
	if _, claimed, _ := s.ClaimNext(ctx, "w1", nil, nil); !claimed {
		t.Fatal("expected to claim the running job")
	}
	if ok, err := s.MarkCancelled(ctx, rid); err != nil || !ok {
		t.Fatalf("MarkCancelled(running): ok=%v err=%v, want ok=true", ok, err)
	}
	if j, _, _ := s.Get(ctx, rid); j.Status != StatusCancelled {
		t.Fatalf("running job not cancelled → status %q", j.Status)
	}

	// --- done → MarkCancelled is a no-op, status preserved ---
	did, _ := s.Enqueue(ctx, "demo", "", nil, 1, time.Now().Add(-time.Second))
	if _, claimed, _ := s.ClaimNext(ctx, "w1", nil, nil); !claimed {
		t.Fatal("expected to claim the to-be-done job")
	}
	if err := s.Complete(ctx, did); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if ok, err := s.MarkCancelled(ctx, did); err != nil || ok {
		t.Fatalf("MarkCancelled(done): ok=%v err=%v, want ok=false (no clobber)", ok, err)
	}
	if j, _, _ := s.Get(ctx, did); j.Status != StatusDone {
		t.Fatalf("done job clobbered by MarkCancelled → status %q, want done", j.Status)
	}
}

func TestSchedulerClaimIsSingleton(t *testing.T) {
	s, ctx := newTestStore(t)
	if err := s.UpsertSchedule(ctx, "cron", time.Hour); err != nil {
		t.Fatalf("UpsertSchedule: %v", err)
	}
	// Freshly registered → next run is one interval out, not due.
	if kinds, _ := s.ClaimDueSchedules(ctx); len(kinds) != 0 {
		t.Fatalf("not-due schedule was claimed: %v", kinds)
	}
	// Force it due.
	if _, err := s.pool.Exec(ctx,
		`UPDATE job_schedules SET next_run_at = now() - interval '1 minute' WHERE kind = 'cron'`); err != nil {
		t.Fatalf("force due: %v", err)
	}
	kinds, err := s.ClaimDueSchedules(ctx)
	if err != nil {
		t.Fatalf("ClaimDueSchedules: %v", err)
	}
	if len(kinds) != 1 || kinds[0] != "cron" {
		t.Fatalf("due kinds = %v, want [cron]", kinds)
	}
	// The claim advanced next_run_at → an immediate re-claim gets nothing
	// (this is the leader-singleton guarantee across replicas).
	if k2, _ := s.ClaimDueSchedules(ctx); len(k2) != 0 {
		t.Fatalf("double-claim returned %v, want none", k2)
	}
}

// TestListJobKindStats seeds jobs across kinds, statuses, and times, then asserts
// the per-kind GROUP BY aggregate: live queued/running depth, trailing-hour
// done/failed throughput (an OLD done row is excluded), and the mean duration
// over that window. A kind with only a queued row reports zeroes + a zero avg.
func TestListJobKindStats(t *testing.T) {
	s, ctx := newTestStore(t)
	now := time.Now()
	ptr := func(tm time.Time) *time.Time { return &tm }

	// seed inserts a job then stamps it into a chosen status with explicit
	// started/finished times (the natural transitions can't backdate the clock).
	seed := func(kind, status string, started, finished *time.Time) {
		id, err := s.Enqueue(ctx, kind, "", nil, 1, time.Time{})
		if err != nil {
			t.Fatalf("enqueue %s/%s: %v", kind, status, err)
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE jobs SET status=$2, started_at=$3, finished_at=$4 WHERE id=$1`,
			id, status, started, finished); err != nil {
			t.Fatalf("seed %s/%s: %v", kind, status, err)
		}
	}

	within := now.Add(-10 * time.Minute) // comfortably inside the 1h window
	// alpha: 2 done (1s + 3s), 1 failed (2s) — all within the hour → avg 2000ms;
	//        1 running; 2 queued; plus 1 OLD done (finished 2h ago, excluded).
	seed("alpha", "done", ptr(within), ptr(within.Add(1*time.Second)))
	seed("alpha", "done", ptr(within), ptr(within.Add(3*time.Second)))
	seed("alpha", "failed", ptr(within), ptr(within.Add(2*time.Second)))
	seed("alpha", "running", ptr(now.Add(-1*time.Minute)), nil)
	if _, err := s.Enqueue(ctx, "alpha", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue alpha queued: %v", err)
	}
	if _, err := s.Enqueue(ctx, "alpha", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue alpha queued: %v", err)
	}
	old := now.Add(-2 * time.Hour)
	seed("alpha", "done", ptr(old), ptr(old.Add(5*time.Second)))

	// beta: a single queued row — the "known but idle" shape.
	if _, err := s.Enqueue(ctx, "beta", "", nil, 1, time.Time{}); err != nil {
		t.Fatalf("enqueue beta: %v", err)
	}

	stats, err := s.ListJobKindStats(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListJobKindStats: %v", err)
	}
	byKind := map[string]KindStats{}
	for _, ks := range stats {
		byKind[ks.Kind] = ks
	}

	a, ok := byKind["alpha"]
	if !ok {
		t.Fatalf("alpha missing from stats: %+v", stats)
	}
	if a.Queued != 2 {
		t.Errorf("alpha Queued = %d, want 2", a.Queued)
	}
	if a.Running != 1 {
		t.Errorf("alpha Running = %d, want 1", a.Running)
	}
	if a.DoneRecent != 2 { // the 2h-old done is excluded by the window
		t.Errorf("alpha DoneRecent = %d, want 2 (old done excluded)", a.DoneRecent)
	}
	if a.FailedRecent != 1 {
		t.Errorf("alpha FailedRecent = %d, want 1", a.FailedRecent)
	}
	if math.Abs(a.AvgDurationMs-2000) > 1 { // mean of 1000, 3000, 2000
		t.Errorf("alpha AvgDurationMs = %.1f, want ~2000", a.AvgDurationMs)
	}
	if a.LastRunAt == nil {
		t.Error("alpha LastRunAt = nil, want a timestamp")
	}
	if a.LastStatus == "" {
		t.Error("alpha LastStatus empty, want the most-recent row's status")
	}

	b, ok := byKind["beta"]
	if !ok {
		t.Fatalf("beta missing from stats: %+v", stats)
	}
	if b.Queued != 1 || b.Running != 0 || b.DoneRecent != 0 || b.FailedRecent != 0 {
		t.Errorf("beta = %+v, want only Queued=1", b)
	}
	if b.AvgDurationMs != 0 {
		t.Errorf("beta AvgDurationMs = %.1f, want 0 (nothing finished)", b.AvgDurationMs)
	}
	if b.LastStatus != StatusQueued {
		t.Errorf("beta LastStatus = %q, want queued", b.LastStatus)
	}
}
