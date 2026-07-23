// Package importer runs durable, resumable jobs that migrate a user's history
// from wakatime.com into the local heartbeats table. Jobs are processed
// day-by-day with per-day durability, resilient error handling (one bad day does
// not fail the whole job), cancellation, and live event streaming via a Hub.
package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/wakatime"
)

// ErrWakatimeUnauthorized is returned by fetch helpers when wakatime.com
// answers with 401. Handlers and the worker use errors.Is to distinguish a
// "your key is bad" outcome from a transient network / rate-limit failure —
// gaka-6jm.8 (save-on-success) and gaka-6jm.10 (key_status wiring) both key
// off this sentinel.
var ErrWakatimeUnauthorized = errors.New("wakatime returned 401")

// JobSubmitted is the sole remaining legacy status label still used by the
// submit endpoint's response envelope. MapState + JobPending/Failed/Finished
// were audit-dead (gaka-al6) and were removed.
const JobSubmitted = "JobSubmitted"

// QueueItem is the JSON stored in import_jobs.value (the request + requester).
//
// TypedToken (gaka-6jm.8) is the plaintext key the user typed on submit —
// distinct from ReqPayload.APIToken which, after the handler resolves it, may
// be a fallback (already-saved encrypted key OR server env key). We only
// persist TypedToken to users.encrypted_wakatime_key on END-TO-END success
// (no wakatime 401 seen during the run). When empty, there is no user-scoped
// secret to persist and the save-on-success step is a no-op.
type QueueItem struct {
	ReqPayload model.ImportRequestPayload `json:"reqPayload"`
	Requester  string                     `json:"requester"`
	TypedToken string                     `json:"typedToken,omitempty"`
}

const wakatimeAPI = "https://wakatime.com"

// httpClient is the shared client for outbound wakatime.com fetches. A hung
// upstream read would otherwise stall an entire import job goroutine forever
// (gaka-al6). 60s is generous — wakatime's biggest response (heartbeats for
// one day) rarely exceeds a few hundred KB — while still bounded.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// runningJob is one in-flight job's cancel handle plus a done channel that
// closes when the worker goroutine exits (finishCancelled or finishError
// already ran). Cancel returns done so the handler can wait for the terminal
// DB write to land instead of racing it with a 150ms sleep.
type runningJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Worker owns the running-job registry and the event hub.
type Worker struct {
	db     *db.DB
	logger *slog.Logger
	hub    *Hub

	// BaseURL overrides the wakatime.com base for tests (httptest.Server). If
	// empty the wakatimeAPI constant is used. Kept exported for the importer
	// integration tests without leaking into the public config.
	BaseURL string

	mu      sync.Mutex
	running map[int]*runningJob // jobID -> cancel+done
	base    context.Context     // parent context (server lifetime)
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
		db:      database,
		logger:  logger,
		hub:     hub,
		running: make(map[int]*runningJob),
		base:    base,
	}
}

// RecoverInterrupted marks any queued/running jobs (from a previous process) as
// failed. Called once at startup so a crash/restart never leaves a zombie job.
func (w *Worker) RecoverInterrupted(ctx context.Context) {
	ids, err := w.db.MarkRunningJobsFailed(ctx, "interrupted by restart")
	if err != nil {
		w.logger.Error("failed to recover interrupted import jobs", "err", err)
		return
	}
	for _, id := range ids {
		w.logger.Warn("marked interrupted import job as failed", "id", id)
	}
}

// StartJob launches processing of an existing queued job in the background.
// The job's context is registered for cancellation.
func (w *Worker) StartJob(job *db.Job, item QueueItem) {
	jobCtx, cancel := context.WithCancel(w.base)
	rj := &runningJob{cancel: cancel, done: make(chan struct{})}

	w.mu.Lock()
	w.running[job.ID] = rj
	w.mu.Unlock()

	go func() {
		defer func() {
			w.mu.Lock()
			delete(w.running, job.ID)
			w.mu.Unlock()
			cancel()
			// Signal Cancel waiters AFTER the deferred DB-write path (run's
			// own defers, incl. finishCancelled) has completed — the whole
			// point of the ack channel is that a caller can read the fresh
			// job state and see the terminal write.
			close(rj.done)
		}()
		w.run(jobCtx, job.ID, item)
	}()
}

// Cancel requests cancellation of a running job. Returns a done channel that
// closes AFTER the worker goroutine's terminal DB write lands (finishCancelled
// / finishError), and ok=true if the job was running in this process. When
// ok=false, done is a pre-closed channel (immediate) so callers can wait
// uniformly. The old 150ms sleep in the handler is now `<-done` (gaka-al6).
func (w *Worker) Cancel(jobID int) (<-chan struct{}, bool) {
	w.mu.Lock()
	rj, ok := w.running[jobID]
	w.mu.Unlock()
	if !ok {
		ch := make(chan struct{})
		close(ch)
		return ch, false
	}
	rj.cancel()
	return rj.done, true
}

