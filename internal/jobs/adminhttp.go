// adminhttp.go — the PORTABLE admin HTTP surface for the catalyst-go-jobs
// subsystem (gaka-hney). This is the operator API behind the "Jobs" admin tab:
// list job history, view the per-kind queue overview + periodic schedules,
// trigger / retry / cancel a job, and read / clear a job's persisted logs.
//
// It lives in the jobs package (not the host's admin package) so the whole
// subsystem — worker, store, and this admin surface — ports to another project
// as a unit. The host mounts it through ONE seam, RegisterAdminRoutes(g, Deps):
// it injects the store, the enqueue provider, the registry, the object store,
// an admin GUARD (the host's own auth/cap policy — the plugin never imports it),
// and a logger. Everything is late-bound: the job subsystem is typically wired
// AFTER routes are registered, so Deps hands back live accessors rather than
// captured values, and the handlers answer 503 until the subsystem is up.
//
// Host coupling to port alongside this file: internal/apierr + internal/apihelpers
// (generic HTTP error/render/bind helpers) and internal/objstore (the blob store
// interface). None reference the host's auth — that arrives only through Deps.Guard.
package jobs

import (
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/objstore"
)

// AdminGuard authorizes an admin request. It returns the acting admin's identity
// (used only for audit logging) and, when the caller is NOT authorized, a
// non-nil error the handler renders as the HTTP response. The host injects its
// own policy here — this is the ONLY place the plugin learns "who may touch
// jobs", so the jobs package never imports the host's auth/cap code. The seam
// type is a plain error so a porting host isn't forced to adopt this project's
// apierr; a host that DOES return an *apierr.Error still gets its exact rendering
// (see guardErr).
type AdminGuard func(c *echo.Context) (owner string, err error)

// Deps is everything the portable jobs-admin surface needs from its host. The
// subsystem accessors are FUNCTIONS (late-bound) because the host commonly wires
// the store/provider AFTER registering routes; the handlers call them per request
// and answer 503 while they still return nil. Guard + Logger are stable values.
//
// To port the plugin: implement this struct against your host's wiring and call
// RegisterAdminRoutes on a group carrying whatever route-level auth you want.
type Deps struct {
	// Store is the jobs table of record (list/get/cancel/schedules/queue stats).
	Store func() *Store
	// Enqueuer is the wired provider used to (re)enqueue jobs on trigger/retry;
	// if it also implements Canceller, cancel reaches an in-process handler.
	Enqueuer func() Enqueuer
	// Registry is the worker registry — the source of per-kind concurrency caps
	// and the full universe of registered kinds for the queue overview.
	Registry func() *Registry
	// ObjStore is the durable log-blob store the log GET/DELETE endpoints read +
	// clear. nil = persistence off (endpoints degrade to empty / no-op).
	ObjStore func() objstore.Store
	// Guard is the host's admin authorization check (required).
	Guard AdminGuard
	// Logger records audit lines for mutating actions (required, non-nil).
	Logger *slog.Logger
}

// adminAPI binds the injected Deps to the handler methods.
type adminAPI struct{ d Deps }

// RegisterAdminRoutes mounts the jobs-admin endpoints onto g (whose prefix the
// host chooses — boomtime uses /api/v1/admin/jobs — and onto which the host has
// already applied any route-level auth middleware). The sub-paths below are the
// plugin's stable contract:
//
//	GET    ""            list job history (?status=&kind=&limit=)
//	GET    /queues       per-kind queue overview
//	GET    /schedules    recurring schedules
//	POST   /trigger      enqueue a kind now
//	POST   /:id/retry    re-enqueue a fresh attempt
//	POST   /:id/cancel   cooperatively cancel a queued/running job
//	GET    /:id/logs     a finished job's persisted logs
//	DELETE /:id/logs     wipe ONE job's stored logs
//	DELETE /logs         bulk-wipe stored logs (?kind= or all)
func RegisterAdminRoutes(g *echo.Group, d Deps) {
	a := &adminAPI{d: d}
	g.GET("", a.list)
	g.GET("/queues", a.queues)
	g.GET("/schedules", a.schedules)
	g.POST("/trigger", a.trigger)
	g.POST("/:id/retry", a.retry)
	g.POST("/:id/cancel", a.cancel)
	g.GET("/:id/logs", a.logs)
	g.DELETE("/:id/logs", a.logsDelete)
	g.DELETE("/logs", a.logsClear)
}

