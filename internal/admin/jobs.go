// jobs.go — admin Jobs API (gaka-hney.2): list history, view the periodic
// schedules, and trigger/retry a job. Reads the catalyst-go-jobs `jobs` table
// (the record-of-truth for BOTH providers) and enqueues via the wired provider,
// so the tab is provider-agnostic. All routes are requireAdmin-gated; when the
// jobs subsystem isn't wired the handlers return 503.
package admin

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/objstore"
)

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

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func rfcPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toJobDTO(j jobs.Job) jobDTO {
	return jobDTO{
		ID: j.ID, Kind: j.Kind, Status: string(j.Status),
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Error: j.Error,
		RunAt: rfc(j.RunAt), CreatedAt: rfc(j.CreatedAt),
		StartedAt: rfcPtr(j.StartedAt), FinishedAt: rfcPtr(j.FinishedAt),
	}
}

func (h *Handler) jobsUnavailable() *apierr.Error {
	return apierr.New(http.StatusServiceUnavailable, "jobs subsystem not enabled", nil)
}

// AdminJobsList: GET /api/v1/admin/jobs?status=&kind=&limit=
func (h *Handler) AdminJobsList(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobStore == nil {
		return apihelpers.RespondErr(c, h.jobsUnavailable())
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	list, err := h.JobStore.List(c.Request().Context(), c.QueryParam("status"), c.QueryParam("kind"), limit)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs list failed", err)
	}
	out := make([]jobDTO, 0, len(list))
	for _, j := range list {
		out = append(out, toJobDTO(j))
	}
	return c.JSON(http.StatusOK, map[string]any{"jobs": out})
}

// AdminJobQueues: GET /api/v1/admin/jobs/queues — the per-kind queue overview
// (gaka-hney). One GROUP BY scan of the jobs table (ListJobKindStats over the
// last hour) merged with the registry's per-kind concurrency caps + the full set
// of registered kinds, so an operator SEES the limiter working: queue depth,
// running/max headroom, trailing-hour throughput + fail ratio, and last activity.
// Kinds with no rows yet (freshly registered / only scheduled) still appear with
// zeroes. Sorted most-active first (running, then queued, then throughput).
func (h *Handler) AdminJobQueues(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobStore == nil {
		return apihelpers.RespondErr(c, h.jobsUnavailable())
	}
	since := time.Now().Add(-time.Hour)
	stats, err := h.JobStore.ListJobKindStats(c.Request().Context(), since)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs queue stats failed", err)
	}

	// Per-kind concurrency caps + the registered-kind universe (both nil-safe when
	// the registry isn't wired).
	var caps map[string]int
	var known []string
	if h.JobRegistry != nil {
		caps = h.JobRegistry.Concurrency()
		known = h.JobRegistry.Kinds()
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
		a, b := out[i], out[j]
		if a.Running != b.Running {
			return a.Running > b.Running
		}
		if a.Queued != b.Queued {
			return a.Queued > b.Queued
		}
		ta := a.DoneLastHour + a.FailedLastHour
		tb := b.DoneLastHour + b.FailedLastHour
		if ta != tb {
			return ta > tb
		}
		return a.Kind < b.Kind
	})
	return c.JSON(http.StatusOK, map[string]any{"queues": out})
}

// AdminJobSchedules: GET /api/v1/admin/jobs/schedules
func (h *Handler) AdminJobSchedules(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobStore == nil {
		return apihelpers.RespondErr(c, h.jobsUnavailable())
	}
	scheds, err := h.JobStore.ListSchedules(c.Request().Context())
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs schedules failed", err)
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

// AdminJobTrigger: POST /api/v1/admin/jobs/trigger {kind} — enqueue a job now.
func (h *Handler) AdminJobTrigger(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobStore == nil || h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, h.jobsUnavailable())
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
	id, err := h.JobEnqueuer.Enqueue(c.Request().Context(), req.Kind, nil)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs trigger failed", err)
	}
	h.Logger.Info("admin jobs trigger", "actor", owner, "kind", req.Kind, "id", id)
	return c.JSON(http.StatusOK, map[string]any{"id": id})
}

// AdminJobRetry: POST /api/v1/admin/jobs/:id/retry — re-enqueue a FRESH attempt
// of the job's kind+payload (the original row is left as-is for history).
func (h *Handler) AdminJobRetry(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobStore == nil || h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, h.jobsUnavailable())
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	job, ok, err := h.JobStore.Get(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs get failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("job not found"))
	}
	// Preserve the original job's owner + attempt budget on retry. Without the
	// owner, owner-scoped kinds (e.g. audiobooks-audible-backfill) re-run with an
	// empty owner and fail "missing owner"; without MaxAttempts a single-attempt
	// job would silently gain the default retry budget.
	opts := []jobs.EnqueueOption{jobs.Owner(job.Owner)}
	if job.MaxAttempts > 0 {
		opts = append(opts, jobs.MaxAttempts(job.MaxAttempts))
	}
	newID, err := h.JobEnqueuer.Enqueue(c.Request().Context(), job.Kind, job.Payload, opts...)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs retry failed", err)
	}
	h.Logger.Info("admin jobs retry", "actor", owner, "kind", job.Kind, "from", id, "id", newID)
	return c.JSON(http.StatusOK, map[string]any{"id": newID})
}

