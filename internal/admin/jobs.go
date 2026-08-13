// jobs.go — admin Jobs API (gaka-hney.2): list history, view the periodic
// schedules, and trigger/retry a job. Reads the catalyst-go-jobs `jobs` table
// (the record-of-truth for BOTH providers) and enqueues via the wired provider,
// so the tab is provider-agnostic. All routes are requireAdmin-gated; when the
// jobs subsystem isn't wired the handlers return 503.
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
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