// ── DTOs ────────────────────────────────────────────────────────────────────

type jobDTO struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	Attempts    int     `json:"attempts"`
	MaxAttempts int     `json:"maxAttempts"`
	Error       string  `json:"error"`
	RunAt       string  `json:"runAt"`
	CreatedAt   string  `json:"createdAt"`
	StartedAt   *string `json:"startedAt"`
	FinishedAt  *string `json:"finishedAt"`
}

// queueKindDTO is one per-kind row of the queue overview. queued/running are the
// live depth; doneLastHour/failedLastHour/avgDurationMs are the trailing-hour
// throughput window; maxConcurrency (0 = unlimited) comes from the registry so
// the FE can render running/max headroom and flag at-cap back-pressure.
type queueKindDTO struct {
	Kind           string  `json:"kind"`
	Queued         int     `json:"queued"`
	Running        int     `json:"running"`
	MaxConcurrency int     `json:"maxConcurrency"`
	DoneLastHour   int     `json:"doneLastHour"`
	FailedLastHour int     `json:"failedLastHour"`
	AvgDurationMs  float64 `json:"avgDurationMs"`
	LastRunAt      *string `json:"lastRunAt"`
	LastStatus     string  `json:"lastStatus"`
}

type scheduleDTO struct {
	Kind            string  `json:"kind"`
	IntervalSeconds int     `json:"intervalSeconds"`
	NextRun         string  `json:"nextRun"`
	LastRun         *string `json:"lastRun"`
}

// jobLogEntryDTO mirrors logging.LogEntry for the stored-logs response. It's the
// SAME shape the live LogHub stream (/api/v1/logs) serializes, so the FE renders
// stored + live lines through one mapping.
type jobLogEntryDTO = logging.LogEntry

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func rfcPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toJobDTO(j Job) jobDTO {
	return jobDTO{
		ID: j.ID, Kind: j.Kind, Status: string(j.Status),
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Error: j.Error,
		RunAt: rfc(j.RunAt), CreatedAt: rfc(j.CreatedAt),
		StartedAt: rfcPtr(j.StartedAt), FinishedAt: rfcPtr(j.FinishedAt),
	}
}

func jobsUnavailable() *apierr.Error {
	return apierr.New(http.StatusServiceUnavailable, "jobs subsystem not enabled", nil)
}

// guardErr renders a guard rejection. When the host's guard returned this
// project's *apierr.Error we preserve its exact JSON shape; any other error from
// a porting host degrades to a plain 403 via echo's error handler.
func (a *adminAPI) guardErr(c *echo.Context, err error) error {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return apihelpers.RespondErr(c, ae)
	}
	return echo.NewHTTPError(http.StatusForbidden, err.Error())
}

// ── handlers ────────────────────────────────────────────────────────────────

// list: GET "" (?status=&kind=&limit=)
func (a *adminAPI) list(c *echo.Context) error {
	if _, gerr := a.d.Guard(c); gerr != nil {
		return a.guardErr(c, gerr)
	}
	store := a.d.Store()
	if store == nil {
		return apihelpers.RespondErr(c, jobsUnavailable())
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	rows, err := store.List(c.Request().Context(), c.QueryParam("status"), c.QueryParam("kind"), limit)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs list failed", err)
	}
	out := make([]jobDTO, 0, len(rows))
	for _, j := range rows {
		out = append(out, toJobDTO(j))
	}
	return c.JSON(http.StatusOK, map[string]any{"jobs": out})
}