// AdminJobCancel: POST /api/v1/admin/jobs/:id/cancel — cooperatively cancel a
// queued or running job. Store.MarkCancelled flips the durable status (a queued
// job is then never claimed; a running job's row is stamped terminal), then a
// best-effort provider.Cancel(id) cancels the in-process context so a handler that
// honors ctx stops promptly. Returns {cancelled, wasRunning}.
//
// wasRunning reports whether the job was actively executing (DB status 'running',
// or the provider found + signalled it locally). In-process cancellation only
// reaches a handler running on THIS pod; the MarkCancelled write is the durable
// cross-pod signal, and the Dragonfly pub-sub fan-out (see LocalProvider.Cancel)
// is the multi-pod upgrade for interrupting a run on another pod.
func (h *Handler) AdminJobCancel(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobStore == nil || h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, h.jobsUnavailable())
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	job, ok, err := h.JobStore.Get(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs get failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("job not found"))
	}
	cancelled, err := h.JobStore.MarkCancelled(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "jobs cancel failed", err)
	}
	// Best-effort in-process ctx cancel (interrupts a running handler that honors
	// ctx). Reaches the provider via the Canceller capability — the wired provider
	// is passed as the Enqueuer, mirroring how AdminJobRetry reaches it.
	wasRunning := job.Status == jobs.StatusRunning
	if cancelled {
		if canceller, okc := h.JobEnqueuer.(jobs.Canceller); okc && canceller.Cancel(id) {
			wasRunning = true
		}
	}
	h.Logger.Info("admin jobs cancel", "actor", owner, "id", id, "kind", job.Kind,
		"cancelled", cancelled, "wasRunning", wasRunning)
	return c.JSON(http.StatusOK, map[string]any{"cancelled": cancelled, "wasRunning": wasRunning})
}

// jobLogEntryDTO mirrors logging.LogEntry for the stored-logs response. It's the
// SAME shape the live LogHub stream (/api/v1/logs) serializes, so the FE renders
// stored + live lines through one mapping (web/src/features/logs/logLine.ts).
type jobLogEntryDTO = logging.LogEntry

// AdminJobLogs: GET /api/v1/admin/jobs/:id/logs — the persisted log stream for a
// FINISHED job (gaka-hney). A running/queued job streams live from the LogHub
// (the FE keeps that path); this endpoint serves the durable copy flushed to
// object storage on completion, so logs survive the in-memory ring rolling over.
// 404 when nothing is stored (job never captured, logs deleted, or S3 off) — the
// FE falls back to its empty state. Reads object storage ONLY; never the jobs
// table.
func (h *Handler) AdminJobLogs(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	if h.JobLogStore == nil {
		// Persistence is off (no S3). Treat as "no stored logs" so the FE shows
		// its empty state rather than an error.
		return apihelpers.RespondErr(c, apierr.NotFound("no stored logs for this job"))
	}
	rc, err := h.JobLogStore.Get(c.Request().Context(), jobs.JobLogKey(id))
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return apihelpers.RespondErr(c, apierr.NotFound("no stored logs for this job"))
		}
		return apihelpers.InternalErr(h.Logger, c, "job logs read failed", err)
	}
	defer rc.Close()
	entries, err := jobs.ReadJobLogs(rc)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "job logs decode failed", err)
	}
	// Never emit null for an empty stored blob — the FE expects an array.
	if entries == nil {
		entries = []jobLogEntryDTO{}
	}
	return c.JSON(http.StatusOK, map[string]any{"entries": entries})
}

// AdminJobLogsDelete: DELETE /api/v1/admin/jobs/:id/logs — wipe ONLY the stored
// log object for a job (gaka-hney). The jobs table row is deliberately left
// untouched: this clears the log panel's stored view, not the job's history. A
// missing object is not an error (idempotent). No-op success when S3 is off.
func (h *Handler) AdminJobLogsDelete(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, perr := strconv.ParseInt(c.Param("id"), 10, 64)
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("bad job id"))
	}
	if h.JobLogStore == nil {
		// Nothing persisted anywhere — report a clean no-op rather than erroring.
		return c.JSON(http.StatusOK, map[string]any{"deleted": false})
	}
	if err := h.JobLogStore.Delete(c.Request().Context(), jobs.JobLogKey(id)); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "job logs delete failed", err)
	}
	h.Logger.Info("admin jobs logs delete", "actor", owner, "id", id)
	return c.JSON(http.StatusOK, map[string]any{"deleted": true})
}
