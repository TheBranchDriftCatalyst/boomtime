// Package backfilljobs is an in-memory job registry for the git-history
// backfill CLI flow (gaka-vh8).
//
// This is a slimmed-down cousin of internal/queue/imagejobs:
//   - Same Registry / Event / Subscribe / Snapshot shape so the
//     WebSocket handler + FE hook can reuse the same wire protocol.
//   - NO Pool: the CLI is the executor. Jobs are created via HTTP by
//     the CLI, get incremented Processed/Written/Skipped counts via
//     PATCH-style server-side helpers as sessions land, and are marked
//     terminal (done / error) by an explicit CLI PATCH once the CLI is
//     done with the repo.
//
// Because the executor is external, this registry does NOT have a
// jobsCh feed channel: nothing on the server side needs to claim jobs.
// The Enqueue API returns the created job, callers pass its ID back on
// every subsequent state change.
//
// Retention windows mirror imagejobs (15min for terminal states) so the
// FE gets a chance to render final counts before the row fades out.
package backfilljobs

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// JobStatus tracks the lifecycle of a single backfill job.
// queued -> running -> (done | error).
type JobStatus string

const (
	StatusQueued  JobStatus = "queued"
	StatusRunning JobStatus = "running"
	StatusDone    JobStatus = "done"
	StatusError   JobStatus = "error"
)

