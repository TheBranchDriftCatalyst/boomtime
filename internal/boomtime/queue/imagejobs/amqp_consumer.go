// amqp_consumer.go — worker-side execution loop for the worker-topology
// decoupling (boom-8bz follow-up). Consumes jobMessage deliveries off the
// RabbitMQ queue and runs the SAME Executor the in-process Pool used
// (labelimages.Worker.RegenerateEntry, wired by cmd/boomtime — the ComfyUI
// orchestration itself is untouched), publishing lifecycle events to the
// cross-pod Redis bus so every server pod's mirror Registry can relay them
// to AdminLabelImagesWS. See docs/design/worker-topology-decoupling.md §6.4.
package imagejobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

// AMQPConsumer is the worker pod's half of the decoupled pipeline.
type AMQPConsumer struct {
	ch          *amqp.Channel
	queue       string
	exec        Executor
	bus         *RedisEventBus
	rdb         *redis.Client
	log         *slog.Logger
	concurrency int
}

// NewAMQPConsumer wires a consumer. concurrency sets both the AMQP prefetch
// (Qos) and the number of worker goroutines Run fans deliveries out to —
// the same BOOM_LABEL_IMAGE_CONCURRENCY knob that sized the in-process
// Pool means the same thing here.
func NewAMQPConsumer(ch *amqp.Channel, queue string, exec Executor, bus *RedisEventBus, rdb *redis.Client, concurrency int, logger *slog.Logger) *AMQPConsumer {
	if concurrency < 1 {
		concurrency = 2
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AMQPConsumer{ch: ch, queue: queue, exec: exec, bus: bus, rdb: rdb, concurrency: concurrency, log: logger}
}

// Run declares the queue (durable, idempotent — mirrors NewAMQPProducer so
// either side can boot first; also stands in for the Messaging Topology
// `Queue` CR when running against local plain RabbitMQ with no operator,
// see k8s/overlays/local/broker.yaml), sets Qos to `concurrency` (one
// unacked delivery per worker goroutine), and fans deliveries out to
// `concurrency` worker goroutines. Blocks until ctx is cancelled or the
// delivery channel closes (broker connection lost); returns nil on a clean
// shutdown.
//
// Graceful drain on ctx cancel (SIGTERM): workers stop pulling new
// deliveries; an in-flight RegenerateEntry either finishes within the
// pod's terminationGracePeriodSeconds or is killed with the delivery still
// unacked, which redelivers on reconnect — safe because RegenerateEntry
// deletes-then-writes the label_images row (last-write-wins).
func (c *AMQPConsumer) Run(ctx context.Context) error {
	if _, err := c.ch.QueueDeclare(c.queue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("imagejobs: queue declare: %w", err)
	}
	if err := c.ch.Qos(c.concurrency, 0, false); err != nil {
		return fmt.Errorf("imagejobs: qos: %w", err)
	}
	deliveries, err := c.ch.Consume(c.queue, "", false /* autoAck */, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("imagejobs: consume: %w", err)
	}
	c.log.Info("imagejobs: amqp consumer started", "queue", c.queue, "concurrency", c.concurrency)

	var wg sync.WaitGroup
	for i := 0; i < c.concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.worker(ctx, id, deliveries)
		}(i)
	}
	wg.Wait()
	return nil
}

func (c *AMQPConsumer) worker(ctx context.Context, id int, deliveries <-chan amqp.Delivery) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}
			c.handle(ctx, id, d)
		}
	}
}

func (c *AMQPConsumer) handle(ctx context.Context, workerID int, d amqp.Delivery) {
	var msg jobMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		c.log.Error("imagejobs: malformed job message, dropping", "worker", workerID, "err", err)
		_ = d.Nack(false, false) // poison message — do not requeue
		return
	}

	now := time.Now().UTC()
	c.publish(Event{Kind: EventUpdated, Job: Job{ID: msg.JobID, LabelID: msg.LabelID, Status: StatusRunning, StartedAt: &now}})

	job := Job{
		ID: msg.JobID, LabelID: msg.LabelID, Description: msg.Description,
		Prompt: msg.Prompt, Model: msg.Model, Size: msg.Size, Seed: msg.Seed,
	}
	start := time.Now()
	execErr := c.exec.Execute(ctx, job)
	dur := time.Since(start)
	finished := time.Now().UTC()

	if execErr != nil {
		c.log.Error("imagejobs: execute failed", "worker", workerID, "jobId", msg.JobID, "labelId", msg.LabelID, "dur", dur, "err", execErr)
		c.publish(Event{Kind: EventUpdated, Job: Job{ID: msg.JobID, LabelID: msg.LabelID, Status: StatusError, Error: execErr.Error(), FinishedAt: &finished}})
	} else {
		c.log.Info("imagejobs: execute done", "worker", workerID, "jobId", msg.JobID, "labelId", msg.LabelID, "dur", dur)
		c.publish(Event{Kind: EventUpdated, Job: Job{ID: msg.JobID, LabelID: msg.LabelID, Status: StatusDone, FinishedAt: &finished}})
	}

	// Clear the cross-pod dedup lock now that the label is no longer
	// in-flight — a fresh Enqueue for the same label should race against
	// nothing once this job has reached a terminal state.
	if c.rdb != nil {
		if derr := c.rdb.Del(ctx, dedupKeyPrefix+msg.LabelID).Err(); derr != nil {
			c.log.Warn("imagejobs: dedup lock clear failed", "labelId", msg.LabelID, "err", derr)
		}
	}

	// Manual ack AFTER the terminal event — at-least-once delivery. A crash
	// between execute() and here leaves the delivery unacked; it redelivers
	// and RegenerateEntry's delete-then-write makes the retry idempotent.
	if aerr := d.Ack(false); aerr != nil {
		c.log.Error("imagejobs: ack failed", "jobId", msg.JobID, "err", aerr)
	}
}

func (c *AMQPConsumer) publish(ev Event) {
	if c.bus == nil {
		return
	}
	if err := c.bus.Publish(ev); err != nil {
		c.log.Warn("imagejobs: publish event failed", "kind", ev.Kind, "jobId", ev.Job.ID, "err", err)
	}
}
