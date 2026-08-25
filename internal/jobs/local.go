package jobs

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// LocalProvider runs jobs with Postgres as the broker: Enqueue inserts a row,
// Run polls + claims due rows via FOR UPDATE SKIP LOCKED. No extra infra, safe
// with many replicas, and retries "just work" (a retry is a queued row with a
// future run_at that the next poll picks up). This is boomtime's default.
type LocalProvider struct {
	store    *Store
	log      *slog.Logger
	id       string
	poll     time.Duration
	notifier Notifier
	// Kind-routing (boom-hney): include = only these kinds (empty = any);
	// exclude = skip these kinds. Lets the always-on server drain light kinds
	// and a ScaledJob drain the heavy ones off the same queue.
	include []string
	exclude []string
	// limiter is the job-layer throttle: kinds at their fleet-wide concurrency
	// cap are excluded from ClaimNext (stay durably queued) and a slot is
	// Acquired around each run. nil = no throttling (every kind unbounded).
	limiter KindLimiter
	// logCapture persists each job's log stream to object storage on completion
	// (boom-hney). nil = persistence off (no S3 configured).
	logCapture *LogCapture

	// workers is how many jobs this process runs CONCURRENTLY. Before boom-jokv
	// the claim/execute loop was strictly sequential, so one long job
	// head-of-line blocked every other kind on the pod — books-reading-monitor
	// averages ~185s per run and blocked the always-on server roughly 41% of
	// wall-clock, claiming nothing else the whole time. It also made every
	// per-kind concurrency cap unreachable in-process: SetConcurrency could only
	// bind ACROSS pods, so a configured cap of 5 admitted 1.
	//
	// Safe because ClaimNext uses FOR UPDATE SKIP LOCKED — N in-process claimers
	// behave exactly like N pods, which the queue was already designed for.
	workers int

	// cancelMu guards cancels: the per-job CancelFunc of every job CURRENTLY
	// executing on this provider, keyed by job id (admin-cancel support). A job's
	// entry lives only for the duration of its handler run — registered in
	// execTracked before execute, deleted (+ cancelled) when it returns.
	cancelMu sync.Mutex
	cancels  map[int64]context.CancelFunc
}

// SetKindFilter restricts which kinds this provider claims (boom-hney).
func (p *LocalProvider) SetKindFilter(include, exclude []string) {
	p.include = include
	p.exclude = exclude
}

// SetWorkers sets how many jobs this process runs concurrently. n < 1 clamps to
// 1. Must be called before Run/Drain — the count is read once when the pool
// starts, not per iteration.
func (p *LocalProvider) SetWorkers(n int) {
	if n < 1 {
		n = 1
	}
	p.workers = n
}

// workerID stamps locked_by per SLOT rather than per pod. Without it every
// worker in a process would share one id and the column would stop identifying
// a single claimant, which is the thing it exists for. Single-worker processes
// keep the bare id so existing rows and dashboards read unchanged.
func (p *LocalProvider) workerID(slot int) string {
	if p.workers <= 1 {
		return p.id
	}
	return p.id + "#" + strconv.Itoa(slot)
}

// SetLimiter wires the per-kind concurrency throttle. nil = unbounded.
func (p *LocalProvider) SetLimiter(l KindLimiter) { p.limiter = l }

// SetLogCapture wires durable per-job log persistence (boom-hney). nil = off.
func (p *LocalProvider) SetLogCapture(lc *LogCapture) { p.logCapture = lc }

// claimExclude merges the static exclude list with the kinds the limiter reports
// as currently AT their concurrency cap, so ClaimNext skips saturated kinds and
// keeps flowing every other kind. When there's no limiter it's just p.exclude.
//
// This Excluded read is the fast-path (avoid claiming a job we'd only have to
// requeue); the atomic Acquire below is the real guard. A kind that saturates in
// the window between this read and the claim just costs one Requeue — see runJob.
func (p *LocalProvider) claimExclude(ctx context.Context, reg *Registry) []string {
	if p.limiter == nil {
		return p.exclude
	}
	excl, err := p.limiter.Excluded(ctx, reg.Concurrency())
	if err != nil {
		// Fail open: on a limiter read error, fall back to the static exclude so
		// work keeps draining rather than wedging the whole queue.
		p.log.Warn("jobs: limiter Excluded failed; not excluding on this pass", "err", err)
		return p.exclude
	}
	return append(append([]string{}, p.exclude...), excl...)
}

