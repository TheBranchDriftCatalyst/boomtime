// Package imagejobs is an in-memory job registry + worker pool for the label-
// image regeneration flow (boom-8bz).
//
// Prior to boom-8bz the FE owned the concurrency pool: it POSTed each label to
// /admin/label-images/regenerate, tracked in-flight IDs in a client-side Set,
// throttled parallel requests at MAX_PARALLEL_REGENS=2, and polled the info
// endpoint every 10s to see completion. Reload/close/tab-switch dropped the
// in-flight state entirely — the server kept generating, but the reopened FE
// had no idea a run was still in progress until the next poll hit AFTER
// completion. From the user's perspective, refreshes orphaned runs.
//
// The fix is server-side ownership of the queue + a WebSocket that streams the
// full state (including whatever's currently running/queued/recently-done) to
// any admin connection. When the browser reconnects it gets an immediate
// snapshot, so the UI is durably bound. This is NOT DB-backed — the user
// explicitly does not want persistence across boomtime restarts. If boomtime
// restarts mid-run, in-flight state is lost (ComfyUI's own queue keeps
// running independently, and the retention window is only a UI convenience).
//
// The Registry owns state transitions AND the job pull channel. Enqueue is
// idempotent per label: an outstanding queued/running job for a labelID
// causes Enqueue to return the existing job with existing=true rather than
// starting a duplicate. The pool workers claim jobs by receiving jobIDs on
// the internal jobs channel and calling the injected Executor.
package imagejobs

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labelcatalog"
)

// JobStatus tracks the lifecycle of a single label-image regeneration job.
// A job flows queued -> running -> (done | error). After a retention window
// the terminal-state entry is removed from the registry (a "removed" event
// fires so subscribers can clean their local mirror).
type JobStatus string

const (
	StatusQueued  JobStatus = "queued"
	StatusRunning JobStatus = "running"
	StatusDone    JobStatus = "done"
	StatusError   JobStatus = "error"
)

