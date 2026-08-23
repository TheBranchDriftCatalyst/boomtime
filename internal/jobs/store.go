package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed queue. It owns the `jobs` + `job_schedules`
// tables (migration 00054) and nothing else.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pgx pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const jobCols = `id, kind, owner, payload, status, attempts, max_attempts, error,
	run_at, created_at, started_at, finished_at`

// Enqueue inserts a queued job. maxAttempts < 1 is clamped to 1; a zero runAt
// means "now"; owner "" = a system job. Returns the new job id.
func (s *Store) Enqueue(ctx context.Context, kind, owner string, payload []byte, maxAttempts int, runAt time.Time) (int64, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if runAt.IsZero() {
		runAt = time.Now()
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO jobs (kind, owner, payload, max_attempts, run_at)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		kind, owner, payload, maxAttempts, runAt,
	).Scan(&id)
	return id, err
}

// ClaimNext atomically grabs the oldest due queued job, marks it running, bumps
// attempts, and returns it. ok=false when nothing is due. FOR UPDATE SKIP
// LOCKED makes it safe for many concurrent workers — each gets a distinct row.
//
// Kind-routing (boom-hney): `include` restricts to those kinds (empty = any);
// `exclude` skips those kinds. So the always-on server can drain light kinds
// (exclude the heavy ones) while a ScaledJob drains only the heavy kinds
// (include them) — all on the same queue, no double-claim.
func (s *Store) ClaimNext(ctx context.Context, workerID string, include, exclude []string) (*Job, bool, error) {
	if len(include) == 0 {
		include = nil
	}
	if len(exclude) == 0 {
		exclude = nil
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE jobs
		    SET status = 'running', attempts = attempts + 1,
		        started_at = now(), locked_by = $1, locked_at = now(),
		        heartbeat_at = now()
		  WHERE id = (
		        SELECT id FROM jobs
		         WHERE status = 'queued' AND run_at <= now()
		           AND ($2::text[] IS NULL OR kind = ANY($2::text[]))
		           AND ($3::text[] IS NULL OR kind <> ALL($3::text[]))
		         ORDER BY run_at
		         FOR UPDATE SKIP LOCKED
		         LIMIT 1
		  )
		  RETURNING `+jobCols,
		workerID, include, exclude,
	)
	j, err := scanJob(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return j, true, nil
}

// ClaimByID atomically claims a specific queued job by id (the AMQP path: the
// delivery carries the id). ok=false if it's already claimed, done, or gone.
func (s *Store) ClaimByID(ctx context.Context, id int64, workerID string) (*Job, bool, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE jobs
		    SET status = 'running', attempts = attempts + 1,
		        started_at = now(), locked_by = $2, locked_at = now(),
		        heartbeat_at = now()
		  WHERE id = $1 AND status = 'queued'
		  RETURNING `+jobCols,
		id, workerID)
	j, err := scanJob(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return j, true, nil
}

// Requeue puts a just-claimed job back to 'queued' WITHOUT bumping attempts, so
// a lost concurrency-slot race (Acquire returned ok=false after the row was
// already claimed) costs nothing — the row simply becomes claimable again for a
// later slot. Distinct from Fail's retry path, which advances run_at/error; here
// the job is immediately eligible again with a cleared started_at/locked_by.
func (s *Store) Requeue(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'queued', started_at = NULL,
		        locked_by = '', locked_at = NULL WHERE id = $1`, id)
	return err
}

// Complete marks a running job done.
func (s *Store) Complete(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'done', finished_at = now(), error = '' WHERE id = $1`, id)
	return err
}

// Fail records a failure. With a non-nil retryAt the job is re-queued to run
// then (the incremented attempt stands); with nil it becomes terminal.
func (s *Store) Fail(ctx context.Context, id int64, errMsg string, retryAt *time.Time) error {
	if retryAt != nil {
		_, err := s.pool.Exec(ctx,
			`UPDATE jobs SET status = 'queued', run_at = $2, error = $3,
			        locked_by = '', locked_at = NULL WHERE id = $1`,
			id, *retryAt, errMsg)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'failed', finished_at = now(), error = $2 WHERE id = $1`,
		id, errMsg)
	return err
}

// MarkCancelled transitions a job to the terminal 'cancelled' status with a clear
// error note, but ONLY from a non-terminal state ('queued' or 'running') — a job
// that already reached done/failed/cancelled is left untouched. Returns whether a
// row actually changed (i.e. the job was still queued or running).
//
// For a QUEUED job this alone stops it: ClaimNext filters on status = 'queued', so
// a 'cancelled' row is never claimed / never runs. For a RUNNING job it stamps the
// durable terminal status; the in-flight interruption is LocalProvider.Cancel(id),
// which cancels the handler's context (cooperative — the handler must honor ctx).
// Clearing locked_by/locked_at keeps the row's lock bookkeeping consistent with
// the other terminal transitions.
func (s *Store) MarkCancelled(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'cancelled', finished_at = now(),
		        error = 'cancelled by admin', locked_by = '', locked_at = NULL
		  WHERE id = $1 AND status IN ('queued','running')`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Heartbeat refreshes a running job's liveness stamp. The executing worker calls
// it on a ~30s tick for the duration of a handler run (see execute), so a LONG but
// LIVE job keeps a fresh heartbeat_at and is never mistaken for a dead one by
// ReapStaleRunning. Guarded on status='running': a heartbeat that races the job's
// own terminal transition (done/failed/cancelled) is a harmless no-op.
func (s *Store) Heartbeat(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() WHERE id = $1 AND status = 'running'`, id)
	return err
}

