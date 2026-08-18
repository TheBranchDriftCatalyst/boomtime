// redis_bus.go — cross-pod progress relay (worker-topology decoupling,
// gaka-8bz follow-up). Under broker=rabbitmq, the AMQPConsumer (worker pod)
// publishes lifecycle events here; every server pod's mirror Registry
// subscribes via PumpBusIntoRegistry and applies them (Registry.Apply) so
// AdminLabelImagesWS stays truthful regardless of which pod is actually
// executing the job. See docs/design/worker-topology-decoupling.md §6.5.
package imagejobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// EventsChannel is the Dragonfly/Redis pub/sub channel image-job lifecycle
// events travel on.
const EventsChannel = "boomtime:imagejobs:events"

// RedisEventBus fans imagejobs.Event values across pods over Dragonfly/
// Redis pub/sub (plain redis:7-alpine stands in for Dragonfly locally —
// same wire protocol, see k8s/overlays/local/broker.yaml).
type RedisEventBus struct {
	rdb     *redis.Client
	channel string
}

// NewRedisEventBus wires a bus on the default EventsChannel.
func NewRedisEventBus(rdb *redis.Client) *RedisEventBus {
	return &RedisEventBus{rdb: rdb, channel: EventsChannel}
}

// Publish JSON-encodes ev and PUBLISHes it on the bus channel.
func (b *RedisEventBus) Publish(ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("imagejobs: marshal event: %w", err)
	}
	return b.rdb.Publish(context.Background(), b.channel, body).Err()
}

// Subscribe returns a channel of decoded events + an unsubscribe func. A
// malformed frame (should never happen — every publisher is this same
// package) is dropped rather than tearing down the relay. The returned
// channel is closed when ctx is cancelled or the underlying subscription
// errors out.
func (b *RedisEventBus) Subscribe(ctx context.Context) (<-chan Event, func()) {
	sub := b.rdb.Subscribe(ctx, b.channel)
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var ev Event
				if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, func() { _ = sub.Close() }
}

// PumpBusIntoRegistry relays every event from bus into mirror.Apply until
// ctx is cancelled or the bus subscription ends. Used by the --role=server
// + broker=rabbitmq wiring in cmd/boomtime/main.go to keep the local
// mirror Registry — and therefore AdminLabelImagesWS — in sync with
// whichever worker pod actually executed the job.
func PumpBusIntoRegistry(ctx context.Context, bus *RedisEventBus, mirror *Registry) {
	sub, unsub := bus.Subscribe(ctx)
	defer unsub()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			mirror.Apply(ev)
		}
	}
}