// queues: GET /queues — the per-kind queue overview (gaka-hney). One GROUP BY
// scan of the jobs table (ListJobKindStats over the last hour) merged with the
// registry's per-kind concurrency caps + the full set of registered kinds, so an
// operator SEES the limiter working: queue depth, running/max headroom,
// trailing-hour throughput + fail ratio, and last activity. Kinds with no rows
// yet still appear with zeroes. Sorted most-active first.
func (a *adminAPI) queues(c *echo.Context) error {
	if _, gerr := a.d.Guard(c); gerr != nil {
		return a.guardErr(c, gerr)
	}
	store := a.d.Store()
	if store == nil {
		return apihelpers.RespondErr(c, jobsUnavailable())
	}
	since := time.Now().Add(-time.Hour)
	stats, err := store.ListJobKindStats(c.Request().Context(), since)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs queue stats failed", err)
	}

	// Per-kind concurrency caps + the registered-kind universe (both nil-safe when
	// the registry isn't wired).
	var caps map[string]int
	var known []string
	if reg := a.d.Registry(); reg != nil {
		caps = reg.Concurrency()
		known = reg.Kinds()
	}

	// Index the DB aggregates by kind, then union in every registered kind so a
	// known-but-idle kind still shows a card at zero depth.
	byKind := make(map[string]queueKindDTO, len(stats)+len(known))
	for _, ks := range stats {
		byKind[ks.Kind] = queueKindDTO{
			Kind:           ks.Kind,
			Queued:         ks.Queued,
			Running:        ks.Running,
			MaxConcurrency: caps[ks.Kind],
			DoneLastHour:   ks.DoneRecent,
			FailedLastHour: ks.FailedRecent,
			AvgDurationMs:  ks.AvgDurationMs,
			LastRunAt:      rfcPtr(ks.LastRunAt),
			LastStatus:     string(ks.LastStatus),
		}
	}
	for _, k := range known {
		if _, seen := byKind[k]; !seen {
			byKind[k] = queueKindDTO{Kind: k, MaxConcurrency: caps[k]}
		}
	}

	out := make([]queueKindDTO, 0, len(byKind))
	for _, q := range byKind {
		out = append(out, q)
	}
	// Most-active first: running desc, then queued desc, then trailing-hour
	// throughput desc, then kind for a stable order.
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Running != y.Running {
			return x.Running > y.Running
		}
		if x.Queued != y.Queued {
			return x.Queued > y.Queued
		}
		tx := x.DoneLastHour + x.FailedLastHour
		ty := y.DoneLastHour + y.FailedLastHour
		if tx != ty {
			return tx > ty
		}
		return x.Kind < y.Kind
	})
	return c.JSON(http.StatusOK, map[string]any{"queues": out})
}

// schedules: GET /schedules
func (a *adminAPI) schedules(c *echo.Context) error {
	if _, gerr := a.d.Guard(c); gerr != nil {
		return a.guardErr(c, gerr)
	}
	store := a.d.Store()
	if store == nil {
		return apihelpers.RespondErr(c, jobsUnavailable())
	}
	scheds, err := store.ListSchedules(c.Request().Context())
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs schedules failed", err)
	}
	out := make([]scheduleDTO, 0, len(scheds))
	for _, s := range scheds {
		out = append(out, scheduleDTO{
			Kind:            s.Kind,
			IntervalSeconds: int(s.Interval.Seconds()),
			NextRun:         rfc(s.NextRun),
			LastRun:         rfcPtr(s.LastRun),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"schedules": out})
}

