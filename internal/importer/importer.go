// Package importer runs durable, resumable jobs that migrate a user's history
// from wakatime.com into the local heartbeats table. Jobs are processed
// day-by-day with per-day durability, resilient error handling (one bad day does
// not fail the whole job), cancellation, and live event streaming via a Hub.
package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/wakatime"
)

// JobStatus JSON strings returned to the client for back-compat (Import.hs).
const (
	JobSubmitted = "JobSubmitted"
	JobPending   = "JobPending"
	JobFailed    = "JobFailed"
	JobFinished  = "JobFinished"
)

// MapState maps an import_jobs.state to a legacy client-facing JobStatus.
func MapState(state string) string {
	switch state {
	case db.JobStateFailed, db.JobStateCancelled:
		return JobFailed
	case db.JobStateCompleted:
		return JobFinished
	default: // queued, running
		return JobPending
	}
}

// QueueItem is the JSON stored in import_jobs.value (the request + requester).
type QueueItem struct {
	ReqPayload model.ImportRequestPayload `json:"reqPayload"`
	Requester  string                     `json:"requester"`
}

const wakatimeAPI = "https://wakatime.com"

// Worker owns the running-job registry and the event hub.
type Worker struct {
	DB     *db.DB
	Logger *slog.Logger
	Hub    *Hub

	// BaseURL overrides the wakatime.com base for tests (httptest.Server). If
	// empty the wakatimeAPI constant is used. Kept exported for the importer
	// integration tests without leaking into the public config.
	BaseURL string

	mu      sync.Mutex
	running map[int]context.CancelFunc // jobID -> cancel
	base    context.Context            // parent context (server lifetime)
}

// baseURL returns the effective wakatime.com base URL for this worker.
func (w *Worker) baseURL() string {
	if w.BaseURL != "" {
		return w.BaseURL
	}
	return wakatimeAPI
}

// NewWorker constructs a Worker bound to a base context (server lifetime).
func NewWorker(base context.Context, database *db.DB, logger *slog.Logger, hub *Hub) *Worker {
	return &Worker{
		DB:      database,
		Logger:  logger,
		Hub:     hub,
		running: make(map[int]context.CancelFunc),
		base:    base,
	}
}

// RecoverInterrupted marks any queued/running jobs (from a previous process) as
// failed. Called once at startup so a crash/restart never leaves a zombie job.
func (w *Worker) RecoverInterrupted(ctx context.Context) {
	ids, err := w.DB.MarkRunningJobsFailed(ctx, "interrupted by restart")
	if err != nil {
		w.Logger.Error("failed to recover interrupted import jobs", "err", err)
		return
	}
	for _, id := range ids {
		w.Logger.Warn("marked interrupted import job as failed", "id", id)
	}
}

// StartJob launches processing of an existing queued job in the background.
// The job's context is registered for cancellation.
func (w *Worker) StartJob(job *db.Job, item QueueItem) {
	jobCtx, cancel := context.WithCancel(w.base)

	w.mu.Lock()
	w.running[job.ID] = cancel
	w.mu.Unlock()

	go func() {
		defer func() {
			w.mu.Lock()
			delete(w.running, job.ID)
			w.mu.Unlock()
			cancel()
		}()
		w.run(jobCtx, job.ID, item)
	}()
}

