package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Import job states.
const (
	JobStateQueued    = "queued"
	JobStateRunning   = "running"
	JobStateCompleted = "completed"
	JobStateFailed    = "failed"
	JobStateCancelled = "cancelled"
)

// Job is a durable import job record. JSON tags follow the shared FE contract.
type Job struct {
	ID            int        `json:"id"`
	Owner         string     `json:"owner"`
	State         string     `json:"state"`
	StartDate     time.Time  `json:"startDate"`
	EndDate       time.Time  `json:"endDate"`
	TotalDays     int        `json:"totalDays"`
	ProcessedDays int        `json:"processedDays"`
	ImportedCount int64      `json:"importedCount"`
	CurrentDay    *string    `json:"currentDay"` // "YYYY-MM-DD" or null
	Error         *string    `json:"error"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
	// Drift is a JSON array of wakatime.com API schema-drift findings observed
	// during the run (boom-unq.1). Stored as raw JSON so this package doesn't
	// import the importer package. Nil when no drift was recorded.
	Drift json.RawMessage `json:"drift,omitempty"`
}

// LogLine is one durable log entry for a job.
type LogLine struct {
	ID      int64     `json:"id"`
	Ts      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// jobColumns is the shared SELECT list; current_day is rendered as text.
const jobColumns = `id, owner, state, start_date, end_date,
	COALESCE(total_days, 0), processed_days, imported_count,
	to_char(current_day, 'YYYY-MM-DD'), error, created_at, started_at, finished_at, drift`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	// drift is JSONB and nullable; scan into a *[]byte then normalize to
	// RawMessage so JSON round-trips as null (not "" / base64) when absent.
	var drift *[]byte
	if err := row.Scan(&j.ID, &j.Owner, &j.State, &j.StartDate, &j.EndDate,
		&j.TotalDays, &j.ProcessedDays, &j.ImportedCount,
		&j.CurrentDay, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &drift); err != nil {
		return nil, err
	}
	if drift != nil {
		j.Drift = json.RawMessage(*drift)
	}
	return &j, nil
}

// CreateImportJob inserts a queued job with its value payload and date range.
func (d *DB) CreateImportJob(ctx context.Context, owner string, payload []byte, start, end time.Time, totalDays int) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO import_jobs (value, state, owner, start_date, end_date, total_days)
		VALUES ($1, 'queued', $2, $3, $4, $5)
		RETURNING `+jobColumns, payload, owner, start, end, totalDays)
	return scanJob(row)
}

// GetJobByID returns a single job (no owner filter; caller must own-check).
func (d *DB) GetJobByID(ctx context.Context, id int) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM import_jobs WHERE id = $1`, id)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// GetJobsByOwner lists a user's jobs, newest first.
func (d *DB) GetJobsByOwner(ctx context.Context, owner string) ([]Job, error) {
	rows, err := d.Pool.Query(ctx, `SELECT `+jobColumns+` FROM import_jobs WHERE owner = $1 ORDER BY id DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// GetRunningJobByOwner returns the queued/running job for an owner, if any
// (enforces one active job per owner). Newest first.
func (d *DB) GetRunningJobByOwner(ctx context.Context, owner string) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `SELECT `+jobColumns+`
		FROM import_jobs
		WHERE owner = $1 AND state IN ('queued','running')
		ORDER BY id DESC LIMIT 1`, owner)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// MarkJobRunning transitions a job to running and stamps started_at.
func (d *DB) MarkJobRunning(ctx context.Context, id int) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `
		UPDATE import_jobs
		SET state = 'running', started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1
		RETURNING `+jobColumns, id)
	return scanJob(row)
}

// UpdateJobProgress durably records per-day progress and returns the fresh row.
func (d *DB) UpdateJobProgress(ctx context.Context, id, processedDays int, importedCount int64, currentDay string) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `
		UPDATE import_jobs
		SET processed_days = $2, imported_count = $3, current_day = $4::date, updated_at = now()
		WHERE id = $1
		RETURNING `+jobColumns, id, processedDays, importedCount, currentDay)
	return scanJob(row)
}

// FinishImportJob sets a terminal state, error (nullable), and finished_at.
func (d *DB) FinishImportJob(ctx context.Context, id int, state string, errMsg *string) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `
		UPDATE import_jobs
		SET state = $2, error = $3, finished_at = now(), current_day = NULL, updated_at = now()
		WHERE id = $1
		RETURNING `+jobColumns, id, state, errMsg)
	return scanJob(row)
}