// trigger: POST /trigger {kind} — enqueue a job now.
func (a *adminAPI) trigger(c *echo.Context) error {
	owner, gerr := a.d.Guard(c)
	if gerr != nil {
		return a.guardErr(c, gerr)
	}
	enq := a.d.Enqueuer()
	if a.d.Store() == nil || enq == nil {
		return apihelpers.RespondErr(c, jobsUnavailable())
	}
	var req struct {
		Kind string `json:"kind"`
	}
	if berr := apihelpers.BindJSONWithLimit(c, &req, 4<<10); berr != nil {
		return apihelpers.RespondErr(c, berr)
	}
	if req.Kind == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("kind is required"))
	}
	id, err := enq.Enqueue(c.Request().Context(), req.Kind, nil)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs trigger failed", err)
	}
	a.d.Logger.Info("admin jobs trigger", "actor", owner, "kind", req.Kind, "id", id)
	return c.JSON(http.StatusOK, map[string]any{"id": id})
}

// retry: POST /:id/retry — re-enqueue a FRESH attempt of the job's kind+payload
// (the original row is left as-is for history).
func (a *adminAPI) retry(c *echo.Context) error {
	owner, gerr := a.d.Guard(c)
	if gerr != nil {
		return a.guardErr(c, gerr)
	}
	store := a.d.Store()
	enq := a.d.Enqueuer()
	if store == nil || enq == nil {
		return apihelpers.RespondErr(c, jobsUnavailable())
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	job, ok, err := store.Get(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs get failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("job not found"))
	}
	// Preserve the original job's owner + attempt budget on retry. Without the
	// owner, owner-scoped kinds re-run with an empty owner and fail "missing
	// owner"; without MaxAttempts a single-attempt job would silently gain the
	// default retry budget.
	opts := []EnqueueOption{Owner(job.Owner)}
	if job.MaxAttempts > 0 {
		opts = append(opts, MaxAttempts(job.MaxAttempts))
	}
	newID, err := enq.Enqueue(c.Request().Context(), job.Kind, job.Payload, opts...)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs retry failed", err)
	}
	a.d.Logger.Info("admin jobs retry", "actor", owner, "kind", job.Kind, "from", id, "id", newID)
	return c.JSON(http.StatusOK, map[string]any{"id": newID})
}

// cancel: POST /:id/cancel — cooperatively cancel a queued or running job.
// Store.MarkCancelled flips the durable status, then a best-effort
// provider.Cancel(id) cancels the in-process context so a handler that honors
// ctx stops promptly. Returns {cancelled, wasRunning}.
func (a *adminAPI) cancel(c *echo.Context) error {
	owner, gerr := a.d.Guard(c)
	if gerr != nil {
		return a.guardErr(c, gerr)
	}
	store := a.d.Store()
	enq := a.d.Enqueuer()
	if store == nil || enq == nil {
		return apihelpers.RespondErr(c, jobsUnavailable())
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	job, ok, err := store.Get(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs get failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("job not found"))
	}
	cancelled, err := store.MarkCancelled(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "jobs cancel failed", err)
	}
	// Best-effort in-process ctx cancel (interrupts a running handler that honors
	// ctx). Reaches the provider via the Canceller capability.
	wasRunning := job.Status == StatusRunning
	if cancelled {
		if canceller, okc := enq.(Canceller); okc && canceller.Cancel(id) {
			wasRunning = true
		}
	}
	a.d.Logger.Info("admin jobs cancel", "actor", owner, "id", id, "kind", job.Kind,
		"cancelled", cancelled, "wasRunning", wasRunning)
	return c.JSON(http.StatusOK, map[string]any{"cancelled": cancelled, "wasRunning": wasRunning})
}

// logs: GET /:id/logs — the persisted log stream for a FINISHED job. A
// running/queued job streams live from the LogHub (the FE keeps that path); this
// serves the durable copy flushed to object storage on completion. 404 when
// nothing is stored. Reads object storage ONLY; never the jobs table.
func (a *adminAPI) logs(c *echo.Context) error {
	if _, gerr := a.d.Guard(c); gerr != nil {
		return a.guardErr(c, gerr)
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	store := a.d.ObjStore()
	if store == nil {
		// Persistence is off (no S3). Treat as "no stored logs" so the FE shows
		// its empty state rather than an error.
		return apihelpers.RespondErr(c, apierr.NotFound("no stored logs for this job"))
	}
	rc, err := store.Get(c.Request().Context(), JobLogKey(id))
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return apihelpers.RespondErr(c, apierr.NotFound("no stored logs for this job"))
		}
		return apihelpers.InternalErr(a.d.Logger, c, "job logs read failed", err)
	}
	defer rc.Close()
	entries, err := ReadJobLogs(rc)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "job logs decode failed", err)
	}
	// Never emit null for an empty stored blob — the FE expects an array.
	if entries == nil {
		entries = []jobLogEntryDTO{}
	}
	return c.JSON(http.StatusOK, map[string]any{"entries": entries})
}