// Cancel requests cancellation of a running job. Returns true if it was running
// in this process. Callers should also update the DB state for durability.
func (w *Worker) Cancel(jobID int) bool {
	w.mu.Lock()
	cancel, ok := w.running[jobID]
	w.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// run executes a job day-by-day with durable progress and live event publishing.
func (w *Worker) run(ctx context.Context, jobID int, item QueueItem) {
	log := func(level, msg string) {
		l, err := w.DB.InsertJobLog(ctx, jobID, level, msg)
		if err != nil {
			// Context cancellation aborts DB writes; still surface at debug.
			w.Logger.Debug("failed to persist job log", "job", jobID, "err", err)
			return
		}
		w.Hub.Publish(jobID, Event{Type: "log", Log: l})
	}
	publishJob := func(kind string, job *db.Job) {
		if job != nil {
			w.Hub.Publish(jobID, Event{Type: kind, Job: job})
		}
	}

	// gaka-unq.1: per-job schema-drift collector. Emits a "warn" log on first
	// occurrence of each finding (dedupe by endpoint+kind+field so repeated
	// drift across many days produces one finding with count). Persisted onto
	// the job row before every terminal transition so historical runs show the
	// warning banner in the FE.
	drift := newDriftCollector()
	flushDriftLogs := func() {
		for _, msg := range drift.drainNewLogs() {
			log("warn", msg)
		}
	}
	persistDrift := func(persistCtx context.Context) {
		findings := drift.findings()
		if findings == nil {
			return
		}
		buf, err := json.Marshal(findings)
		if err != nil {
			w.Logger.Warn("marshal drift findings", "job", jobID, "err", err)
			return
		}
		if err := w.DB.SetJobDrift(persistCtx, jobID, buf); err != nil {
			w.Logger.Warn("persist drift findings", "job", jobID, "err", err)
		}
	}

	// Transition to running.
	job, err := w.DB.MarkJobRunning(ctx, jobID)
	if err != nil {
		w.Logger.Error("failed to mark job running", "job", jobID, "err", err)
		return
	}
	publishJob("state", job)

	p := item.ReqPayload
	days := genDateRange(p.StartDate, p.EndDate)
	log("info", fmt.Sprintf("starting import for %d day(s): %s .. %s",
		len(days), p.StartDate.Format("2006-01-02"), p.EndDate.Format("2006-01-02")))

	// Resolve user_agents and machine_names once up front.
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.APIToken))
	uaByID, mnByID, err := w.fetchLookups(ctx, authHeader, drift)
	flushDriftLogs()
	if err != nil {
		if ctx.Err() != nil {
			// Persist drift on best-effort background ctx (job ctx is done).
			withBackgroundTimeout(5*time.Second, persistDrift)
			w.finishCancelled(jobID, publishJob)
			return
		}
		msg := err.Error()
		log("error", "failed to fetch wakatime metadata: "+msg)
		persistDrift(ctx)
		j, _ := w.DB.FinishImportJob(ctx, jobID, db.JobStateFailed, &msg)
		publishJob("state", j)
		return
	}

	var importedTotal int64
	for i, day := range days {
		if ctx.Err() != nil {
			withBackgroundTimeout(5*time.Second, persistDrift)
			w.finishCancelled(jobID, publishJob)
			return
		}

		n, dayErr := w.importDay(ctx, authHeader, item.Requester, day, mnByID, uaByID, drift)
		flushDriftLogs()
		if dayErr != nil {
			if ctx.Err() != nil {
				withBackgroundTimeout(5*time.Second, persistDrift)
				w.finishCancelled(jobID, publishJob)
				return
			}
			// Resilient: log and continue to the next day.
			log("error", fmt.Sprintf("failed to import %s: %s", day, dayErr.Error()))
		} else {
			importedTotal += n
			log("info", fmt.Sprintf("imported %d heartbeats for %s", n, day))
		}

		j, upErr := w.DB.UpdateJobProgress(ctx, jobID, i+1, importedTotal, day)
		if upErr != nil {
			if ctx.Err() != nil {
				withBackgroundTimeout(5*time.Second, persistDrift)
				w.finishCancelled(jobID, publishJob)
				return
			}
			w.Logger.Error("failed to persist progress", "job", jobID, "err", upErr)
			continue
		}
		publishJob("progress", j)
	}

	log("info", fmt.Sprintf("imported %d heartbeats across %d days", importedTotal, len(days)))
	// Persist drift BEFORE FinishImportJob so the returned terminal snapshot
	// (and the "state" WS event) carries drift[].
	persistDrift(ctx)
	final, err := w.DB.FinishImportJob(ctx, jobID, db.JobStateCompleted, nil)
	if err != nil {
		w.Logger.Error("failed to finalize job", "job", jobID, "err", err)
		return
	}
	publishJob("state", final)
	w.Logger.Info("import completed", "job", jobID, "user", item.Requester, "imported", importedTotal)
}

// withBackgroundTimeout runs fn with a short-lived background context used
// when the job's own context is already done (cancellation path) but we still
// need to write terminal state / drift to the DB. The cancel is deferred so
// nothing leaks.
func withBackgroundTimeout(d time.Duration, fn func(context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	fn(ctx)
}

// finishCancelled records a cancelled terminal state using a background context
// (the job context is already done). Idempotent: no-op if already terminal.
func (w *Worker) finishCancelled(jobID int, publishJob func(string, *db.Job)) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, _ := w.DB.InsertJobLog(ctx, jobID, "info", "cancelled by user")
	if l != nil {
		w.Hub.Publish(jobID, Event{Type: "log", Log: l})
	}
	j, err := w.DB.CancelJob(ctx, jobID)
	if err != nil {
		w.Logger.Error("failed to mark job cancelled", "job", jobID, "err", err)
		return
	}
	publishJob("state", j)
}

