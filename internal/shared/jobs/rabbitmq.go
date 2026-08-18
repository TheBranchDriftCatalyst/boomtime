package jobs

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
	amqp "github.com/rabbitmq/amqp091-go"
)

// AMQPProvider runs jobs celery-style over RabbitMQ. Enqueue records the job in
// the `jobs` table (source of truth + admin visibility) THEN publishes its id;
// Run consumes ids, claims the row, and dispatches. Keeping the DB as the record
// layer means the admin Jobs UI reads one place no matter which provider is
// active — the transport only decides how a worker is woken.
//
// The channel is caller-owned (dialed + wired in main.go, alongside the image
// queue's connection); this provider just declares its own durable queue on it.
type AMQPProvider struct {
	ch       *amqp.Channel
	queue    string
	store    *Store
	log      *slog.Logger
	id       string
	prefetch int
	notifier Notifier
	// limiter is the job-layer concurrency throttle (fleet-wide per-kind caps).
	// nil = unbounded. See handle() for the acquire/release + NACK-requeue path.
	limiter KindLimiter
	// logCapture persists each job's log stream on completion (gaka-hney). nil = off.
	logCapture *LogCapture
}

// SetNotifier implements Provider.
func (p *AMQPProvider) SetNotifier(n Notifier) { p.notifier = n }

// SetLimiter implements Provider. nil = unbounded.
func (p *AMQPProvider) SetLimiter(l KindLimiter) { p.limiter = l }

// SetLogCapture implements Provider. nil = off.
func (p *AMQPProvider) SetLogCapture(lc *LogCapture) { p.logCapture = lc }

// NewAMQPProvider declares the durable jobs queue on ch and returns the provider.
func NewAMQPProvider(ch *amqp.Channel, queue string, store *Store, log *slog.Logger, workerID string, prefetch int) (*AMQPProvider, error) {
	if _, err := ch.QueueDeclare(queue, true /*durable*/, false, false, false, nil); err != nil {
		return nil, err
	}
	if prefetch < 1 {
		prefetch = 1
	}
	return &AMQPProvider{ch: ch, queue: queue, store: store, log: log, id: workerID, prefetch: prefetch}, nil
}

// Name implements Provider.
func (p *AMQPProvider) Name() string { return "rabbitmq" }

// Cancel implements Provider/Canceller but is a no-op on the AMQP transport (out
// of scope): in-flight cancel would need per-delivery context tracking mirroring
// LocalProvider.execTracked, plus the same cross-pod Dragonfly pub-sub fan-out to
// reach whichever consumer owns the delivery. Store.MarkCancelled still stops a
// QUEUED job (it's never claimed), so admin-cancel of pending work works here too;
// only interrupting an already-running AMQP handler is unsupported for now.
func (p *AMQPProvider) Cancel(jobID int64) bool { return false }

// Enqueue records the job then publishes its id to the queue.
func (p *AMQPProvider) Enqueue(ctx context.Context, kind string, payload []byte, opts ...EnqueueOption) (int64, error) {
	c := resolveEnqueue(opts)
	id, err := p.store.Enqueue(ctx, kind, c.owner, payload, c.maxAttempts, c.runAt)
	if err != nil {
		return 0, err
	}
	if err := p.publish(ctx, id); err != nil {
		return id, err // recorded but not delivered — a local sweeper/retry could pick it up
	}
	return id, nil
}

func (p *AMQPProvider) publish(ctx context.Context, id int64) error {
	return p.ch.PublishWithContext(ctx, "" /*default exchange*/, p.queue, false, false, amqp.Publishing{
		ContentType:  "text/plain",
		Body:         []byte(strconv.FormatInt(id, 10)),
		DeliveryMode: amqp.Persistent,
	})
}

// Run consumes job ids and dispatches them until ctx is cancelled.
func (p *AMQPProvider) Run(ctx context.Context, reg *Registry) error {
	if err := p.ch.Qos(p.prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := p.ch.Consume(p.queue, p.id, false /*manual ack*/, false, false, false, nil)
	if err != nil {
		return err
	}
	p.log.Info("jobs: rabbitmq provider running", "queue", p.queue, "worker", p.id)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return nil // channel closed
			}
			p.handle(ctx, reg, d)
		}
	}
}

func (p *AMQPProvider) handle(ctx context.Context, reg *Registry, d amqp.Delivery) {
	id, err := strconv.ParseInt(string(d.Body), 10, 64)
	if err != nil {
		_ = d.Ack(false) // malformed — nothing to do, drop it
		return
	}
	job, ok, err := p.store.ClaimByID(ctx, id, p.id)
	if err != nil {
		_ = d.Nack(false, true) // transient DB error — requeue
		return
	}
	if !ok {
		_ = d.Ack(false) // already claimed by another worker, or gone — drop
		return
	}

	// Job-layer concurrency throttle: if this kind is capped, reserve a slot
	// before running. On a lost slot-race (kind already at fleet limit) put the
	// row back to 'queued' (attempt-free) and NACK+requeue the delivery so a
	// later redelivery re-attempts once a slot frees — instead of running over
	// the cap or head-of-line-blocking every other kind. Excluded-before-claim
	// isn't feasible on the push-delivery loop, so the atomic Acquire is the
	// sole guard here; the rare requeue is the only cost. (A tight redeliver
	// spin while saturated is possible; a delay-queue is a scale follow-up.)
	var release func()
	if p.limiter != nil {
		if max := reg.Concurrency()[job.Kind]; max > 0 {
			metrics.JobLimiterMax.WithLabelValues(job.Kind).Set(float64(max))
			rel, okAcq, aerr := p.limiter.Acquire(ctx, job.Kind,
				p.id+":"+strconv.FormatInt(id, 10), max)
			recordAcquire(job.Kind, okAcq, aerr)
			switch {
			case aerr != nil:
				p.log.Warn("jobs: limiter Acquire failed; running unthrottled", "kind", job.Kind, "id", id, "err", aerr)
			case !okAcq:
				if rerr := p.store.Requeue(ctx, id); rerr != nil {
					p.log.Warn("jobs: requeue after slot-race failed", "id", id, "err", rerr)
				}
				metrics.AMQPDeliveriesTotal.WithLabelValues(p.queue, "requeued").Inc()
				_ = d.Nack(false, true) // at limit — redeliver later
				return
			default:
				release = rel
				metrics.JobLimiterInflight.WithLabelValues(job.Kind).Inc()
			}
		}
	}

	oc := execute(ctx, reg, p.store, *job, p.log, p.notifier, p.logCapture)
	if release != nil {
		metrics.JobLimiterInflight.WithLabelValues(job.Kind).Dec()
		release()
	}
	metrics.AMQPDeliveriesTotal.WithLabelValues(p.queue, "processed").Inc()
	_ = d.Ack(false)

	// AMQP has no native delayed retry here, so a retry is re-published for an
	// immediate re-attempt (the Store's run_at backoff is advisory on this
	// transport). A delay-queue is a follow-up if backoff matters at scale.
	if oc == outcomeRetry {
		if perr := p.publish(ctx, id); perr != nil {
			p.log.Warn("jobs: rabbitmq re-publish failed", "id", id, "err", perr)
		}
	}
}
