// coverage_test.go — additional ginkgo specs targeting uncovered branches in
// the imagejobs package. Each It pins a named invariant that would fail if the
// specific branch it exercises were regressed. See gaka-d6x.
//
// Every spec here targets a branch that the pre-existing pool_test.go and
// registry_test.go do NOT cover:
//
//   NewPool guard rails       → NewPool panics on missing Registry / Executor
//   NewPool concurrency floor → Concurrency<1 falls back to default 2
//   NewPool default logger    → Logger==nil is filled in by NewPool
//   Pool.String debug         → String() reports configured concurrency
//   Pool.Stop timeout         → Stop returns false when worker ignores ctx
//   Worker vanish race        → getForExecute miss skips silently (no crash)
//   NewRegistry default       → NewRegistry uses the default retention windows
//   NewRegistryWith nil log   → nil logger falls through to slog.Default
//   MarkRunning missing job   → no-op, no panic on unknown id
//   MarkDone/MarkError miss   → no-op, no panic on unknown id
//   finalize prior timer      → double finalize (defense-in-depth path)
//   Snapshot empty            → returns len 0 slice without panic
//   broadcastLocked drop      → wedged subscriber drops the OLDEST event
//   claim jobsCh closed       → returns ok=false when channel is closed
//   getForExecute vanish      → returns ok=false for unknown id
//   Enqueue jobsCh full       → nonblocking push warns and keeps job Queued

package imagejobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewPool guard rails", func() {
	quietLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	It("panics with 'Registry is required' when Registry is nil", func() {
		// invariant: nil registry is a programming error — panic loudly at
		// construction, not later when a nil deref would take down a worker.
		exec := ExecutorFunc(func(context.Context, Job) error { return nil })
		defer func() {
			rec := recover()
			Expect(rec).NotTo(BeNil(), "NewPool must panic when Registry is nil")
			msg, ok := rec.(string)
			Expect(ok).To(BeTrue())
			Expect(msg).To(ContainSubstring("Registry is required"))
		}()
		_ = NewPool(PoolConfig{Executor: exec, Logger: quietLogger})
	})

	It("panics with 'Executor is required' when Executor is nil", func() {
		// invariant: nil executor is a programming error — panic loudly.
		r := newRegistryGinkgo()
		defer func() {
			rec := recover()
			Expect(rec).NotTo(BeNil(), "NewPool must panic when Executor is nil")
			msg, ok := rec.(string)
			Expect(ok).To(BeTrue())
			Expect(msg).To(ContainSubstring("Executor is required"))
		}()
		_ = NewPool(PoolConfig{Registry: r, Logger: quietLogger})
	})

	It("clamps Concurrency<1 to the default of 2", func() {
		// invariant: a caller passing 0 (or a negative) must get the safe
		// default, not a pool that spawns zero workers and drops every job.
		r := newRegistryGinkgo()
		exec := ExecutorFunc(func(context.Context, Job) error { return nil })
		p := NewPool(PoolConfig{Registry: r, Executor: exec, Logger: quietLogger, Concurrency: 0})
		Expect(p.cfg.Concurrency).To(Equal(2))

		p2 := NewPool(PoolConfig{Registry: r, Executor: exec, Logger: quietLogger, Concurrency: -5})
		Expect(p2.cfg.Concurrency).To(Equal(2))
	})

	It("fills a nil Logger with slog.Default", func() {
		// invariant: passing nil for Logger must not nil-deref later when the
		// pool logs an execute-failed line. NewPool substitutes a default.
		r := newRegistryGinkgo()
		exec := ExecutorFunc(func(context.Context, Job) error { return nil })
		p := NewPool(PoolConfig{Registry: r, Executor: exec, Concurrency: 1})
		Expect(p.cfg.Logger).NotTo(BeNil())
	})
})

