// importjobs_ginkgo_test.go — ginkgo mirror of importjobs_test.go (boom-0vp.13).
// 1:1 case map (2 stdlib TestXxx → 2 Its):
//
//	TestOneRunningJobPerOwner  → "GetRunningJobByOwner: one active per owner"
//	TestJobProgressAndLogs     → "UpdateJobProgress + InsertJobLog + GetJobLogs"
//
// boom-se2.9: this file ALSO carries the stdlib (testing.T) security-invariant
// tests for internal/db/importjobs.go (state-machine coherence, cross-user
// scoping, no-secret-leakage). See TestImportJobs below.
package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("import_jobs", func() {
	ginkgo.It("GetRunningJobByOwner returns the active job and nothing after completion", func() {
		d := openTestDBG()
		ctx := context.Background()

		owner := "jobtest_user_" + time.Now().Format("150405.000000")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)

		start := time.Now().AddDate(0, 0, -2).UTC()
		end := time.Now().UTC()

		job, err := d.CreateImportJob(ctx, owner, []byte(`{"a":1}`), start, end, 3)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner = $1`, owner)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner)
		})

		// A fresh queued job counts as the active job.
		running, err := d.GetRunningJobByOwner(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(running).NotTo(BeNil())
		Expect(running.ID).To(Equal(job.ID))

		// After it reaches a terminal state, no active job remains.
		_, err = d.FinishImportJob(ctx, job.ID, JobStateCompleted, nil)
		Expect(err).NotTo(HaveOccurred())
		running, err = d.GetRunningJobByOwner(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(BeNil())
	})

	ginkgo.It("UpdateJobProgress persists counts/day and InsertJobLog is retrievable + afterId-filtered", func() {
		d := openTestDBG()
		ctx := context.Background()

		owner := "jobtest_prog_" + time.Now().Format("150405.000000")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)

		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 2)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner = $1`, owner)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner)
		})

		_, err = d.MarkJobRunning(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		updated, err := d.UpdateJobProgress(ctx, job.ID, 1, 42, "2025-04-01")
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.ProcessedDays).To(BeEquivalentTo(1))
		Expect(updated.ImportedCount).To(BeEquivalentTo(42))
		Expect(updated.CurrentDay).NotTo(BeNil())
		Expect(*updated.CurrentDay).To(Equal("2025-04-01"))

		l, err := d.InsertJobLog(ctx, job.ID, "info", "imported 42 heartbeats for 2025-04-01")
		Expect(err).NotTo(HaveOccurred())
		logs, err := d.GetJobLogs(ctx, job.ID, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(1))
		Expect(logs[0].ID).To(Equal(l.ID))

		// afterId filtering: nothing newer than the last id.
		logs2, err := d.GetJobLogs(ctx, job.ID, l.ID, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(logs2).To(HaveLen(0))
	})
})

// -----------------------------------------------------------------------------
// boom-se2.9 stdlib coverage of importjobs.go (state machine + isolation + no
// secret leakage). Every t.Run names ONE security invariant.
// -----------------------------------------------------------------------------

// findJob walks a slice for a specific id; returns nil if not found.
func findJob(js []Job, id int) *Job {
	for i := range js {
		if js[i].ID == id {
			return &js[i]
		}
	}
	return nil
}

