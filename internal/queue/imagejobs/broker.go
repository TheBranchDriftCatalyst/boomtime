// broker.go — transport-agnostic seams for the image-job pipeline
// (worker-topology decoupling, gaka-8bz follow-up). *Registry satisfies
// both interfaces unchanged (the default, welded-in-process behavior is
// preserved verbatim); *AMQPProducer satisfies Enqueuer and the rabbitmq
// "mirror" Registry (see Apply, below) satisfies EventSource, letting
// cmd/boomtime and the admin handler stay ignorant of which transport is
// active. See docs/design/worker-topology-decoupling.md §6.2.
package imagejobs

// Enqueuer accepts a regen request. Satisfied by *Registry (broker=
// inprocess, the enqueue and the pool share one process) and by
// *AMQPProducer (broker=rabbitmq, enqueue publishes to a queue a separate
// worker pod consumes). Handler.ImageJobQueue is typed as an Enqueuer so
// the admin regen endpoint (AdminLabelImagesRegenerate) never branches on
// transport.
type Enqueuer interface {
	Enqueue(in EnqueueInput) (*Job, bool)
}

// EventSource is what AdminLabelImagesWS subscribes to for the live
// lifecycle stream + reconnect snapshot. *Registry satisfies it whether
// it's the broker=inprocess registry (fed directly by the in-process Pool)
// or the broker=rabbitmq "mirror" registry (fed by Apply via
// PumpBusIntoRegistry relaying the cross-pod Redis event bus) — either way
// the WS handler code is unchanged.
type EventSource interface {
	Subscribe() (<-chan Event, func())
	Snapshot() []Job
}

// QueueInspector is an OPTIONAL capability an Enqueuer may satisfy to
// report live broker depth for ops/admin visibility. AdminLabelImagesInfo
// type-asserts h.ImageJobQueue against this and includes the result only
// when present. *AMQPProducer satisfies it; *Registry (broker=inprocess)
// does not — there IS no separate broker to inspect there, and the
// in-process "queued" count is already visible via Snapshot().
type QueueInspector interface {
	QueueDepth() (int, error)
}

// Compile-time assertions: *Registry must keep satisfying both seams no
// matter how its internals evolve.
var (
	_ Enqueuer       = (*Registry)(nil)
	_ EventSource    = (*Registry)(nil)
	_ QueueInspector = (*AMQPProducer)(nil)
)
