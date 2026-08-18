// Package imagejobs — worker pool.
//
// The pool owns state transitions on the registry. Workers claim a jobID
// from the registry's internal jobs channel, ask for a defensive copy of
// the Job (getForExecute), mark it Running, invoke the injected Executor,
// then mark it Done or Error based on the Executor's return.
//
// Concurrency is bounded by pool.cfg.Concurrency (default 2, override via
// BOOM_LABEL_IMAGE_CONCURRENCY). At concurrency=2, an operator clicking
// "Regen all" on a 100-label catalog fills the registry with 100 queued
// jobs, and at most 2 are running at any moment. Because the queue lives
// on the server, closing the browser tab does not orphan the runs — the
// pool keeps churning through the queue.
//
// The pool does NOT itself manage graceful shutdown of in-flight generations.
// Individual Executor calls receive a cancellable context; Stop() cancels
// that context and waits up to the caller-supplied timeout for the workers
// to observe cancellation and return. In practice a ComfyUI generation
// takes 5-10 minutes and does not check ctx internally, so Stop() with a
// 30s timeout will typically time out and let the process exit anyway.
// This is acceptable — the retention window and lack of DB backing mean
// mid-run restarts drop in-flight jobs regardless.
package imagejobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Executor generates the image for one job. Implementations should NOT
// touch the Registry — the pool owns state transitions. Return nil on
// success, error on failure. Long-running work should check ctx and abort
// early on cancellation.
type Executor interface {
	Execute(ctx context.Context, job Job) error
}

// ExecutorFunc adapts a bare function to the Executor interface. Handy in
// tests and for the labelimages adapter.
type ExecutorFunc func(ctx context.Context, job Job) error

// Execute satisfies Executor.
func (f ExecutorFunc) Execute(ctx context.Context, job Job) error {
	return f(ctx, job)
}

// PoolConfig configures a worker pool. Concurrency must be >= 1; if 0 or
// negative a default of 2 is applied.
type PoolConfig struct {
	Concurrency int
	Registry    *Registry
	Executor    Executor
	Logger      *slog.Logger
}

// Pool is a fixed-size set of worker goroutines that consume jobIDs from
// the registry and invoke the Executor for each one.
type Pool struct {
	cfg    PoolConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPool constructs a Pool but does NOT start it. Call Start to begin
// consuming.
func NewPool(cfg PoolConfig) *Pool {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 2
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Registry == nil {
		panic("imagejobs.NewPool: Registry is required")
	}
	if cfg.Executor == nil {
		panic("imagejobs.NewPool: Executor is required")
	}
	return &Pool{cfg: cfg}
}

// Start spins up Concurrency workers, each in its own goroutine. The passed
// context is the parent; Stop() calls the internal cancel to signal
// workers.
func (p *Pool) Start(ctx context.Context) {
	workerCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	for i := 0; i < p.cfg.Concurrency; i++ {
		p.wg.Add(1)
		go p.worker(workerCtx, i)
	}
	p.cfg.Logger.Info("imagejobs: pool started", "concurrency", p.cfg.Concurrency)
}

// Stop signals all workers to shut down and waits up to timeout for them
// to return. Returns true if all workers exited within the timeout.
// In-flight Executor calls that ignore context cancellation may hold up
// shutdown until the timeout expires.
func (p *Pool) Stop(timeout time.Duration) bool {
	if p.cancel != nil {
		p.cancel()
	}
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		p.cfg.Logger.Warn("imagejobs: pool stop timed out, some workers still running",
			"timeout", timeout)
		return false
	}
}

// worker is the main loop for a single pool goroutine. Runs until ctx is
// cancelled OR the registry's jobs channel is closed (never happens today,
// but future-proof against a torn-down registry).
func (p *Pool) worker(ctx context.Context, id int) {
	defer p.wg.Done()
	for {
		jobID, ok := p.cfg.Registry.claim(ctx)
		if !ok {
			return
		}
		job, ok := p.cfg.Registry.getForExecute(jobID)
		if !ok {
			// Job vanished before we could grab it (retention race /
			// supersede). Skip silently.
			continue
		}
		p.cfg.Registry.MarkRunning(jobID)
		start := time.Now()
		err := p.cfg.Executor.Execute(ctx, job)
		dur := time.Since(start)
		if err != nil {
			p.cfg.Logger.Error("imagejobs: execute failed",
				"worker", id, "jobId", jobID, "labelId", job.LabelID,
				"dur", dur, "err", err)
			p.cfg.Registry.MarkError(jobID, err.Error())
			continue
		}
		p.cfg.Logger.Info("imagejobs: execute done",
			"worker", id, "jobId", jobID, "labelId", job.LabelID, "dur", dur)
		p.cfg.Registry.MarkDone(jobID)
	}
}

// String makes Pool useful in log lines / debug prints.
func (p *Pool) String() string {
	return fmt.Sprintf("imagejobs.Pool(concurrency=%d)", p.cfg.Concurrency)
}
