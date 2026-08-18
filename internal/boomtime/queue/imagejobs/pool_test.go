// pool_ginkgo_test.go — ginkgo mirror of pool_test.go.
// 1:1 case map (5 stdlib TestXxx):
//
//	TestPool_ExecutesEveryQueuedJob             → Pool > "executes every queued job"
//	TestPool_HonorsConcurrencyLimit             → Pool > "honors the configured concurrency limit"
//	TestPool_ExecutorErrorDoesNotStopWorker     → Pool > "executor error does not stop the worker"
//	TestPool_StopUnblocksClaim                  → Pool > "Stop unblocks workers parked in claim"
//	TestPool_ObservesStateTransitionsInRegistry → Pool > "observes Running and Done transitions in the registry"
package imagejobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newQuietPoolGinkgo(r *Registry, exec Executor, concurrency int) *Pool {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewPool(PoolConfig{
		Concurrency: concurrency,
		Registry:    r,
		Executor:    exec,
		Logger:      logger,
	})
}

var _ = Describe("Pool", func() {
	It("executes every queued job", func() {
		r := newRegistryGinkgo()
		var seen int32
		exec := ExecutorFunc(func(_ context.Context, _ Job) error {
			atomic.AddInt32(&seen, 1)
			return nil
		})
		pool := newQuietPoolGinkgo(r, exec, 2)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		pool.Start(ctx)
		DeferCleanup(func() { pool.Stop(time.Second) })

		for _, id := range []string{"a", "b", "c", "d", "e"} {
			r.Enqueue(EnqueueInput{LabelID: id, Prompt: "p"})
		}

		Eventually(func() int32 { return atomic.LoadInt32(&seen) },
			2*time.Second, 10*time.Millisecond).Should(BeEquivalentTo(5))
	})

	It("honors the configured concurrency limit", func() {
		r := newRegistryGinkgo()
		var (
			mu     sync.Mutex
			active int
			peak   int
		)
		exec := ExecutorFunc(func(_ context.Context, _ Job) error {
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
		pool := newQuietPoolGinkgo(r, exec, 2)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		pool.Start(ctx)
		DeferCleanup(func() { pool.Stop(time.Second) })

		for i := 0; i < 10; i++ {
			// Distinct label IDs so dedupe doesn't collapse them.
			r.Enqueue(EnqueueInput{LabelID: labelNGinkgo(i), Prompt: "p"})
		}

		// Wait long enough for all 10 * 50ms / 2 workers = ~250ms of work
		// PLUS scheduling slack.
		time.Sleep(1 * time.Second)

		mu.Lock()
		got := peak
		mu.Unlock()
		Expect(got).To(Equal(2))
	})

	It("executor error does not stop the worker", func() {
		r := newRegistryGinkgo()
		var seen int32
		exec := ExecutorFunc(func(_ context.Context, job Job) error {
			atomic.AddInt32(&seen, 1)
			if job.LabelID == "boom" {
				return errors.New("blew up")
			}
			return nil
		})
		pool := newQuietPoolGinkgo(r, exec, 1)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		pool.Start(ctx)
		DeferCleanup(func() { pool.Stop(time.Second) })

		r.Enqueue(EnqueueInput{LabelID: "boom", Prompt: "p", Model: "", Size: ""})
		r.Enqueue(EnqueueInput{LabelID: "ok-1", Prompt: "p", Model: "", Size: ""})
		r.Enqueue(EnqueueInput{LabelID: "ok-2", Prompt: "p", Model: "", Size: ""})

		Eventually(func() int32 { return atomic.LoadInt32(&seen) },
			2*time.Second, 10*time.Millisecond).Should(BeEquivalentTo(3))
	})

	It("Stop unblocks workers parked in claim", func() {
		r := newRegistryGinkgo()
		exec := ExecutorFunc(func(_ context.Context, _ Job) error { return nil })
		pool := newQuietPoolGinkgo(r, exec, 2)
		pool.Start(context.Background())
		// With no jobs queued, workers are blocked in claim(). Stop should
		// cancel their context and let them exit.
		Expect(pool.Stop(time.Second)).To(BeTrue(), "pool.Stop timed out; workers did not observe cancellation")
	})

	It("observes Running and Done transitions in the registry", func() {
		r := newRegistryGinkgo()
		var mid Job
		exec := ExecutorFunc(func(_ context.Context, j Job) error {
			mid = j
			return nil
		})
		pool := newQuietPoolGinkgo(r, exec, 1)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		pool.Start(ctx)
		DeferCleanup(func() { pool.Stop(time.Second) })

		sub, unsub := r.Subscribe()
		DeferCleanup(unsub)
		job, _ := r.Enqueue(EnqueueInput{LabelID: "late-night-coder", Prompt: "p", Model: "", Size: ""})

		seenStatuses := map[JobStatus]bool{}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !seenStatuses[StatusDone] {
			select {
			case ev := <-sub:
				if ev.Job.ID == job.ID {
					seenStatuses[ev.Job.Status] = true
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
		Expect(seenStatuses[StatusRunning]).To(BeTrue(), "never observed StatusRunning event")
		Expect(seenStatuses[StatusDone]).To(BeTrue(), "never observed StatusDone event")
		Expect(mid.LabelID).To(Equal("late-night-coder"))
	})
})

func labelNGinkgo(i int) string {
	return "label-" + string(rune('a'+i))
}
