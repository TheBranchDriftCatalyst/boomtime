// amqp_producer.go — API-side Enqueuer for the worker-topology decoupling
// (gaka-8bz follow-up). Replaces Registry's welded enqueue->jobsCh->Pool
// path with an AMQP publish a separate worker pod consumes, and replaces
// Registry's in-memory byLabel dedup map with a Dragonfly/Redis SET NX
// lock so the same per-label idempotency holds across pods. See
// docs/design/worker-topology-decoupling.md §6.3.
package imagejobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

// dedupKeyPrefix namespaces the Dragonfly SET NX lock. TTL is a generous
// upper bound on a single regen (ComfyUI/SDXL rounds are 15-30s; Chroma-HD
// can run 15-30 MINUTES on M-series — see internal/comfyui/client.go) so a
// worker that crashes mid-job without publishing a terminal event doesn't
// wedge the label forever; the AMQPConsumer clears the lock explicitly on
// every terminal (done/error) event, so this TTL is only a backstop.
const (
	dedupKeyPrefix = "imagejobs:label:"
	dedupTTL       = 30 * time.Minute
)

// jobMessage is the AMQP body (JSON). Mirrors the execute-relevant subset
// of imagejobs.Job — Status/timestamps are re-derived from the lifecycle
// events the consumer publishes, not carried on the wire.
type jobMessage struct {
	JobID       string `json:"jobId"`
	LabelID     string `json:"labelId"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
	Model       string `json:"model,omitempty"`
	Size        string `json:"size,omitempty"`
	Seed        *int64 `json:"seed,omitempty"`
}

// AMQPProducer implements Enqueuer over a RabbitMQ queue. ch must already
// be open on a live *amqp.Connection; rdb backs both the cross-pod dedup
// lock and (via bus) the "queued" event every server pod's mirror Registry
// needs to show the job immediately.
type AMQPProducer struct {
	ch    *amqp.Channel
	queue string
	rdb   *redis.Client
	bus   *RedisEventBus
	log   *slog.Logger
}

var _ Enqueuer = (*AMQPProducer)(nil)

// NewAMQPProducer declares the target queue (durable, idempotent — safe to
// call whether or not a Messaging Topology `Queue` CR already declared it,
// see k8s/overlays/talos00-knowledgedump/rabbitmq.yaml) and returns a
// ready-to-use producer.
func NewAMQPProducer(ch *amqp.Channel, queue string, rdb *redis.Client, bus *RedisEventBus, logger *slog.Logger) (*AMQPProducer, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return nil, fmt.Errorf("imagejobs: queue declare: %w", err)
	}
	return &AMQPProducer{ch: ch, queue: queue, rdb: rdb, bus: bus, log: logger}, nil
}

// Enqueue mirrors Registry.Enqueue's contract (idempotent per LabelID,
// returns existing=true with no publish when the label already has an
// in-flight job) but replaces the in-memory byLabel map with a Dragonfly
// SET NX lock so the dedup holds across the API pod(s) and the separate
// worker pod(s).
func (p *AMQPProducer) Enqueue(in EnqueueInput) (*Job, bool) {
	ctx := context.Background()
	key := dedupKeyPrefix + in.LabelID
	id := uuid.NewString()

	ok, err := p.rdb.SetNX(ctx, key, id, dedupTTL).Result()
	if err != nil {
		// Fail OPEN, matching Registry's own philosophy elsewhere (e.g. a
		// full jobsCh logs a warning but never blocks the caller) — a
		// dedup-check outage shouldn't stop an operator from regenerating
		// a label, it just risks a duplicate in-flight run.
		p.log.Error("imagejobs: dedup lock check failed, enqueueing anyway", "labelId", in.LabelID, "err", err)
		ok = true
	}
	if !ok {
		existingID, gerr := p.rdb.Get(ctx, key).Result()
		if gerr != nil || existingID == "" {
			existingID = id
		}
		return &Job{ID: existingID, LabelID: in.LabelID, Status: StatusQueued}, true
	}

	msg := jobMessage{
		JobID: id, LabelID: in.LabelID, Description: in.Description,
		Prompt: in.Prompt, Model: in.Model, Size: in.Size, Seed: in.Seed,
	}
	body, err := json.Marshal(msg)
	if err != nil {
		_ = p.rdb.Del(ctx, key).Err()
		p.log.Error("imagejobs: marshal job message failed", "labelId", in.LabelID, "err", err)
		return &Job{ID: id, LabelID: in.LabelID, Status: StatusError, Error: err.Error()}, false
	}

	// Same call shape as the homelab flex reference producer: default
	// exchange, routing key == queue name, persistent delivery.
	if err := p.ch.PublishWithContext(ctx, "", p.queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         body,
	}); err != nil {
		_ = p.rdb.Del(ctx, key).Err()
		p.log.Error("imagejobs: publish failed", "labelId", in.LabelID, "jobId", id, "err", err)
		return &Job{ID: id, LabelID: in.LabelID, Status: StatusError, Error: err.Error()}, false
	}

	job := Job{
		ID: id, LabelID: in.LabelID, Description: in.Description, Prompt: in.Prompt,
		Model: in.Model, Size: in.Size, Seed: in.Seed,
		Status: StatusQueued, EnqueuedAt: time.Now().UTC(),
	}
	if p.bus != nil {
		if perr := p.bus.Publish(Event{Kind: EventAdded, Job: job}); perr != nil {
			// Every server pod's mirror misses the immediate "queued" row,
			// but the job itself was published fine — the consumer's own
			// "running" event will still surface it a moment later.
			p.log.Warn("imagejobs: publish queued event failed", "jobId", id, "err", perr)
		}
	}
	return &job, false
}
