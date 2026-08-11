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
// Kind-routing (gaka-hney): `include` restricts to those kinds (empty = any);
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
		        started_at = now(), locked_by = $1, locked_at = now()
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
		        started_at = now(), locked_by = $2, locked_at = now()
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
// (gaka-hney Stage 3): the label-images admin tab reads the latest label-image
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