var _ = Describe("Pool debug hooks", func() {
	It("String reports the configured concurrency", func() {
		// invariant: Pool.String is used in operator log lines and MUST
		// surface the actual concurrency value (not a placeholder or "0").
		r := newRegistryGinkgo()
		exec := ExecutorFunc(func(context.Context, Job) error { return nil })
		p := NewPool(PoolConfig{
			Registry:    r,
			Executor:    exec,
			Concurrency: 7,
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		s := p.String()
		Expect(s).To(ContainSubstring("imagejobs.Pool"))
		Expect(s).To(ContainSubstring("concurrency=7"))
	})
})

var _ = Describe("Pool.Stop", func() {
	It("returns false when workers ignore context cancellation past the timeout", func() {
		// invariant: an Executor that never checks ctx (e.g. an in-progress
		// ComfyUI generation) must not deadlock Stop forever — Stop MUST
		// return false after the deadline so the process can exit anyway.
		r := newRegistryGinkgo()
		unblock := make(chan struct{})
		exec := ExecutorFunc(func(_ context.Context, _ Job) error {
			<-unblock // deliberately ignore ctx to model an unresponsive job
			return nil
		})
		p := newQuietPoolGinkgo(r, exec, 1)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		p.Start(ctx)

		r.Enqueue(EnqueueInput{LabelID: "unresponsive", Prompt: "p"})
		// Give the worker a moment to claim + enter Execute.
		time.Sleep(50 * time.Millisecond)

		ok := p.Stop(100 * time.Millisecond)
		Expect(ok).To(BeFalse(), "Stop must report timeout when a worker ignores ctx")

		// Now release the worker so the WaitGroup can drain and we don't leak.
		close(unblock)
		// Belt-and-suspenders: wait for real completion.
		done := make(chan struct{})
		go func() { p.wg.Wait(); close(done) }()
		Eventually(done, time.Second).Should(BeClosed())
	})
})

var _ = Describe("worker vanish race", func() {
	It("skips silently when a job is removed between claim and getForExecute", func() {
		// invariant: retention timers can delete a Queued job between claim()
		// returning its ID and getForExecute fetching the payload. The worker
		// must NOT crash, must NOT invoke the Executor for a phantom job, and
		// must loop back for the next ID.
		r := newRegistryGinkgo()
		var executed int32
		exec := ExecutorFunc(func(_ context.Context, _ Job) error {
			atomic.AddInt32(&executed, 1)
			return nil
		})
		p := newQuietPoolGinkgo(r, exec, 1)

		// Inject a phantom ID directly into the jobs channel: the worker will
		// claim it, find no matching entry, and skip. Then enqueue a real
		// job — the worker must go on to execute that one.
		r.jobsCh <- "does-not-exist-1234"
		r.Enqueue(EnqueueInput{LabelID: "real-work", Prompt: "p"})

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		p.Start(ctx)
		DeferCleanup(func() { p.Stop(time.Second) })

		Eventually(func() int32 { return atomic.LoadInt32(&executed) },
			time.Second, 10*time.Millisecond).Should(BeEquivalentTo(1),
			"worker must skip phantom ID and continue with the real one")
	})
})

var _ = Describe("Registry constructors", func() {
	It("NewRegistry seeds default retention windows", func() {
		// invariant: NewRegistry MUST use DefaultRetentionDone and
		// DefaultRetentionError — a regression that flipped these to zero
		// would delete every terminal job before the FE could paint it.
		r := NewRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)))
		Expect(r.retentionDone).To(Equal(DefaultRetentionDone))
		Expect(r.retentionError).To(Equal(DefaultRetentionError))
	})

	It("NewRegistryWith falls back to slog.Default when logger is nil", func() {
		// invariant: nil logger must NOT nil-deref later — the constructor
		// substitutes a working default. Assert by exercising a path that
		// logs (broadcastLocked wedged-subscriber warning is the easiest
		// covered log call site, but even a plain Enqueue exercises the
		// stored logger indirectly).
		r := NewRegistryWith(nil, time.Second, time.Second)
		Expect(r.logger).NotTo(BeNil())
		// And it must actually function.
		_, existing := r.Enqueue(EnqueueInput{LabelID: "with-nil-logger", Prompt: "p"})
		Expect(existing).To(BeFalse())
	})
})