// fetchLookups resolves the user_agent and machine-name id maps.
//
// gaka-unq.1: raw body is decoded twice — once into the typed struct (existing
// behavior) and once against the schemaSpec for drift checks. The typed decode
// is deliberately independent of the drift check so a benign new field never
// interferes with importer functionality. If the drift check turns up any
// error-severity finding on these small lookup lists (missing id/value or
// broken envelope), we return an error so the job fails fast — heartbeat
// ingestion depends on these maps.
func (w *Worker) fetchLookups(ctx context.Context, authHeader string, drift *driftCollector) (uaByID, mnByID map[string]string, err error) {
	uaBody, err := getRawJSON(ctx, w.baseURL()+"/api/v1/users/current/user_agents", authHeader, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch user_agents: %w", err)
	}
	var uaList userAgentList
	if err := json.Unmarshal(uaBody, &uaList); err != nil {
		return nil, nil, fmt.Errorf("decode user_agents: %w", err)
	}
	if data, ok := drift.checkEnvelope("user_agents", uaBody, jtArray); ok {
		drift.checkList("user_agents", "", data, lookupSpec, -1)
	}
	if drift.hasError() {
		return nil, nil, fmt.Errorf("fetch user_agents: schema drift breaks required fields")
	}

	mnBody, err := getRawJSON(ctx, w.baseURL()+"/api/v1/users/current/machine_names", authHeader, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch machine_names: %w", err)
	}
	var mnList machineNameList
	if err := json.Unmarshal(mnBody, &mnList); err != nil {
		return nil, nil, fmt.Errorf("decode machine_names: %w", err)
	}
	if data, ok := drift.checkEnvelope("machine_names", mnBody, jtArray); ok {
		drift.checkList("machine_names", "", data, lookupSpec, -1)
	}
	if drift.hasError() {
		return nil, nil, fmt.Errorf("fetch machine_names: schema drift breaks required fields")
	}

	uaByID = map[string]string{}
	for _, ua := range uaList.Data {
		uaByID[ua.ID] = ua.Value
	}
	mnByID = map[string]string{}
	for _, mn := range mnList.Data {
		mnByID[mn.ID] = mn.Value
	}
	return uaByID, mnByID, nil
}

// importDay fetches and stores one day's heartbeats, returning the count stored.
// Re-importing an overlapping range does not duplicate: SaveHeartbeats upserts on
// the unique_heartbeats constraint.
//
// gaka-unq.1: raw body is decoded twice (typed struct + envelope map) so we
// can warn on unknown/missing/type-changed fields without breaking the import
// on benign additions. Per the design, a missing_required finding on
// heartbeats surfaces as an error-severity finding + skipped day insert
// (this function returns a synthetic error so the outer loop logs it), but
// the job KEEPS RUNNING — matching existing per-day resilience.
func (w *Worker) importDay(ctx context.Context, authHeader, user, day string, mnByID, uaByID map[string]string, drift *driftCollector) (int64, error) {
	q := url.Values{"date": {day}}
	body, err := getRawJSON(ctx, w.baseURL()+"/api/v1/users/current/heartbeats", authHeader, q)
	if err != nil {
		return 0, err
	}
	var hbList heartbeatList
	if err := json.Unmarshal(body, &hbList); err != nil {
		return 0, fmt.Errorf("decode heartbeats: %w", err)
	}

	// Envelope + sampled item drift check. Uniform per-day schema means we
	// only need to sample the first N items (driftSampleSizeDay).
	if data, ok := drift.checkEnvelope("heartbeats", body, jtArray); ok {
		before := drift.hasError()
		drift.checkList("heartbeats", day, data, heartbeatSpec, driftSampleSizeDay)
		// If a NEW error-severity finding just appeared for heartbeats
		// (required field missing/type-changed at the sampled items), skip
		// this day's insert — the ingest would silently mangle rows.
		if !before && drift.hasError() {
			return 0, fmt.Errorf("skipping insert: required heartbeat field(s) missing or type-changed (see drift findings)")
		}
	}

	hbs := convertForDB(user, mnByID, uaByID, hbList.Data)
	if len(hbs) == 0 {
		return 0, nil
	}
	ids, err := w.DB.SaveHeartbeats(ctx, hbs)
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

// ---- wakatime.com response shapes (noPrefixOptions applied) ----

type importHeartbeat struct {
	MachineNameID *string  `json:"machine_name_id"` // wMachine_name_id
	UserAgentID   string   `json:"user_agent_id"`   // wUser_agent_id
	Branch        *string  `json:"branch"`
	Category      *string  `json:"category"`
	Cursorpos     *int64   `json:"cursorpos"`
	Dependencies  []string `json:"dependencies"`
	Entity        string   `json:"entity"`
	IsWrite       *bool    `json:"is_write"`
	Language      *string  `json:"language"`
	Lineno        *int64   `json:"lineno"`
	Lines         *int64   `json:"lines"`
	Project       *string  `json:"project"`
	Type          string   `json:"type"`
	Time          float64  `json:"time"`
}

type heartbeatList struct {
	Data []importHeartbeat `json:"data"`
}

type userAgent struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}
type userAgentList struct {
	Data []userAgent `json:"data"`
}

type machineName struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}
type machineNameList struct {
	Data []machineName `json:"data"`
}