// CancelJob marks a job cancelled only if it is still queued/running.
// Returns the updated job, or nil if it was already terminal.
func (d *DB) CancelJob(ctx context.Context, id int) (*Job, error) {
	row := d.Pool.QueryRow(ctx, `
		UPDATE import_jobs
		SET state = 'cancelled', error = 'cancelled by user', finished_at = now(), current_day = NULL, updated_at = now()
		WHERE id = $1 AND state IN ('queued','running')
		RETURNING `+jobColumns, id)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// SetJobDrift persists a JSON-encoded array of schema-drift findings on the
// job row. The importer package builds the payload (see internal/importer/drift.go).
// Passing nil clears the column. Called before FinishImportJob so terminal
// snapshots carry drift for historical runs and WS clients.
func (d *DB) SetJobDrift(ctx context.Context, id int, drift []byte) error {
	// drift is nullable JSONB. json.RawMessage / []byte is emitted by pgx as
	// bytea if we don't tell it otherwise; cast on the server side.
	if len(drift) == 0 {
		_, err := d.Pool.Exec(ctx, `UPDATE import_jobs SET drift = NULL, updated_at = now() WHERE id = $1`, id)
		return err
	}
	_, err := d.Pool.Exec(ctx, `UPDATE import_jobs SET drift = $2::jsonb, updated_at = now() WHERE id = $1`, id, string(drift))
	return err
}

// ImportJobStaleAfter is the lease TTL for an in-flight import job (boom-1vwl).
//
// A live run touches its row on EVERY processed day (UpdateJobProgress sets
// updated_at = now()), and a single day is one wakatime.com request bounded by
// the importer's 60s client timeout — so a healthy import refreshes the lease
// well inside this window. Anything older than the TTL has no live goroutine
// behind it and is safe to reclaim.
const ImportJobStaleAfter = 10 * time.Minute

// MarkRunningJobsFailed marks EVERY queued/running job as failed, fleet-wide,
// with no staleness or ownership scoping.
//
// boom-1vwl: this is deliberately NOT the startup-recovery path any more. In
// the split server/worker (+ KEDA drain-pod) topology every pod boot ran this
// and killed the server pod's live import mid-run. Production recovery goes
// through MarkStaleJobsFailed; this remains only as a blunt test/cleanup
// helper (many suites call it to drain jobs before DB teardown).
// Returns affected job ids.
func (d *DB) MarkRunningJobsFailed(ctx context.Context, reason string) ([]int, error) {
	return d.markJobsFailed(ctx, reason, 0, "")
}

// MarkStaleJobsFailed marks queued/running import jobs whose row has not been
// touched for at least staleAfter as failed, returning the affected ids
// (boom-1vwl). A job whose worker is alive keeps refreshing updated_at, so it
// is never reclaimed by another process's boot sweep.
//
// owner == "" sweeps fleet-wide (startup recovery); a non-empty owner scopes
// the sweep to that user's rows (the submit path reclaims only its own
// caller's zombie so a user action never writes to other owners' jobs).
func (d *DB) MarkStaleJobsFailed(ctx context.Context, reason string, staleAfter time.Duration, owner string) ([]int, error) {
	if staleAfter <= 0 {
		staleAfter = ImportJobStaleAfter
	}
	return d.markJobsFailed(ctx, reason, staleAfter, owner)
}

// markJobsFailed is the shared UPDATE. staleAfter <= 0 disables the lease
// predicate entirely (MarkRunningJobsFailed's blunt behavior); owner == ""
// disables the ownership predicate.
func (d *DB) markJobsFailed(ctx context.Context, reason string, staleAfter time.Duration, owner string) ([]int, error) {
	var (
		staleSecs any
		ownerArg  any
	)
	if staleAfter > 0 {
		staleSecs = staleAfter.Seconds()
	}
	if owner != "" {
		ownerArg = owner
	}
	rows, err := d.Pool.Query(ctx, `
		UPDATE import_jobs
		SET state = 'failed', error = $1, finished_at = now(), current_day = NULL, updated_at = now()
		WHERE state IN ('queued','running')
		  AND ($2::double precision IS NULL
		       OR updated_at < now() - ($2::double precision * interval '1 second'))
		  AND ($3::text IS NULL OR owner = $3)
		RETURNING id`, reason, staleSecs, ownerArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// InsertJobLog appends a log line and returns the persisted record.
func (d *DB) InsertJobLog(ctx context.Context, jobID int, level, message string) (*LogLine, error) {
	var l LogLine
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO import_job_logs (job_id, level, message)
		VALUES ($1, $2, $3)
		RETURNING id, ts, level, message`, jobID, level, message).Scan(&l.ID, &l.Ts, &l.Level, &l.Message)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// GetJobLogs returns log lines with id > afterID, oldest first, capped by limit.
func (d *DB) GetJobLogs(ctx context.Context, jobID int, afterID int64, limit int) ([]LogLine, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := d.Pool.Query(ctx, `
		SELECT id, ts, level, message FROM import_job_logs
		WHERE job_id = $1 AND id > $2
		ORDER BY id ASC LIMIT $3`, jobID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.ID, &l.Ts, &l.Level, &l.Message); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