// Job is the public shape stored in the registry and sent over the wire.
// Owner is tracked so the WS filter can gate an admin session to only
// jobs THEY own (the CLI's bearer token maps to a specific admin; the
// FE cookie-auth resolves to the same admin).
type Job struct {
	ID         string     `json:"id"`
	Owner      string     `json:"owner"`
	RepoName   string     `json:"repoName"`
	RepoPath   string     `json:"repoPath"`
	Status     JobStatus  `json:"status"`
	Error      string     `json:"error,omitempty"`
	Total      int        `json:"total"`     // reported by CLI on enqueue
	Processed  int        `json:"processed"` // commits processed
	Written    int        `json:"written"`   // heartbeats accepted
	Skipped    int        `json:"skipped"`   // heartbeats skipped by overlap
	EnqueuedAt time.Time  `json:"enqueuedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// EventKind names one of the three lifecycle events subscribers observe.
type EventKind string

const (
	EventAdded   EventKind = "added"
	EventUpdated EventKind = "updated"
	EventRemoved EventKind = "removed"
)

// Event is a snapshot of one state transition. Removed carries only
// Job.ID (Owner is preserved for filtering).
type Event struct {
	Kind EventKind `json:"kind"`
	Job  Job       `json:"job"`
}

// Retention defaults for terminal-state jobs. Both are the same (15min)
// because the FE doesn't distinguish "success takes longer to fade" from
// "error takes longer to fade" for backfill — a done row is just as
// informational as an errored one.
const (
	DefaultRetentionDone  = 15 * time.Minute
	DefaultRetentionError = 15 * time.Minute
)

// Registry is the single source of truth for in-flight + recently-done
// backfill jobs across all admin users. Owner-scoping happens at the
// subscribe/snapshot boundary — the WS handler filters events by the
// requesting admin's owner.
type Registry struct {
	mu     sync.RWMutex
	jobs   map[string]*Job
	subs   map[int64]chan Event
	nextSub int64
	timers map[string]*time.Timer
	logger *slog.Logger

	retentionDone  time.Duration
	retentionError time.Duration
}

// NewRegistry returns a Registry with default retention windows.
func NewRegistry(logger *slog.Logger) *Registry {
	return NewRegistryWith(logger, DefaultRetentionDone, DefaultRetentionError)
}

// NewRegistryWith is NewRegistry with caller-tunable retention. Tests
// use it to set very short retention for the "removed after done"
// scenario.
func NewRegistryWith(logger *slog.Logger, retentionDone, retentionError time.Duration) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		jobs:           make(map[string]*Job),
		subs:           make(map[int64]chan Event),
		timers:         make(map[string]*time.Timer),
		logger:         logger,
		retentionDone:  retentionDone,
		retentionError: retentionError,
	}
}

// EnqueueInput is the caller-supplied payload for Enqueue.
type EnqueueInput struct {
	Owner    string
	RepoName string
	RepoPath string
	// Total is the number of commits the CLI intends to process. Used
	// by the FE to render a progress bar with a stable denominator.
	// Zero means unknown (the FE renders indeterminate progress).
	Total int
}

// Enqueue creates a new Queued job and broadcasts EventAdded. Unlike
// imagejobs there is no dedupe key — the CLI can enqueue as many
// concurrent repo-scans as it wants, and the FE renders each row.
func (r *Registry) Enqueue(in EnqueueInput) *Job {
	id := uuid.NewString()
	job := &Job{
		ID:         id,
		Owner:      in.Owner,
		RepoName:   in.RepoName,
		RepoPath:   in.RepoPath,
		Total:      in.Total,
		Status:     StatusQueued,
		EnqueuedAt: time.Now().UTC(),
	}
	r.mu.Lock()
	r.jobs[id] = job
	out := *job
	r.broadcastLocked(Event{Kind: EventAdded, Job: out})
	r.mu.Unlock()
	return &out
}

// Update applies a partial patch to a job (any zero-valued field is
// left unchanged). If the patch flips the job into a terminal status
// (done / error) the retention timer starts. Returns the updated Job
// or ok=false when the id is unknown.
type UpdatePatch struct {
	Status    JobStatus
	Error     *string // nil = leave; non-nil (even "") = set to *
	Processed *int
	Written   *int
	Skipped   *int
}

// Update mutates the job under lock and fans the update out. When the
// patch pushes the job into a terminal state a retention timer is
// installed so the FE can render final counts before the row fades.
func (r *Registry) Update(jobID string, p UpdatePatch) (Job, bool) {
	r.mu.Lock()
	job, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return Job{}, false
	}
	if p.Status != "" && p.Status != job.Status {
		job.Status = p.Status
		if p.Status == StatusRunning && job.StartedAt == nil {
			now := time.Now().UTC()
			job.StartedAt = &now
		}
		if p.Status == StatusDone || p.Status == StatusError {
			now := time.Now().UTC()
			job.FinishedAt = &now
		}
	}
	if p.Error != nil {
		job.Error = *p.Error
	}
	if p.Processed != nil {
		job.Processed = *p.Processed
	}
	if p.Written != nil {
		job.Written = *p.Written
	}
	if p.Skipped != nil {
		job.Skipped = *p.Skipped
	}

	out := *job
	r.broadcastLocked(Event{Kind: EventUpdated, Job: out})

	// If we just transitioned into a terminal state, schedule removal.
	if p.Status == StatusDone || p.Status == StatusError {
		retention := r.retentionDone
		if p.Status == StatusError {
			retention = r.retentionError
		}
		if t, has := r.timers[jobID]; has {
			t.Stop()
		}
		r.timers[jobID] = time.AfterFunc(retention, func() {
			r.remove(jobID)
		})
	}
	r.mu.Unlock()
	return out, true
}

// IncrementCounts is a fast-path for the /jobs/:id/heartbeats handler,
// which reports "N heartbeats accepted, M skipped, K commits processed"
// after each session batch. Applied atomically so a burst of batches
// doesn't lose an update to a concurrent PATCH.
//
// Passing zeros for any field means "no change to that field". Also
// auto-flips Status=queued -> Status=running on first increment.
func (r *Registry) IncrementCounts(jobID string, processed, written, skipped int) (Job, bool) {
	r.mu.Lock()
	job, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return Job{}, false
	}
	if job.Status == StatusQueued {
		job.Status = StatusRunning
		now := time.Now().UTC()
		job.StartedAt = &now
	}
	job.Processed += processed
	job.Written += written
	job.Skipped += skipped
	out := *job
	r.broadcastLocked(Event{Kind: EventUpdated, Job: out})
	r.mu.Unlock()
	return out, true
}

// remove deletes a jobID from the registry and broadcasts EventRemoved.
func (r *Registry) remove(jobID string) {
	r.mu.Lock()
	job, ok := r.jobs[jobID]
	if !ok {
		r.mu.Unlock()
		return
	}
	owner := job.Owner
	delete(r.jobs, jobID)
	delete(r.timers, jobID)
	r.broadcastLocked(Event{Kind: EventRemoved, Job: Job{ID: jobID, Owner: owner}})
	r.mu.Unlock()
}

// Subscribe returns a channel that receives every future event + an
// unsubscribe function. The channel is buffered (16); if the subscriber
// can't keep up, the OLDEST event is dropped so the emitter never
// blocks.
//
// NOTE: the channel is NOT filtered by owner. The WS handler is
// responsible for gating on Job.Owner == connectedAdmin before
// forwarding to the client. Keeping the filter at the boundary lets
// tests exercise Subscribe without a per-owner setup.
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

// SnapshotFor returns a defensive copy of every currently-tracked job
// owned by `owner`. Ordered by EnqueuedAt ascending so the FE can
// render deterministically.
func (r *Registry) SnapshotFor(owner string) []Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		if owner != "" && j.Owner != owner {
			continue
		}
		out = append(out, *j)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].EnqueuedAt.Before(out[j-1].EnqueuedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Get returns a defensive copy of a job.
func (r *Registry) Get(jobID string) (Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[jobID]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

// broadcastLocked fans an event out to every subscriber, non-blocking.
// If a subscriber's buffer is full, drop the OLDEST event and try
// again; if still full, drop the new event with a warning.
func (r *Registry) broadcastLocked(ev Event) {
	for id, ch := range r.subs {
		select {
		case ch <- ev:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
				r.logger.Warn("backfilljobs: subscriber buffer wedged, event dropped",
					"subId", id, "kind", ev.Kind, "jobId", ev.Job.ID)
			}
		}
	}
}