// convertForDB transforms wakatime heartbeats into local HeartbeatPayloads,
// resolving user-agent and machine ids, then enriching from the user agent
// (Import.convertForDb + Database.updateHeartbeats).
func convertForDB(user string, machines, agents map[string]string, hbs []importHeartbeat) []model.HeartbeatPayload {
	out := make([]model.HeartbeatPayload, 0, len(hbs))
	for _, hb := range hbs {
		ua := agents[hb.UserAgentID]
		machine := "wakatime-import"
		if hb.MachineNameID != nil {
			if v, ok := machines[*hb.MachineNameID]; ok {
				machine = v
			}
		}
		m := machine
		info := wakatime.UserAgentInfo(ua)
		u := user
		out = append(out, model.HeartbeatPayload{
			Branch:       hb.Branch,
			Category:     hb.Category,
			Cursorpos:    hb.Cursorpos,
			Dependencies: hb.Dependencies,
			Editor:       info.Editor,
			Plugin:       info.Plugin,
			Platform:     info.Platform,
			Machine:      &m,
			Entity:       hb.Entity,
			FileLines:    hb.Lines,
			IsWrite:      hb.IsWrite,
			Language:     hb.Language,
			Lineno:       hb.Lineno,
			Project:      hb.Project,
			UserAgent:    ua,
			Sender:       &u,
			TimeSent:     hb.Time,
			Type:         model.EntityType(hb.Type),
		})
	}
	return out
}

// genDateRange returns YYYY-MM-DD strings from start..end inclusive (+1 day,
// matching Utils.genDateRange which iterates 0..diffDays+1).
func genDateRange(t0, t1 time.Time) []string {
	start := time.Date(t0.Year(), t0.Month(), t0.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(t1.Year(), t1.Month(), t1.Day(), 0, 0, 0, 0, time.UTC)
	var days []string
	for d := start; !d.After(end.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	return days
}

// DayRange exposes the exact day list an import will process (for the handler to
// stamp total_days and for tests).
func DayRange(t0, t1 time.Time) []string { return genDateRange(t0, t1) }

// TotalDays is the number of days DayRange yields for the given range.
func TotalDays(t0, t1 time.Time) int { return len(genDateRange(t0, t1)) }

// AllTimeRange is the parsed result of wakatime's all_time_since_today endpoint.
type AllTimeRange struct {
	StartDate    string  `json:"startDate"`
	EndDate      string  `json:"endDate"`
	TotalSeconds float64 `json:"totalSeconds"`
	Text         string  `json:"text"`
	HasData      bool    `json:"hasData"`
}

// wakatime all_time_since_today response shape (only the fields we need).
type allTimeResponse struct {
	Data struct {
		TotalSeconds float64 `json:"total_seconds"`
		Text         string  `json:"text"`
		Range        struct {
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		} `json:"range"`
	} `json:"data"`
}

// FetchAllTimeRange queries wakatime.com for how far back a user's data goes.
// apiToken is the raw (already base64-encoded by the client) token; it is used
// verbatim as the Basic credential, identical to how the import worker auths.
//
// gaka-unq.1: this call runs standalone (from a handler, not a job), so drift
// findings are logged at slog "warn" but not persisted anywhere. That's fine
// — the primary drift surface is the import run; this endpoint is only a UX
// helper for auto-populating the date range.
func FetchAllTimeRange(ctx context.Context, apiToken string) (*AllTimeRange, error) {
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(apiToken))
	body, err := getRawJSON(ctx, wakatimeAPI+"/api/v1/users/current/all_time_since_today", authHeader, nil)
	if err != nil {
		return nil, err
	}
	var resp allTimeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	// Best-effort drift check; no worker/log surface here, so we swallow the
	// findings. The primary drift surface is import job runs.
	drift := newDriftCollector()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Data) > 0 {
		drift.checkObject("all_time_since_today", env.Data, allTimeSpec)
	}
	return &AllTimeRange{
		StartDate:    resp.Data.Range.StartDate,
		EndDate:      resp.Data.Range.EndDate,
		TotalSeconds: resp.Data.TotalSeconds,
		Text:         resp.Data.Text,
		HasData:      resp.Data.Range.StartDate != "",
	}, nil
}

// getRawJSON returns the raw response body (bytes) so callers can decode it
// twice — once into the typed struct, once via json.RawMessage for drift
// checks. Reading the body once is important because http.Response.Body is
// single-use.
func getRawJSON(ctx context.Context, endpoint, authHeader string, query url.Values) ([]byte, error) {
	if query != nil {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("wakatime returned %d: %s", resp.StatusCode, string(body))
	}
	// Cap body reads defensively (wakatime heartbeat days can be a few MB but
	// nowhere near this). 32 MB matches typical HTTP body caps.
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}