// Job is the public shape stored in the registry and sent over the wire. Time
// pointers are omitted when zero (json omitempty), matching what the FE hook
// expects.
//
// Description holds the label's rich narrative; the labelimages Executor
// slots it between the systemPrompt and the optimizedPrompt when composing
// the final tag-list (see labelimages.buildFinalPrompt). Empty description
// preserves the pre-boom-8bz {system, prompt} shape.
type Job struct {
	ID          string     `json:"id"`
	LabelID     string     `json:"labelId"`
	Description string     `json:"description,omitempty"`
	Prompt      string     `json:"prompt"`
	Model       string     `json:"model,omitempty"`
	Size        string     `json:"size,omitempty"`
	Seed        *int64     `json:"seed,omitempty"`
	Status      JobStatus  `json:"status"`
	Error       string     `json:"error,omitempty"`
	EnqueuedAt  time.Time  `json:"enqueuedAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

// ToLabelEntry converts a Job's execute-relevant fields into the
// labelcatalog.Entry shape every regeneration entrypoint
// (labelimages.Worker.RegenerateEntry) actually consumes. This is the
// SINGLE place the Job -> Entry mapping happens: cmd/boomtime's Executor
// closure (shared verbatim by both the in-process Pool and the AMQP
// consumer — see main.go) calls this instead of duplicating the struct
// literal per transport. (boom-8bz — shared DRY core.)
func (j Job) ToLabelEntry() labelcatalog.Entry {
	return labelcatalog.Entry{
		ID:          j.LabelID,
		Description: j.Description,
		Prompt:      j.Prompt,
		Model:       j.Model,
		Size:        j.Size,
		Seed:        j.Seed,
	}
}

// EventKind names one of the three lifecycle events subscribers can observe.
// A "removed" event carries only the ID (Job.ID); other fields on Job may be
// zero-valued.
type EventKind string

const (
	EventAdded   EventKind = "added"
	EventUpdated EventKind = "updated"
	EventRemoved EventKind = "removed"
)

// Event is a snapshot of one state transition. For "removed" only Job.ID is
// meaningful — the entry has already been deleted from the registry, so the
// full Job is not available.
type Event struct {
	Kind EventKind `json:"kind"`
	Job  Job       `json:"job"`
}

// Retention defaults. Terminal jobs are held in the registry for this long
// so the FE can show final feedback (green check, red error badge) before
// the row fades out. Overridable per-Registry via NewRegistryWith.
const (
	DefaultRetentionDone  = 5 * time.Minute
	DefaultRetentionError = 15 * time.Minute
)

// Registry is the single source of truth for in-flight + recently-done label-
// image jobs. Concurrent access is guarded by mu; the jobs channel (used by
// the worker pool) is unbuffered for events but ~200 for job IDs (in practice
// the queue never exceeds the label catalog size, so 200 is generous).
type Registry struct {
	mu   sync.RWMutex
	jobs map[string]*Job
	// byLabel tracks the current (queued or running) jobID for a label so
	// Enqueue can dedupe. Cleared when a job finishes (retention timer)
	// OR is superseded by a new Enqueue.
	byLabel map[string]string
	// subs holds subscriber channels. Numeric IDs make Unsubscribe trivial
	// and avoid closure identity comparisons.
	subs    map[int64]chan Event
	nextSub int64
	// timers tracks retention timers keyed by jobID so a supersede can
	// cancel a pending removal.
	timers map[string]*time.Timer
	// jobsCh is the pool feed. Enqueue nonblocking-writes the new jobID
	// here; the pool workers Receive from it. Sized to comfortably hold
	// the whole catalog + margin so Enqueue never has to block.
	jobsCh chan string
	logger *slog.Logger

	retentionDone  time.Duration
	retentionError time.Duration
}

// NewRegistry builds a registry with the default retention windows.
func NewRegistry(logger *slog.Logger) *Registry {
	return NewRegistryWith(logger, DefaultRetentionDone, DefaultRetentionError)
}

// NewRegistryWith is NewRegistry with caller-tunable retention windows. Tests
// use it to set very short retention so the "removed after done" scenario
// finishes in milliseconds.
func NewRegistryWith(logger *slog.Logger, retentionDone, retentionError time.Duration) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		jobs:           make(map[string]*Job),
		byLabel:        make(map[string]string),
		subs:           make(map[int64]chan Event),
		timers:         make(map[string]*time.Timer),
		jobsCh:         make(chan string, 200),
		logger:         logger,
		retentionDone:  retentionDone,
		retentionError: retentionError,
	}
}

// EnqueueInput is the caller-supplied payload for Enqueue. Kept as a
// struct so future additions (systemPrompt override, priority) don't
// grow the positional signature further.
type EnqueueInput struct {
	LabelID     string
	Description string
	Prompt      string
	Model       string
	Size        string
	Seed        *int64
}

// Enqueue is idempotent per labelID. If the label already has an in-flight
// (queued OR running) job, the existing job is returned with existing=true
// and no worker signal is sent. Otherwise a new Queued job is created, an
// EventAdded is broadcast, and the jobID is pushed onto the pool feed.
//
// A previously-terminal job is NOT considered in-flight: if the retention
// window for a done/error job hasn't expired yet, a fresh Enqueue for the
// same labelID cancels the pending removal timer and starts a new job. This
// matches operator intent — clicking Regen on an errored row should try
// again immediately, not wait 15 minutes for the error to age out.
func (r *Registry) Enqueue(in EnqueueInput) (*Job, bool) {
	labelID := in.LabelID
	prompt := in.Prompt
	model := in.Model
	size := in.Size
	seed := in.Seed
	description := in.Description
	r.mu.Lock()
	if existingID, ok := r.byLabel[labelID]; ok {
		if job, exists := r.jobs[existingID]; exists && (job.Status == StatusQueued || job.Status == StatusRunning) {
			// Copy under lock so the caller sees a stable snapshot.
			out := *job
			r.mu.Unlock()
			return &out, true
		}
	}
	// Cancel any pending retention timer for a stale terminal job on this
	// label so operator intent (fresh regen) wins over a lingering error.
	if oldID, ok := r.byLabel[labelID]; ok {
		if t, has := r.timers[oldID]; has {
			t.Stop()
			delete(r.timers, oldID)
		}
		if _, has := r.jobs[oldID]; has {
			delete(r.jobs, oldID)
			// Broadcast a removed event so subscribers drop the row from
			// their local mirror when a supersede happens.
			r.broadcastLocked(Event{Kind: EventRemoved, Job: Job{ID: oldID, LabelID: labelID}})
		}
	}

	id := uuid.NewString()
	job := &Job{
		ID:          id,
		LabelID:     labelID,
		Description: description,
		Prompt:      prompt,
		Model:       model,
		Size:        size,
		Seed:        seed,
		Status:      StatusQueued,
		EnqueuedAt:  time.Now().UTC(),
	}
	r.jobs[id] = job
	r.byLabel[labelID] = id

	// Snapshot under lock so the broadcast + return see the same job value.
	out := *job
	r.broadcastLocked(Event{Kind: EventAdded, Job: out})
	r.mu.Unlock()

	// Nonblocking push to the pool feed. With a 200-slot buffer and a
	// per-label catalog on the order of low hundreds, this should never
	// fail; if it ever does (some future path enqueues thousands at once),
	// the job stays in the registry as Queued and a warning is logged —
	// no worker will pick it up until a slot frees.
	select {
	case r.jobsCh <- id:
	default:
		r.logger.Warn("imagejobs: pool feed full, job will not be picked up until channel drains",
			"jobId", id, "labelId", labelID)
	}
	return &out, false
}

// MarkRunning transitions a job from queued -> running. Called by the pool
// worker just before it invokes the Executor. Returns nil on success or a
// no-op if the job is missing (already retention-expired) — either way the
// pool worker doesn't need to care.
func (r *Registry) MarkRunning(jobID string) {
	r.mu.Lock()
	job, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	job.Status = StatusRunning
	job.StartedAt = &now
	out := *job
	r.broadcastLocked(Event{Kind: EventUpdated, Job: out})
	r.mu.Unlock()
}

// MarkDone transitions a job to done and schedules removal after
// retentionDone. Safe to call on a missing job (no-op).
func (r *Registry) MarkDone(jobID string) {
	r.finalize(jobID, StatusDone, "", r.retentionDone)
}

// MarkError transitions a job to error with the message and schedules
// removal after retentionError. Safe to call on a missing job (no-op).
func (r *Registry) MarkError(jobID string, errMsg string) {
	r.finalize(jobID, StatusError, errMsg, r.retentionError)
}

func (r *Registry) finalize(jobID string, status JobStatus, errMsg string, retention time.Duration) {
	r.mu.Lock()
	job, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	job.Status = status
	job.FinishedAt = &now
	job.Error = errMsg
	out := *job
	r.broadcastLocked(Event{Kind: EventUpdated, Job: out})
	labelID := job.LabelID
	// Schedule removal. Cancel any prior timer for this jobID first — in
	// practice there shouldn't be one, but defense in depth is cheap.
	if t, has := r.timers[jobID]; has {
		t.Stop()
	}
	r.timers[jobID] = time.AfterFunc(retention, func() { r.remove(jobID, labelID) })
	r.mu.Unlock()
}

// remove deletes a jobID from the registry and broadcasts an EventRemoved.
// Called by the retention timer (see finalize) and by Enqueue-supersede.
func (r *Registry) remove(jobID, labelID string) {
	r.mu.Lock()
	delete(r.jobs, jobID)
	delete(r.timers, jobID)
	// Only clear byLabel if the current pointer is this jobID; a newer
	// Enqueue may have already claimed the slot.
	if cur, ok := r.byLabel[labelID]; ok && cur == jobID {
		delete(r.byLabel, labelID)
	}
	r.broadcastLocked(Event{Kind: EventRemoved, Job: Job{ID: jobID, LabelID: labelID}})
	r.mu.Unlock()
}

// Apply applies an externally-sourced Event — relayed from the cross-pod
// Redis event bus by PumpBusIntoRegistry — to this registry's local state
// and rebroadcasts it to local subscribers, WITHOUT pushing anything onto
// jobsCh. This is how the broker=rabbitmq "mirror" Registry stays truthful
// for AdminLabelImagesWS even though the jobs it describes are executing in
// a separate worker pod: the mirror has no Pool and no workers ever claim()
// from it, so Apply is its only write path (see docs/design/worker-
// topology-decoupling.md §6.5).
//
// A Done/Error event also schedules the same retention-window removal a
// source-owned registry's finalize() would — the rabbitmq path has no
// separate wire "removed" event, so each mirror ages its own terminal jobs
// out independently rather than holding them forever.
func (r *Registry) Apply(ev Event) {
	r.mu.Lock()
	switch ev.Kind {
	case EventRemoved:
		if t, has := r.timers[ev.Job.ID]; has {
			t.Stop()
			delete(r.timers, ev.Job.ID)
		}
		delete(r.jobs, ev.Job.ID)
		if cur, ok := r.byLabel[ev.Job.LabelID]; ok && cur == ev.Job.ID {
			delete(r.byLabel, ev.Job.LabelID)
		}
	default: // EventAdded, EventUpdated
		job := ev.Job
		r.jobs[job.ID] = &job
		switch job.Status {
		case StatusQueued, StatusRunning:
			r.byLabel[job.LabelID] = job.ID
		case StatusDone, StatusError:
			if cur, ok := r.byLabel[job.LabelID]; ok && cur == job.ID {
				delete(r.byLabel, job.LabelID)
			}
			retention := r.retentionDone
			if job.Status == StatusError {
				retention = r.retentionError
			}
			jobID, labelID := job.ID, job.LabelID
			if t, has := r.timers[jobID]; has {
				t.Stop()
			}
			r.timers[jobID] = time.AfterFunc(retention, func() { r.remove(jobID, labelID) })
		}
	}
	r.broadcastLocked(ev)
	r.mu.Unlock()
}

// Subscribe returns a channel that receives every future event + an
// unsubscribe function. The channel is buffered (16); if a subscriber can't
// keep up, the OLDEST event in its buffer is discarded so the emitter
// (broadcastLocked) never blocks. Callers should invoke unsubscribe when
// the WebSocket closes so the internal map + goroutines shrink.
//
// Callers wanting to bootstrap from the current state should call
// Snapshot() AFTER Subscribe() (in that order). Doing it in reverse would
// race: an event fired between Snapshot and Subscribe would be missed.
func (r *Registry) Subscribe() (<-chan Event, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := atomic.AddInt64(&r.nextSub, 1)
	ch := make(chan Event, 16)
	r.subs[id] = ch
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if c, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(c)
		}
	}
}

// Snapshot returns a defensive copy of every currently-tracked job. Ordered
// by EnqueuedAt ascending so the FE can render deterministically without
// re-sorting on every update.
func (r *Registry) Snapshot() []Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, *j)
	}
	// Simple insertion sort — the set is small (bounded by concurrency +
	// retention window, so tens at worst) and stable ordering matters
	// more than raw speed here.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].EnqueuedAt.Before(out[j-1].EnqueuedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// broadcastLocked fans an event out to every subscriber. Non-blocking: if a
// subscriber's buffer is full, drain the oldest event and try once more; if
// still full (subscriber stuck), drop and log. Must be called with r.mu
// held.
func (r *Registry) broadcastLocked(ev Event) {
	for id, ch := range r.subs {
		select {
		case ch <- ev:
		default:
			// Buffer full: drain oldest to make room. If still full after
			// that, drop with a warning — indicates a wedged subscriber.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
				r.logger.Warn("imagejobs: subscriber buffer wedged, event dropped",
					"subId", id, "kind", ev.Kind, "jobId", ev.Job.ID)
			}
		}
	}
}

// claim returns the next jobID from the pool feed, or a zero string when
// ctx is done. Used by the worker pool; not exported outside the package
// because it's an internal contract with Pool.
func (r *Registry) claim(ctx doneCh) (string, bool) {
	select {
	case id, ok := <-r.jobsCh:
		if !ok {
			return "", false
		}
		return id, true
	case <-ctx.Done():
		return "", false
	}
}

// getForExecute fetches a defensive copy of a job for the pool worker's
// Executor call. Returns ok=false if the job vanished (retention race).
func (r *Registry) getForExecute(jobID string) (Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[jobID]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

// doneCh is the minimal interface Registry needs to observe context
// cancellation — kept as a tiny interface so the pool can pass its own
// context type without pulling context into this file's signature just for
// one method.
type doneCh interface {
	Done() <-chan struct{}
}
