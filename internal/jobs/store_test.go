package jobs

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
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

	job, ok, err := s.ClaimNext(ctx, "w1")
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
	if _, ok, _ := s.ClaimNext(ctx, "w2"); ok {
		t.Fatal("second ClaimNext should find nothing")
	}
	if err := s.Complete(ctx, id); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestStoreRetryThenTerminal(t *testing.T) {
	s, ctx := newTestStore(t)
	id, _ := s.Enqueue(ctx, "demo", "", nil, 3, time.Time{})

	job, _, _ := s.ClaimNext(ctx, "w1")
	// Fail with a past retryAt → immediately re-claimable, attempt preserved.
	past := time.Now().Add(-time.Minute)
	if err := s.Fail(ctx, job.ID, "boom", &past); err != nil {
		t.Fatalf("Fail (retry): %v", err)
	}
	job2, ok, _ := s.ClaimNext(ctx, "w1")
	if !ok || job2.ID != id || job2.Attempts != 2 {
		t.Fatalf("retry not re-claimed: ok=%v %+v", ok, job2)
	}

	// Terminal fail → never claimable again.
	if err := s.Fail(ctx, job2.ID, "boom again", nil); err != nil {
		t.Fatalf("Fail (terminal): %v", err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "w1"); ok {
		t.Fatal("terminal-failed job should not be claimable")
	}
}

func TestStoreRunAtGatesClaim(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.Enqueue(ctx, "later", "", nil, 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, _ := s.ClaimNext(ctx, "w1"); ok {
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