// run executes a job day-by-day with durable progress and live event publishing.
func (w *Worker) run(ctx context.Context, jobID int, item QueueItem) {
	log := func(level, msg string) {
		l, err := w.db.InsertJobLog(ctx, jobID, level, msg)
		if err != nil {
			// Context cancellation aborts DB writes; still surface at debug.
			w.logger.Debug("failed to persist job log", "job", jobID, "err", err)
			return
		}
		w.hub.Publish(jobID, Event{Type: "log", Log: l})
	}
	publishJob := func(kind string, job *db.Job) {
		if job != nil {
			w.hub.Publish(jobID, Event{Type: kind, Job: job})
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
			w.logger.Warn("marshal drift findings", "job", jobID, "err", err)
			return
		}
		if err := w.db.SetJobDrift(persistCtx, jobID, buf); err != nil {
			w.logger.Warn("persist drift findings", "job", jobID, "err", err)
		}
	}

	// Transition to running.
	job, err := w.db.MarkJobRunning(ctx, jobID)
	if err != nil {
		w.logger.Error("failed to mark job running", "job", jobID, "err", err)
		return
	}
	publishJob("state", job)

	p := item.ReqPayload
	days := genDateRange(p.StartDate, p.EndDate)
	log("info", fmt.Sprintf("starting import for %d day(s): %s .. %s",
		len(days), p.StartDate.Format("2006-01-02"), p.EndDate.Format("2006-01-02")))

	// gaka-6jm.8/.10: track whether wakatime.com issued a 401 at any point in
	// the run. Drives (a) save-on-success — a typed token is only persisted
	// when saw401 stays false end-to-end — and (b) key_status — a 401 flips
	// the row to 'invalid', a clean run to 'valid'. saw401 is only meaningful
	// when a typed token was submitted (otherwise the auth header used a
	// server env key or a previously-saved key we don't want to disturb).
	saw401 := false

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
		if errors.Is(err, ErrWakatimeUnauthorized) {
			saw401 = true
		}
		persistDrift(ctx)
		j, _ := w.db.FinishImportJob(ctx, jobID, db.JobStateFailed, &msg)
		publishJob("state", j)
		w.applyKeyOutcome(item, db.JobStateFailed, saw401)
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
			if errors.Is(dayErr, ErrWakatimeUnauthorized) {
				saw401 = true
			}
		} else {
			importedTotal += n
			log("info", fmt.Sprintf("imported %d heartbeats for %s", n, day))
		}

		j, upErr := w.db.UpdateJobProgress(ctx, jobID, i+1, importedTotal, day)
		if upErr != nil {
			if ctx.Err() != nil {
				withBackgroundTimeout(5*time.Second, persistDrift)
				w.finishCancelled(jobID, publishJob)
				return
			}
			w.logger.Error("failed to persist progress", "job", jobID, "err", upErr)
			continue
		}
		publishJob("progress", j)
	}

	log("info", fmt.Sprintf("imported %d heartbeats across %d days", importedTotal, len(days)))
	// Persist drift BEFORE FinishImportJob so the returned terminal snapshot
	// (and the "state" WS event) carries drift[].
	persistDrift(ctx)
	final, err := w.db.FinishImportJob(ctx, jobID, db.JobStateCompleted, nil)
	if err != nil {
		w.logger.Error("failed to finalize job", "job", jobID, "err", err)
		return
	}
	publishJob("state", final)
	w.logger.Info("import completed", "job", jobID, "user", item.Requester, "imported", importedTotal)
	w.applyKeyOutcome(item, db.JobStateCompleted, saw401)
}