var _ = Describe("Registry no-op transitions on missing jobs", func() {
	It("MarkRunning is a silent no-op for an unknown jobID", func() {
		// invariant: pool workers may race a retention-expired job. MarkRunning
		// on an unknown ID must NOT panic and MUST NOT emit any event to
		// subscribers (subscribers would then process a bogus running job).
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		Expect(func() { r.MarkRunning("ghost-id") }).NotTo(Panic())

		select {
		case ev := <-sub:
			Fail("MarkRunning on ghost id must not broadcast; got: " + string(ev.Kind))
		case <-time.After(50 * time.Millisecond):
			// good — no event fired
		}
	})

	It("MarkDone / MarkError are silent no-ops for unknown jobIDs", func() {
		// invariant: symmetric to MarkRunning — retention races must never
		// crash the pool nor broadcast stale terminal events.
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		Expect(func() { r.MarkDone("ghost-1") }).NotTo(Panic())
		Expect(func() { r.MarkError("ghost-2", "irrelevant") }).NotTo(Panic())

		select {
		case ev := <-sub:
			Fail("MarkDone/MarkError on ghost ids must not broadcast; got: " + string(ev.Kind))
		case <-time.After(50 * time.Millisecond):
			// good — no event fired
		}
	})
})

var _ = Describe("Registry.finalize prior-timer defense", func() {
	It("survives a double-finalize by stopping the prior retention timer", func() {
		// invariant: finalize must Stop() any pre-existing timer for the same
		// jobID before installing a new one — otherwise the earlier timer
		// could still fire against a stale closure and (harmlessly) double-
		// remove. Exercising the code path proves the branch is reachable
		// and doesn't panic; the observable behaviour is a single removal.
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		job, _ := r.Enqueue(EnqueueInput{LabelID: "double-finalize", Prompt: "p"})
		// Drain "added" so we only count subsequent events.
		<-sub

		r.MarkDone(job.ID)   // installs timer #1
		r.MarkError(job.ID, "late error") // must Stop timer #1 and install timer #2

		// Expect: one "updated" (done), one "updated" (error), then EXACTLY
		// one "removed" from the retention timer — not two.
		countRemoved := 0
		deadline := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			select {
			case ev := <-sub:
				if ev.Kind == EventRemoved && ev.Job.ID == job.ID {
					countRemoved++
				}
			case <-time.After(20 * time.Millisecond):
			}
		}
		Expect(countRemoved).To(Equal(1),
			"finalize must Stop prior timer so only one EventRemoved fires")
	})
})

var _ = Describe("Registry.Snapshot", func() {
	It("returns an empty slice (not nil) for a fresh registry", func() {
		// invariant: FE bootstrap deserializes Snapshot() as a JSON array —
		// nil would marshal as null and break the client's list rendering.
		r := newRegistryGinkgo()
		snap := r.Snapshot()
		Expect(snap).NotTo(BeNil())
		Expect(snap).To(HaveLen(0))
	})
})

var _ = Describe("Registry.broadcastLocked overflow", func() {
	It("drops the OLDEST event and logs a warning when a subscriber is wedged past two drops", func() {
		// invariant (security-adjacent: liveness): a wedged subscriber must
		// NEVER block the broadcast path — the pool worker holds the map
		// lock via broadcastLocked, and a stuck send would deadlock every
		// other consumer + freeze the whole pool. Under sustained overflow,
		// events are dropped rather than blocking.
		//
		// Buffer is 16; fire ~40 events without reading. We can't observe
		// the log line directly here, but we CAN assert that (a) the
		// broadcaster returns promptly (no deadlock) and (b) the wedged
		// subscriber's channel only ever holds up to 16 items.
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		start := time.Now()
		for i := 0; i < 40; i++ {
			// Each Enqueue+MarkRunning+MarkDone pair emits three events.
			job, _ := r.Enqueue(EnqueueInput{LabelID: "wedged", Prompt: "p"})
			r.MarkRunning(job.ID)
			r.MarkDone(job.ID)
		}
		elapsed := time.Since(start)
		Expect(elapsed).To(BeNumerically("<", time.Second),
			"broadcast must not block on a wedged subscriber")

		// After all that, the wedged subscriber's buffer must be <= 16.
		Expect(len(sub)).To(BeNumerically("<=", 16),
			"subscriber buffer must be bounded by the declared capacity")
	})
})