// ReapStaleRunning reclaims running rows whose worker died — the pod-restart
// zombie problem: a deploy leaves in-flight jobs status='running' forever, hanging
// in the admin UI, inflating the running count, and BLOCKING the per-kind
// concurrency cap so the scheduler's queued work piles up unbounded. A row is
// stale when its most recent liveness signal — COALESCE(heartbeat_at, locked_at,
// started_at) — is older than ttl. The COALESCE fallback is what lets the CURRENT
// backlog (heartbeat_at NULL, pre-heartbeat rows) get reclaimed on first boot.
//
// Each stale row is reset in ONE statement: still-retryable (attempts <
// max_attempts) → back to 'queued' (re-runnable now), else → terminal 'failed'.
// Guarded on status='running' AND stale, so it's idempotent and concurrency-safe:
// two pods reaping the same instant is harmless (the second UPDATE matches nothing
// a claim/reap already moved off 'running'). Returns how many rows were reclaimed.
func (s *Store) ReapStaleRunning(ctx context.Context, ttl time.Duration) (int, error) {
	secs := ttl.Seconds()
	if secs < 1 {
		secs = 1
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET
		    status       = CASE WHEN attempts < max_attempts THEN 'queued' ELSE 'failed' END,
		    error        = CASE WHEN attempts < max_attempts
		                        THEN 'reclaimed: worker lost (pod restart)'
		                        ELSE 'worker lost (pod restart)' END,
		    run_at       = CASE WHEN attempts < max_attempts THEN now()  ELSE run_at      END,
		    started_at   = CASE WHEN attempts < max_attempts THEN NULL    ELSE started_at  END,
		    finished_at  = CASE WHEN attempts < max_attempts THEN finished_at ELSE now()   END,
		    locked_by    = CASE WHEN attempts < max_attempts THEN ''      ELSE locked_by   END,
		    locked_at    = CASE WHEN attempts < max_attempts THEN NULL    ELSE locked_at   END,
		    heartbeat_at = CASE WHEN attempts < max_attempts THEN NULL    ELSE heartbeat_at END
		  WHERE status = 'running'
		    AND COALESCE(heartbeat_at, locked_at, started_at) < now() - make_interval(secs => $1)`,
		secs)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// UpsertSchedule registers/updates a periodic schedule. On first insert the
// next run is one interval out, so a restart doesn't fire the job immediately.
func (s *Store) UpsertSchedule(ctx context.Context, kind string, interval time.Duration) error {
	secs := int(interval.Seconds())
	if secs < 1 {
		secs = 1
	}
	// next_run_at is computed in Go and passed as its own param so $2 isn't
	// reused in two type contexts (int column + make_interval's double).
	next := time.Now().Add(time.Duration(secs) * time.Second)
	_, err := s.pool.Exec(ctx,
		`INSERT INTO job_schedules (kind, interval_seconds, next_run_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (kind) DO UPDATE SET interval_seconds = EXCLUDED.interval_seconds`,
		kind, secs, next)
	return err
}

// ClaimDueSchedules atomically advances every due schedule and returns the kinds
// that just came due. The UPDATE ... RETURNING is the leader-singleton: even if
// every replica runs the scheduler, each due row is claimed by exactly one.
func (s *Store) ClaimDueSchedules(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE job_schedules
		    SET last_run_at = now(),
		        next_run_at = now() + make_interval(secs => interval_seconds)
		  WHERE next_run_at <= now()
		  RETURNING kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		kinds = append(kinds, k)
	}
	return kinds, rows.Err()
}

// List returns recent jobs, newest first, optionally filtered by status/kind
// (empty = any). limit is clamped to [1, 500]. Used by the admin Jobs UI (S2).
func (s *Store) List(ctx context.Context, status, kind string, limit int) ([]Job, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+jobCols+` FROM jobs
		  WHERE ($1 = '' OR status = $1)
		    AND ($2 = '' OR kind = $2)
		  ORDER BY created_at DESC
		  LIMIT $3`,
		status, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// KindStats is a per-kind aggregate of the jobs table for the admin queue
// overview (boom-hney): current queue depth (queued/running are point-in-time,
// all-time), recent throughput + failures (done/failed over a `since` window,
// keyed on finished_at), the mean successful+failed run duration over that
// window, and the kind's most-recent activity timestamp + status.
//
// maxConcurrency is deliberately NOT here — it's registry policy (SetConcurrency),
// merged in by the admin handler so this stays a pure jobs-table read.
type KindStats struct {
	Kind          string
	Queued        int
	Running       int
	DoneRecent    int
	FailedRecent  int
	AvgDurationMs float64 // 0 when nothing of this kind finished within `since`
	LastRunAt     *time.Time
	LastStatus    Status
}

// ListJobKindStats returns one KindStats per DISTINCT kind present in the jobs
// table, computed in a single GROUP BY scan (not N queries). `since` bounds the
// throughput window: the done/failed counts and the avg-duration only consider
// rows whose finished_at >= since, while queued/running reflect the live depth.
//
// last_run_at is the kind's most recent activity (finished, else started, else
// created) and last_status is that row's status — resolved with array_agg ...
// ORDER BY so it costs no extra query. Kinds with zero rows never appear here;
// the admin layer unions in the registered kinds so a known-but-idle kind still
// shows a card.
func (s *Store) ListJobKindStats(ctx context.Context, since time.Time) ([]KindStats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT kind,
		        count(*) FILTER (WHERE status = 'queued')                       AS queued,
		        count(*) FILTER (WHERE status = 'running')                      AS running,
		        count(*) FILTER (WHERE status = 'done'   AND finished_at >= $1) AS done_recent,
		        count(*) FILTER (WHERE status = 'failed' AND finished_at >= $1) AS failed_recent,
		        coalesce(avg(EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)
		            FILTER (WHERE status IN ('done','failed')
		                    AND finished_at >= $1 AND started_at IS NOT NULL), 0) AS avg_ms,
		        max(coalesce(finished_at, started_at, created_at))              AS last_run_at,
		        (array_agg(status ORDER BY coalesce(finished_at, started_at, created_at) DESC))[1] AS last_status
		   FROM jobs
		  GROUP BY kind
		  ORDER BY kind`,
		since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindStats
	for rows.Next() {
		var ks KindStats
		var lastStatus string
		if err := rows.Scan(&ks.Kind, &ks.Queued, &ks.Running,
			&ks.DoneRecent, &ks.FailedRecent, &ks.AvgDurationMs,
			&ks.LastRunAt, &lastStatus); err != nil {
			return nil, err
		}
		ks.LastStatus = Status(lastStatus)
		out = append(out, ks)
	}
	return out, rows.Err()
}

// Get loads one job by id (admin detail view).
func (s *Store) Get(ctx context.Context, id int64) (*Job, bool, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = $1`, id)
	j, err := scanJob(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return j, true, nil
}

// ListSchedules returns every registered periodic schedule (admin view).
func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT kind, interval_seconds, next_run_at, last_run_at
		   FROM job_schedules ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		var secs int
		if err := rows.Scan(&sc.Kind, &secs, &sc.NextRun, &sc.LastRun); err != nil {
			return nil, err
		}
		sc.Interval = time.Duration(secs) * time.Second
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ListLatestPerOwner returns the most recent job per distinct owner for a kind
// (boom-hney Stage 3): the label-images admin tab reads the latest label-image
// job per label (owner == labelID) from the DB queue instead of the old
// in-memory imagejobs registry. Both terminal and in-flight jobs are included;
// owner=="" rows (kinds that don't scope by owner) are excluded.
func (s *Store) ListLatestPerOwner(ctx context.Context, kind string) ([]Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (owner) `+jobCols+`
		    FROM jobs
		   WHERE kind = $1 AND owner <> ''
		   ORDER BY owner, created_at DESC`,
		kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// HasPending reports whether a queued or running job already exists for
// (kind, owner). Used to dedupe label-image regens (owner == labelID),
// mirroring the imagejobs registry's per-label idempotency so a double-click
// or overlapping "regen all" doesn't double-fire ComfyUI for one label.
func (s *Store) HasPending(ctx context.Context, kind, owner string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs
		  WHERE kind = $1 AND owner = $2 AND status IN ('queued','running')`,
		kind, owner).Scan(&n)
	return n > 0, err
}

// HasPendingKind reports whether ANY queued or running job of a kind already
// exists (owner-agnostic). The scheduler consults it before a periodic enqueue so
// a transient hang can't stack the queue — e.g. books-reading-monitor fires every
// minute; without this a stuck run would pile up 100s of duplicate rows behind it.
func (s *Store) HasPendingKind(ctx context.Context, kind string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM jobs WHERE kind = $1 AND status IN ('queued','running') LIMIT 1`,
		kind).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// scanRow is the shared shape of pgx.Row / a rows cursor position.
type scanRow interface {
	Scan(dest ...any) error
}

func scanJob(row scanRow) (*Job, error) {
	var j Job
	if err := row.Scan(
		&j.ID, &j.Kind, &j.Owner, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
		&j.Error, &j.RunAt, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	); err != nil {
		return nil, err
	}
	return &j, nil
}
