package db

import (
	"context"
	"testing"
	"time"
)

// openTestDB / truncateAll live in main_test.go (they use the isolated
// boomtime_test database provisioned by TestMain).

// TestOneRunningJobPerOwner verifies GetRunningJobByOwner returns the active job
// so the handler can avoid starting a second one.
func TestOneRunningJobPerOwner(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	owner := "jobtest_user_" + time.Now().Format("150405.000000")
	// Ensure FK-satisfying user row exists (owner is a plain TEXT here, but keep clean).
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)

	start := time.Now().AddDate(0, 0, -2).UTC()
	end := time.Now().UTC()

	job, err := d.CreateImportJob(ctx, owner, []byte(`{"a":1}`), start, end, 3)
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner = $1`, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner)
	})

	// A fresh queued job counts as the active job.
	running, err := d.GetRunningJobByOwner(ctx, owner)
	if err != nil {
		t.Fatalf("GetRunningJobByOwner: %v", err)
	}
	if running == nil || running.ID != job.ID {
		t.Fatalf("expected active job %d, got %v", job.ID, running)
	}

	// After it reaches a terminal state, no active job remains.
	if _, err := d.FinishImportJob(ctx, job.ID, JobStateCompleted, nil); err != nil {
		t.Fatalf("FinishImportJob: %v", err)
	}
	running, err = d.GetRunningJobByOwner(ctx, owner)
	if err != nil {
		t.Fatalf("GetRunningJobByOwner after finish: %v", err)
	}
	if running != nil {
		t.Fatalf("expected no active job after completion, got %+v", running)
	}
}

// TestJobProgressAndLogs exercises durable progress updates and log persistence.
func TestJobProgressAndLogs(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	owner := "jobtest_prog_" + time.Now().Format("150405.000000")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)

	job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 2)
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner = $1`, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner)
	})

	if _, err := d.MarkJobRunning(ctx, job.ID); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	updated, err := d.UpdateJobProgress(ctx, job.ID, 1, 42, "2025-04-01")
	if err != nil {
		t.Fatalf("UpdateJobProgress: %v", err)
	}
	if updated.ProcessedDays != 1 || updated.ImportedCount != 42 {
		t.Fatalf("progress = %+v, want processed=1 imported=42", updated)
	}
	if updated.CurrentDay == nil || *updated.CurrentDay != "2025-04-01" {
		t.Fatalf("currentDay = %v, want 2025-04-01", updated.CurrentDay)
	}

	l, err := d.InsertJobLog(ctx, job.ID, "info", "imported 42 heartbeats for 2025-04-01")
	if err != nil {
		t.Fatalf("InsertJobLog: %v", err)
	}
	logs, err := d.GetJobLogs(ctx, job.ID, 0, 100)
	if err != nil {
		t.Fatalf("GetJobLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].ID != l.ID {
		t.Fatalf("logs = %+v, want 1 entry id=%d", logs, l.ID)
	}
	// afterId filtering: nothing newer than the last id.
	logs2, err := d.GetJobLogs(ctx, job.ID, l.ID, 100)
	if err != nil {
		t.Fatalf("GetJobLogs afterId: %v", err)
	}
	if len(logs2) != 0 {
		t.Fatalf("expected no logs after id=%d, got %d", l.ID, len(logs2))
	}
}