// applyKeyOutcome writes the gaka-6jm.8/.10 outcome for a terminal job:
//
//	saw401 && terminal=failed  → status='invalid' (never persists typed token)
//	!saw401 && terminal=completed && typedToken != ""
//	                           → SetEncryptedWakatimeKey(status='valid') —
//	                             this is the save-on-success path.
//	!saw401 && terminal=completed && typedToken == ""
//	                           → best-effort status='valid' on the row (the
//	                             user used a previously-saved encrypted key
//	                             or the server env key; if there IS a saved
//	                             key we can refresh its status).
//	other outcomes (network, rate limit, cancelled) → NO writes, per spec.
//
// A write failure here is logged (warn) and does not affect the job's own
// terminal state — the import is a success from the user's point of view.
// Uses a bounded background context so a slow DB write can't hold anything up.
func (w *Worker) applyKeyOutcome(item QueueItem, state string, saw401 bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch {
	case saw401:
		// gaka-6jm.10: any 401 during the run means the key is bad. Update
		// the status regardless of terminal state (failed / cancelled with a
		// prior 401 both count). Do NOT persist the typed token — the whole
		// point of save-on-success is to not save known-bad keys.
		if err := w.db.UpdateWakatimeKeyStatus(ctx, item.Requester, db.WakatimeKeyStatusInvalid); err != nil {
			w.logger.Warn("wakatime key_status update to invalid failed",
				"user", item.Requester, "err", err)
		} else {
			w.logger.Debug("save-on-success: SKIP (401 seen); marked key_status=invalid",
				"user", item.Requester)
		}
		return
	case state == db.JobStateCompleted && item.TypedToken != "":
		// gaka-6jm.8: end-to-end success with a fresh typed token — this is
		// the moment to persist encrypted at rest. Encryption failure logs a
		// warning but does not block; the import already succeeded.
		ct, err := auth.Encrypt([]byte(item.TypedToken))
		if err != nil {
			w.logger.Warn("save-on-success: encrypt failed",
				"user", item.Requester, "err", err)
			return
		}
		if err := w.db.SetEncryptedWakatimeKey(ctx, item.Requester, ct, db.WakatimeKeyStatusValid); err != nil {
			w.logger.Warn("save-on-success: persist failed",
				"user", item.Requester, "err", err)
			return
		}
		w.logger.Debug("save-on-success: PERSIST (no 401)",
			"user", item.Requester, "hasSavedWakatimeKey", true)
	case state == db.JobStateCompleted:
		// Import succeeded and there's no fresh typed token. If the user has
		// a previously-saved key we CAN refresh its status to 'valid' (a
		// clean run just re-proved it works). If there's no saved key this
		// is a no-op inside UpdateWakatimeKeyStatus.
		if err := w.db.UpdateWakatimeKeyStatus(ctx, item.Requester, db.WakatimeKeyStatusValid); err != nil {
			w.logger.Warn("wakatime key_status refresh to valid failed",
				"user", item.Requester, "err", err)
		}
	default:
		// Failed/cancelled without a 401 (network, rate limit, user
		// cancel) — leave status untouched per gaka-6jm.10 spec.
		w.logger.Debug("import outcome inconclusive; leaving key_status untouched",
			"user", item.Requester, "state", state)
	}
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
	l, _ := w.db.InsertJobLog(ctx, jobID, "info", "cancelled by user")
	if l != nil {
		w.hub.Publish(jobID, Event{Type: "log", Log: l})
	}
	j, err := w.db.CancelJob(ctx, jobID)
	if err != nil {
		w.logger.Error("failed to mark job cancelled", "job", jobID, "err", err)
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
	ids, err := w.db.SaveHeartbeats(ctx, hbs)
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
	// gaka-1l9: AI-assistance fields wakatime.com started sending on 2026-07-03.
	// All nullable — plugins that don't emit them (older / non-AI) simply omit.
	AIInputTokens      *int64  `json:"ai_input_tokens"`
	AIOutputTokens     *int64  `json:"ai_output_tokens"`
	AILineChanges      *int64  `json:"ai_line_changes"`
	HumanLineChanges   *int64  `json:"human_line_changes"`
	AIPromptLength     *int64  `json:"ai_prompt_length"`
	AISession          *string `json:"ai_session"`
	AISubscriptionPlan *string `json:"ai_subscription_plan"`
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
			// gaka-1l9: AI-assistance fields — pass through as-is (nullable).
			AIInputTokens:      hb.AIInputTokens,
			AIOutputTokens:     hb.AIOutputTokens,
			AILineChanges:      hb.AILineChanges,
			HumanLineChanges:   hb.HumanLineChanges,
			AIPromptLength:     hb.AIPromptLength,
			AISession:          hb.AISession,
			AISubscriptionPlan: hb.AISubscriptionPlan,
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
// apiToken is the RAW wakatime.com api_key as the user copied it from
// wakatime.com (e.g. "waka_<uuid>" or a bare UUID) — this function does the
// single Basic base64-encode into Authorization. Any caller that base64-
// encodes it first would double-encode and wakatime would 401 (gaka-f2l).
// Identical convention to how the import worker auths (fetchLookups).
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
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// gaka-6jm.8/.10: wrap 401 with a sentinel so the worker can
		// distinguish "bad key" from other failures for save-on-success and
		// key_status updates. The status code + response body are still
		// preserved via %w-anchored fmt so operator debugging is unchanged.
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: %s", ErrWakatimeUnauthorized, string(body))
		}
		return nil, fmt.Errorf("wakatime returned %d: %s", resp.StatusCode, string(body))
	}
	// Cap body reads defensively (wakatime heartbeat days can be a few MB but
	// nowhere near this). 32 MB matches typical HTTP body caps.
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}