// logsDelete: DELETE /:id/logs — wipe ONLY the stored log object for a job. The
// jobs table row is deliberately left untouched: this clears the log panel's
// stored view, not the job's history. A missing object is not an error
// (idempotent). No-op success when persistence is off.
func (a *adminAPI) logsDelete(c *echo.Context) error {
	owner, gerr := a.d.Guard(c)
	if gerr != nil {
		return a.guardErr(c, gerr)
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	store := a.d.ObjStore()
	if store == nil {
		return c.JSON(http.StatusOK, map[string]any{"deleted": false})
	}
	if err := store.Delete(c.Request().Context(), JobLogKey(id)); err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "job logs delete failed", err)
	}
	a.d.Logger.Info("admin jobs logs delete", "actor", owner, "id", id)
	return c.JSON(http.StatusOK, map[string]any{"deleted": true})
}

// logsClear: DELETE /logs?kind=<kind> — bulk-wipe the STORED log objects for
// many jobs at once. Like the per-job DELETE it touches object storage ONLY: the
// jobs table rows are never mutated. It lists every object under JobLogPrefix and
// deletes them; ?kind= restricts the wipe to that kind's jobs — the kind isn't in
// the log key, so we READ the jobs table for that kind's ids (a SELECT — never a
// write) and keep only matching keys. Returns {deleted: N} — the count of objects
// actually removed. No-op {deleted:0} when persistence is off, and — for a kind
// filter — when the jobs subsystem isn't wired.
func (a *adminAPI) logsClear(c *echo.Context) error {
	owner, gerr := a.d.Guard(c)
	if gerr != nil {
		return a.guardErr(c, gerr)
	}
	store := a.d.ObjStore()
	if store == nil {
		return c.JSON(http.StatusOK, map[string]any{"deleted": 0})
	}
	ctx := c.Request().Context()
	kind := c.QueryParam("kind")

	keys, err := store.List(ctx, JobLogPrefix)
	if err != nil {
		return apihelpers.InternalErr(a.d.Logger, c, "job logs list failed", err)
	}

	// A kind filter needs the id→kind mapping, which lives in the jobs table (the
	// log key carries only the id). Read the kind's ids (500 most-recent cap) and
	// keep only their log keys. No jobs-table write happens here.
	if kind != "" {
		js := a.d.Store()
		if js == nil {
			return c.JSON(http.StatusOK, map[string]any{"deleted": 0})
		}
		rows, lerr := js.List(ctx, "", kind, 500)
		if lerr != nil {
			return apihelpers.InternalErr(a.d.Logger, c, "job logs list kind failed", lerr)
		}
		allow := make(map[string]struct{}, len(rows))
		for _, j := range rows {
			allow[JobLogKey(j.ID)] = struct{}{}
		}
		filtered := keys[:0:0]
		for _, k := range keys {
			if _, ok := allow[k]; ok {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	deleted := 0
	for _, k := range keys {
		if derr := store.Delete(ctx, k); derr != nil {
			return apihelpers.InternalErr(a.d.Logger, c, "job logs bulk delete failed", derr)
		}
		deleted++
	}
	a.d.Logger.Info("admin jobs logs clear", "actor", owner, "kind", kind, "deleted", deleted)
	return c.JSON(http.StatusOK, map[string]any{"deleted": deleted})
}