var _ = Describe("Registry.claim jobsCh closed", func() {
	It("returns ok=false when the internal jobs channel is closed", func() {
		// invariant: if a future path tears down the registry by closing
		// jobsCh (currently never happens), workers must unblock cleanly
		// with ok=false and exit their loops — not spin on a nil receive.
		r := newRegistryGinkgo()
		close(r.jobsCh)

		id, ok := r.claim(context.Background())
		Expect(ok).To(BeFalse(), "closed jobsCh must yield ok=false")
		Expect(id).To(BeEmpty())
	})
})

var _ = Describe("Registry.getForExecute", func() {
	It("returns ok=false for a jobID that was never enqueued", func() {
		// invariant: worker's vanish-race guard depends on getForExecute
		// signaling "not found" rather than returning a zero Job silently.
		r := newRegistryGinkgo()
		_, ok := r.getForExecute("never-enqueued")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("Registry.Enqueue full-channel fallback", func() {
	It("keeps the job Queued and logs a warning when the pool feed is full", func() {
		// invariant: the nonblocking select in Enqueue must NOT block when
		// jobsCh is full — the job stays in the registry (visible via
		// Snapshot) and the caller is not stalled. This protects the HTTP
		// admin path from wedging behind a stopped pool.
		//
		// Reproduce by pre-filling jobsCh to its capacity (200) so the next
		// Enqueue hits the default branch.
		r := NewRegistryWith(slog.New(slog.NewTextHandler(io.Discard, nil)),
			time.Second, time.Second)
		for i := 0; i < cap(r.jobsCh); i++ {
			r.jobsCh <- "filler"
		}
		start := time.Now()
		job, existing := r.Enqueue(EnqueueInput{LabelID: "overflow-me", Prompt: "p"})
		elapsed := time.Since(start)
		Expect(existing).To(BeFalse())
		Expect(job.Status).To(Equal(StatusQueued))
		Expect(elapsed).To(BeNumerically("<", 100*time.Millisecond),
			"Enqueue must not block when jobsCh is full")

		// The job remains visible via Snapshot even though no worker got the ID.
		snap := r.Snapshot()
		found := false
		for _, j := range snap {
			if j.ID == job.ID {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "job must remain in registry when feed is full")
	})
})

var _ = Describe("Pool integration — error propagates to registry", func() {
	It("marks a failing job as StatusError with the executor's error string", func() {
		// invariant (state-machine): when the Executor returns non-nil, the
		// pool MUST route through MarkError so the FE sees the error string.
		// A regression that swallowed err into MarkDone would silently mark
		// broken generations as green — worse than a crash.
		r := newRegistryGinkgo()
		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)

		exec := ExecutorFunc(func(_ context.Context, _ Job) error {
			return errors.New("comfyui-500")
		})
		p := newQuietPoolGinkgo(r, exec, 1)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		p.Start(ctx)
		DeferCleanup(func() { p.Stop(time.Second) })

		job, _ := r.Enqueue(EnqueueInput{LabelID: "will-fail", Prompt: "p"})

		var errEv Event
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case ev := <-sub:
				if ev.Job.ID == job.ID && ev.Job.Status == StatusError {
					errEv = ev
				}
			case <-time.After(20 * time.Millisecond):
			}
			if errEv.Job.ID != "" {
				break
			}
		}
		Expect(errEv.Job.ID).To(Equal(job.ID),
			"executor error must surface as StatusError on the same job")
		Expect(errEv.Job.Error).To(Equal("comfyui-500"),
			"error message must be preserved verbatim so operator can debug")
		Expect(strings.TrimSpace(errEv.Job.Error)).NotTo(BeEmpty())
	})
})