// runJob acquires a concurrency slot for an already-claimed job (when its kind is
// limited), runs it through execute, then releases the slot — on both the success
// and failure paths (execute recovers panics internally, so it always returns).
// It returns false, having Requeued the row, when the slot couldn't be acquired:
// a rare exclude/acquire race whose only cost is that attempt-free Requeue.
// startSlotRefresh re-stamps a held semaphore slot every slotRefreshInterval
// until the returned stop func is called (which signals AND waits for the
// goroutine to exit, so it can be sequenced before release()). No-op when there
// is no limiter.
func (p *LocalProvider) startSlotRefresh(ctx context.Context, kind, holder string) func() {
	if p.limiter == nil {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(slotRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if err := p.limiter.Refresh(ctx, kind, holder); err != nil && ctx.Err() == nil {
					p.log.Warn("jobs: slot refresh failed", "kind", kind, "err", err)
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func (p *LocalProvider) runJob(ctx context.Context, reg *Registry, job Job) bool {
	if p.limiter != nil {
		if max := reg.Concurrency()[job.Kind]; max > 0 {
			// Publish the configured cap (the headroom denominator).
			metrics.JobLimiterMax.WithLabelValues(job.Kind).Set(float64(max))
			release, ok, err := p.limiter.Acquire(ctx, job.Kind,
				p.id+":"+strconv.FormatInt(job.ID, 10), max)
			recordAcquire(job.Kind, ok, err)
			if err != nil {
				p.log.Warn("jobs: limiter Acquire failed; running unthrottled", "kind", job.Kind, "id", job.ID, "err", err)
			} else if !ok {
				// Lost the slot race — leave it queued (Requeue doesn't bump
				// attempts) for a later slot instead of running over the cap.
				if rerr := p.store.Requeue(ctx, job.ID); rerr != nil {
					p.log.Warn("jobs: requeue after slot-race failed", "id", job.ID, "err", rerr)
				}
				return false
			} else if release != nil {
				// This pod holds a slot for the run's duration — reflect it as
				// in-flight vs the cap, releasing on completion.
				metrics.JobLimiterInflight.WithLabelValues(job.Kind).Inc()
				// Re-stamp the slot every slotRefreshInterval so a long-but-LIVE job
				// keeps it (semTTL is short now, so a crashed pod's slot prunes fast).
				// stopRefresh signals-and-waits, so the refresher is guaranteed done
				// BEFORE release() ZREMs — no refresh can resurrect the freed slot.
				holder := p.id + ":" + strconv.FormatInt(job.ID, 10)
				stopRefresh := p.startSlotRefresh(ctx, job.Kind, holder)
				defer func() {
					metrics.JobLimiterInflight.WithLabelValues(job.Kind).Dec()
					stopRefresh()
					release()
				}()
			}
		}
	}
	p.execTracked(ctx, reg, job)
	return true
}

// execTracked derives a per-job cancellable child context, registers its
// CancelFunc so Cancel(job.ID) can interrupt a long handler mid-run, runs the job
// through execute, then unregisters + cancels to free the context. Cancellation
// is COOPERATIVE: the child ctx is cancelled, but only a handler that honors ctx
// (checks ctx.Err() between units of work / passes ctx to its network calls) stops
// promptly — execute() then sees ctx.Err() != nil and skips the Complete/Fail
// write so the admin's 'cancelled' status stands.
func (p *LocalProvider) execTracked(ctx context.Context, reg *Registry, job Job) {
	jobCtx, cancel := context.WithCancel(ctx)
	p.cancelMu.Lock()
	p.cancels[job.ID] = cancel
	p.cancelMu.Unlock()
	defer func() {
		p.cancelMu.Lock()
		delete(p.cancels, job.ID)
		p.cancelMu.Unlock()
		cancel() // release the context (no-op if Cancel already fired)
	}()
	execute(jobCtx, reg, p.store, job, p.log, p.notifier, p.logCapture)
}

// Cancel signals the in-process context of a job currently executing on THIS
// provider, returning whether it was found running here. This is the "easy",
// single-worker cooperative cancel: it interrupts the handler's ctx (the handler
// must honor it to stop). It does NOT touch the DB — the admin path calls
// Store.MarkCancelled for the durable terminal status; a QUEUED job (not executing
// anywhere) returns false here and is stopped purely by that status flip
// (ClaimNext skips non-'queued' rows).
//
// Multi-pod upgrade (Dragonfly/redis bus): with several worker pods the job may be
// running on a DIFFERENT pod than the admin request landed on, so this in-process
// map can't reach it. The cross-pod version publishes "cancel <jobID>" on a
// Dragonfly pub-sub channel every pod subscribes to; each pod checks its own
// `cancels` map and cancels if it owns the run. Store.MarkCancelled stays the
// durable signal in every topology, so a queued job is always stopped regardless.
func (p *LocalProvider) Cancel(jobID int64) bool {
	p.cancelMu.Lock()
	cancel, ok := p.cancels[jobID]
	p.cancelMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// NewLocalProvider builds the Postgres-backed provider. workerID is stamped
// into locked_by for observability.
func NewLocalProvider(store *Store, log *slog.Logger, workerID string) *LocalProvider {
	return &LocalProvider{
		store:   store,
		log:     log,
		id:      workerID,
		poll:    5 * time.Second,
		workers: 1, // conservative default; SetWorkers raises it from config
		cancels: map[int64]context.CancelFunc{},
	}
}

// Name implements Provider.
func (p *LocalProvider) Name() string { return "local" }

// SetNotifier implements Provider.
func (p *LocalProvider) SetNotifier(n Notifier) { p.notifier = n }

// Enqueue implements Enqueuer.
func (p *LocalProvider) Enqueue(ctx context.Context, kind string, payload []byte, opts ...EnqueueOption) (int64, error) {
	c := resolveEnqueue(opts)
	return p.store.Enqueue(ctx, kind, c.owner, payload, c.maxAttempts, c.runAt)
}

// Drain claims + runs every currently-due job, then returns (ScaledJob mode,
// boom-hney). A KEDA ScaledJob creates one pod per pending job; each pod drains
// what it can and exits. A long job runs to completion — a ScaledJob Job is
// never killed on scale-down, which removes the mid-job-kill + redelivery
// amplification at the root. Any extra pod that finds the queue already emptied
// simply exits. Per-job errors are recorded by execute() and don't stop the
// drain. Retries (future run_at) are left for a later pod once they come due.
// Runs p.workers concurrent drain loops (boom-jokv). EXIT SEMANTICS are the
// subtle part: an individual worker stops as soon as IT finds the queue empty,
// but Drain does not return until every worker has stopped. A ScaledJob pod must
// never terminate while a sibling worker is still mid-handler — that would
// reintroduce exactly the mid-job-kill this mode exists to avoid.
func (p *LocalProvider) Drain(ctx context.Context, reg *Registry) error {
	p.log.Info("jobs: draining due queue", "worker", p.id, "workers", p.workers)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		total    int
		firstErr error
	)
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			n, err := p.drainLoop(ctx, reg, slot)
			mu.Lock()
			total += n
			// Report the FIRST error but let siblings finish their current job;
			// killing them on a peer's claim failure would abandon running work.
			if err != nil && firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	p.log.Info("jobs: drain complete", "worker", p.id, "processed", total)
	return nil
}

// drainLoop is one worker's drain cycle; returns how many jobs it ran.
func (p *LocalProvider) drainLoop(ctx context.Context, reg *Registry, slot int) (int, error) {
	n := 0
	for {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		job, ok, err := p.store.ClaimNext(ctx, p.workerID(slot), p.include, p.claimExclude(ctx, reg))
		if err != nil {
			return n, err
		}
		if !ok {
			// Nothing left for THIS worker. Siblings may still be running; Drain
			// waits for them. Retries (future run_at) are left for a later pod.
			return n, nil
		}
		if p.runJob(ctx, reg, *job) {
			n++
		}
	}
}

// Run drains due work then polls, until ctx is cancelled.
//
// Runs p.workers concurrent claim loops (boom-jokv). Each loop is the same
// sequential claim/execute cycle as before; the POOL is what stops one long job
// from blocking every other kind on the pod. Returns only once every worker has
// stopped, so in-flight handlers finish rather than being abandoned mid-run on
// shutdown.
func (p *LocalProvider) Run(ctx context.Context, reg *Registry) error {
	p.log.Info("jobs: local provider running",
		"worker", p.id, "poll", p.poll.String(), "workers", p.workers)
	var wg sync.WaitGroup
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			p.runLoop(ctx, reg, slot)
		}(i)
	}
	wg.Wait()
	return ctx.Err()
}

// runLoop is one worker's claim/execute cycle, identical in shape to the
// pre-pool Run body.
func (p *LocalProvider) runLoop(ctx context.Context, reg *Registry, slot int) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, ok, err := p.store.ClaimNext(ctx, p.workerID(slot), p.include, p.claimExclude(ctx, reg))
		if err != nil {
			p.log.Warn("jobs: claim error", "err", err, "slot", slot)
		}
		if ok {
			// LocalProvider ignores the outcome: a retry is a re-queued row the
			// next poll re-claims once run_at is due. runJob applies the per-kind
			// concurrency throttle (a lost slot-race just requeues the row).
			p.runJob(ctx, reg, *job)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.poll):
		}
	}
}