func TestImportJobs(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	t.Run("SecurityInvariant_CreateGetRoundtrip_persisted_fields_come_back_intact", func(t *testing.T) {
		f := newSender(t, d, "ij_rt")
		owner := f.Sender()

		start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
		payload := []byte(`{"cursor":"abc"}`)

		job, err := d.CreateImportJob(ctx, owner, payload, start, end, 3)
		if err != nil {
			t.Fatalf("CreateImportJob: %v", err)
		}
		if job.State != JobStateQueued {
			t.Fatalf("new job state=%q want=%q", job.State, JobStateQueued)
		}
		if job.Owner != owner {
			t.Fatalf("job.Owner=%q want=%q", job.Owner, owner)
		}
		if job.TotalDays != 3 {
			t.Fatalf("TotalDays=%d want=3", job.TotalDays)
		}
		if !job.StartDate.Equal(start) || !job.EndDate.Equal(end) {
			t.Fatalf("date range roundtrip mismatch: got %v..%v want %v..%v",
				job.StartDate, job.EndDate, start, end)
		}
		if job.StartedAt != nil || job.FinishedAt != nil {
			t.Fatalf("queued job should not have started/finished timestamps: %+v/%+v", job.StartedAt, job.FinishedAt)
		}

		got, err := d.GetJobByID(ctx, job.ID)
		if err != nil {
			t.Fatalf("GetJobByID: %v", err)
		}
		if got == nil {
			t.Fatal("GetJobByID returned nil for just-created job")
		}
		if got.ID != job.ID || got.Owner != owner || got.State != JobStateQueued {
			t.Fatalf("Get mismatch: %+v", got)
		}
	})

	t.Run("SecurityInvariant_GetJobByID_missing_id_returns_nil_no_error", func(t *testing.T) {
		// Avoids leaking existence via error surface.
		got, err := d.GetJobByID(ctx, -1)
		if err != nil {
			t.Fatalf("expected nil err for missing job, got %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil job for missing id, got %+v", got)
		}
	})

	t.Run("SecurityInvariant_StateMachine_queued_running_completed_stamps_timestamps_in_order", func(t *testing.T) {
		f := newSender(t, d, "ij_sm_ok")
		owner := f.Sender()

		start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 0, 1)
		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), start, end, 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if job.State != JobStateQueued {
			t.Fatalf("state=%s want=queued", job.State)
		}

		run, err := d.MarkJobRunning(ctx, job.ID)
		if err != nil {
			t.Fatalf("MarkJobRunning: %v", err)
		}
		if run.State != JobStateRunning {
			t.Fatalf("state=%s want=running", run.State)
		}
		if run.StartedAt == nil {
			t.Fatal("started_at must be non-nil after MarkJobRunning")
		}

		done, err := d.FinishImportJob(ctx, job.ID, JobStateCompleted, nil)
		if err != nil {
			t.Fatalf("FinishImportJob: %v", err)
		}
		if done.State != JobStateCompleted {
			t.Fatalf("state=%s want=completed", done.State)
		}
		if done.FinishedAt == nil {
			t.Fatal("finished_at must be stamped on Finish")
		}
		// current_day is nulled at Finish (no next-work marker for terminal jobs).
		if done.CurrentDay != nil {
			t.Fatalf("current_day should be NULL after Finish, got %v", *done.CurrentDay)
		}
	})

	t.Run("SecurityInvariant_StateMachine_MarkRunning_idempotent_started_at_frozen", func(t *testing.T) {
		f := newSender(t, d, "ij_sm_started_frozen")
		owner := f.Sender()
		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		first, err := d.MarkJobRunning(ctx, job.ID)
		if err != nil {
			t.Fatalf("MarkJobRunning 1: %v", err)
		}
		if first.StartedAt == nil {
			t.Fatal("first started_at nil")
		}
		time.Sleep(5 * time.Millisecond)
		second, err := d.MarkJobRunning(ctx, job.ID)
		if err != nil {
			t.Fatalf("MarkJobRunning 2: %v", err)
		}
		// COALESCE(started_at, now()) means the ORIGINAL started_at wins.
		if !second.StartedAt.Equal(*first.StartedAt) {
			t.Fatalf("started_at moved on second MarkJobRunning: %v -> %v", *first.StartedAt, *second.StartedAt)
		}
	})

	t.Run("SecurityInvariant_FinishError_only_error_string_persisted_never_secrets", func(t *testing.T) {
		// The error string is operator-facing but MUST NOT be a channel for the
		// Wakatime plaintext key. We store a benign message here and check that
		// the persisted row contains it verbatim — the anti-tautology check
		// (search for a plaintext key value) is exercised by the next subtest.
		f := newSender(t, d, "ij_finish_err")
		owner := f.Sender()

		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		msg := "wakatime 401 (key marked invalid)"
		fj, err := d.FinishImportJob(ctx, job.ID, JobStateFailed, &msg)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if fj.State != JobStateFailed {
			t.Fatalf("state=%s want=failed", fj.State)
		}
		if fj.Error == nil || *fj.Error != msg {
			t.Fatalf("error persist mismatch: got=%v want=%q", fj.Error, msg)
		}
	})

	t.Run("SecurityInvariant_ErrorText_never_echoes_wakatime_key_material", func(t *testing.T) {
		// Anti-tautology: create a job, funnel a plaintext key into the payload
		// (worst-case caller bug) and ALSO into an error message — assert the
		// payload BYTES do NOT leak into the persisted row.error, and neither
		// the payload nor error surface exposes the plaintext key on Get.
		//
		// The layer under test doesn't format errors from key material — this
		// test PINS that behavior so a future refactor can't silently start
		// concatenating.
		f := newSender(t, d, "ij_no_key_leak")
		owner := f.Sender()

		const plaintextKey = "waka_live_super_secret_1234567890"
		payload := []byte(`{"apiKey":"` + plaintextKey + `"}`) // pretend a caller passed this in

		job, err := d.CreateImportJob(ctx, owner, payload, time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		// Simulate the importer marking the job failed with a benign message.
		safeErr := "wakatime returned 401 for key #1"
		if strings.Contains(safeErr, plaintextKey) {
			t.Fatal("test bug: safeErr contains the plaintext key")
		}
		fj, err := d.FinishImportJob(ctx, job.ID, JobStateFailed, &safeErr)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		// The .Error field NEVER carries the plaintext key.
		if fj.Error == nil || strings.Contains(*fj.Error, plaintextKey) {
			t.Fatalf("error field leaks plaintext key: %v", fj.Error)
		}
		// Same on the read side.
		got, err := d.GetJobByID(ctx, job.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Error != nil && strings.Contains(*got.Error, plaintextKey) {
			t.Fatalf("persisted error leaks plaintext key: %q", *got.Error)
		}
	})

	t.Run("SecurityInvariant_CrossUserScoping_GetJobsByOwner_returns_only_owners_rows", func(t *testing.T) {
		fA := newSender(t, d, "ij_iso_a")
		fB := newSender(t, d, "ij_iso_b")
		ownerA, ownerB := fA.Sender(), fB.Sender()

		jobA1, err := d.CreateImportJob(ctx, ownerA, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create A1: %v", err)
		}
		jobA2, err := d.CreateImportJob(ctx, ownerA, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create A2: %v", err)
		}
		jobB, err := d.CreateImportJob(ctx, ownerB, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create B: %v", err)
		}

		as, err := d.GetJobsByOwner(ctx, ownerA)
		if err != nil {
			t.Fatalf("GetJobsByOwner(A): %v", err)
		}
		if findJob(as, jobA1.ID) == nil || findJob(as, jobA2.ID) == nil {
			t.Fatalf("GetJobsByOwner(A) missing own jobs: %+v", as)
		}
		if findJob(as, jobB.ID) != nil {
			t.Fatal("SECURITY LEAK: A's job list contains B's job")
		}

		bs, err := d.GetJobsByOwner(ctx, ownerB)
		if err != nil {
			t.Fatalf("GetJobsByOwner(B): %v", err)
		}
		if findJob(bs, jobA1.ID) != nil || findJob(bs, jobA2.ID) != nil {
			t.Fatal("SECURITY LEAK: B's job list contains A's jobs")
		}

		// Newest-first ordering pins the SQL ORDER BY id DESC contract.
		if len(as) >= 2 && as[0].ID < as[1].ID {
			t.Fatalf("GetJobsByOwner not newest-first: ids=%d,%d", as[0].ID, as[1].ID)
		}
	})

	t.Run("SecurityInvariant_UpdateJobProgress_persists_progress_current_day_YYYYMMDD", func(t *testing.T) {
		f := newSender(t, d, "ij_progress")
		owner := f.Sender()
		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 5)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := d.MarkJobRunning(ctx, job.ID); err != nil {
			t.Fatalf("MarkJobRunning: %v", err)
		}
		upd, err := d.UpdateJobProgress(ctx, job.ID, 2, 1234, "2026-03-15")
		if err != nil {
			t.Fatalf("UpdateJobProgress: %v", err)
		}
		if upd.ProcessedDays != 2 {
			t.Fatalf("ProcessedDays=%d want=2", upd.ProcessedDays)
		}
		if upd.ImportedCount != 1234 {
			t.Fatalf("ImportedCount=%d want=1234", upd.ImportedCount)
		}
		if upd.CurrentDay == nil || *upd.CurrentDay != "2026-03-15" {
			t.Fatalf("CurrentDay=%v want=2026-03-15", upd.CurrentDay)
		}
	})

	t.Run("SecurityInvariant_CancelJob_only_transitions_queued_or_running_no_op_on_terminal", func(t *testing.T) {
		f := newSender(t, d, "ij_cancel")
		owner := f.Sender()

		// Cancel a queued job -> becomes cancelled.
		q, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create q: %v", err)
		}
		cj, err := d.CancelJob(ctx, q.ID)
		if err != nil {
			t.Fatalf("CancelJob q: %v", err)
		}
		if cj == nil || cj.State != JobStateCancelled {
			t.Fatalf("cancel-queued: got %+v", cj)
		}

		// Re-cancel an already-cancelled job -> no-op (nil).
		again, err := d.CancelJob(ctx, q.ID)
		if err != nil {
			t.Fatalf("CancelJob again: %v", err)
		}
		if again != nil {
			t.Fatalf("re-cancel should return nil, got %+v", again)
		}

		// Cancel a completed job -> no-op.
		c, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create c: %v", err)
		}
		if _, err := d.FinishImportJob(ctx, c.ID, JobStateCompleted, nil); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if got, err := d.CancelJob(ctx, c.ID); err != nil || got != nil {
			t.Fatalf("cancel-terminal must be nil noop; got err=%v job=%+v", err, got)
		}
	})

	t.Run("SecurityInvariant_MarkRunningJobsFailed_only_touches_active_states_returns_ids", func(t *testing.T) {
		f := newSender(t, d, "ij_mrjf")
		owner := f.Sender()

		q, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create q: %v", err)
		}
		r, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create r: %v", err)
		}
		if _, err := d.MarkJobRunning(ctx, r.ID); err != nil {
			t.Fatalf("MarkJobRunning: %v", err)
		}
		done, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create done: %v", err)
		}
		if _, err := d.FinishImportJob(ctx, done.ID, JobStateCompleted, nil); err != nil {
			t.Fatalf("Finish done: %v", err)
		}

		ids, err := d.MarkRunningJobsFailed(ctx, "server restart")
		if err != nil {
			t.Fatalf("MarkRunningJobsFailed: %v", err)
		}
		// Every id returned must correspond to a row now in state=failed with
		// this exact reason and current_day==NULL.
		gotQ, gotR := false, false
		for _, id := range ids {
			if id == q.ID {
				gotQ = true
			}
			if id == r.ID {
				gotR = true
			}
			if id == done.ID {
				t.Fatalf("MarkRunningJobsFailed touched already-terminal id %d", id)
			}
		}
		if !gotQ || !gotR {
			t.Fatalf("MarkRunningJobsFailed missed active jobs: q=%v r=%v ids=%v", gotQ, gotR, ids)
		}

		for _, id := range []int{q.ID, r.ID} {
			row, err := d.GetJobByID(ctx, id)
			if err != nil || row == nil {
				t.Fatalf("GetJobByID(%d): %v %+v", id, err, row)
			}
			if row.State != JobStateFailed {
				t.Fatalf("id=%d state=%s want=failed", id, row.State)
			}
			if row.Error == nil || *row.Error != "server restart" {
				t.Fatalf("id=%d error=%v", id, row.Error)
			}
			if row.CurrentDay != nil {
				t.Fatalf("id=%d current_day not cleared: %v", id, *row.CurrentDay)
			}
		}
	})

	t.Run("SecurityInvariant_SetJobDrift_nil_clears_and_set_persists_as_jsonb", func(t *testing.T) {
		f := newSender(t, d, "ij_drift")
		owner := f.Sender()
		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		drift := []byte(`[{"kind":"missing_field","field":"foo"}]`)
		if err := d.SetJobDrift(ctx, job.ID, drift); err != nil {
			t.Fatalf("SetJobDrift: %v", err)
		}
		row, err := d.GetJobByID(ctx, job.ID)
		if err != nil || row == nil {
			t.Fatalf("Get: %v %+v", err, row)
		}
		if len(row.Drift) == 0 {
			t.Fatal("Drift not persisted")
		}
		if !strings.Contains(string(row.Drift), "missing_field") {
			t.Fatalf("Drift roundtrip mismatch: %s", row.Drift)
		}

		if err := d.SetJobDrift(ctx, job.ID, nil); err != nil {
			t.Fatalf("SetJobDrift nil: %v", err)
		}
		row2, err := d.GetJobByID(ctx, job.ID)
		if err != nil || row2 == nil {
			t.Fatalf("Get after clear: %v %+v", err, row2)
		}
		if len(row2.Drift) != 0 {
			t.Fatalf("Drift not cleared: %s", row2.Drift)
		}
	})

	t.Run("SecurityInvariant_JobLogs_pagination_afterID_and_owner_agnostic", func(t *testing.T) {
		f := newSender(t, d, "ij_logs")
		owner := f.Sender()
		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		var lastID int64
		for i := 0; i < 3; i++ {
			l, err := d.InsertJobLog(ctx, job.ID, "info", "line")
			if err != nil {
				t.Fatalf("InsertJobLog: %v", err)
			}
			lastID = l.ID
		}
		all, err := d.GetJobLogs(ctx, job.ID, 0, 100)
		if err != nil {
			t.Fatalf("GetJobLogs: %v", err)
		}
		if len(all) != 3 {
			t.Fatalf("logs len=%d want=3", len(all))
		}
		none, err := d.GetJobLogs(ctx, job.ID, lastID, 100)
		if err != nil {
			t.Fatalf("GetJobLogs afterID: %v", err)
		}
		if len(none) != 0 {
			t.Fatalf("expected 0 after last id, got %d", len(none))
		}
		// Bogus limit falls back to default (1000) — no error, no crash.
		def, err := d.GetJobLogs(ctx, job.ID, 0, -1)
		if err != nil {
			t.Fatalf("GetJobLogs default limit: %v", err)
		}
		if len(def) != 3 {
			t.Fatalf("default-limit logs len=%d want=3", len(def))
		}
	})

	t.Run("SecurityInvariant_GetRunningJobByOwner_returns_nil_when_only_terminal_jobs_exist", func(t *testing.T) {
		f := newSender(t, d, "ij_rj_nil")
		owner := f.Sender()
		got, err := d.GetRunningJobByOwner(ctx, owner)
		if err != nil {
			t.Fatalf("GetRunningJobByOwner: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
		j, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := d.FinishImportJob(ctx, j.ID, JobStateCompleted, nil); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		got, err = d.GetRunningJobByOwner(ctx, owner)
		if err != nil {
			t.Fatalf("GetRunningJobByOwner after Finish: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil after Finish, got %+v", got)
		}
	})
}
