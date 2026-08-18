// redis_relay_test.go — coverage for the worker->Redis->hub log relay.
//
// No live Redis/Dragonfly (or miniredis) in the loop: the publish side is
// exercised through the redisPublisher interface via a fake that records
// what would have gone over the wire, and the subscribe side is exercised
// through decodeWorkerLogRecord directly — the pure decode step
// SubscribeRedisIntoHub applies to every pub/sub message before injecting
// it into the hub. Together these cover the same "source-tagging + publish/
// subscribe round-trip" contract the live wiring relies on, without a
// network dependency.
package logging

import (
	"context"
	"encoding/json"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/redis/go-redis/v9"
)

// fakePublisher implements redisPublisher, capturing every published
// message instead of talking to a real Redis connection.
type fakePublisher struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (f *fakePublisher) Publish(_ context.Context, _ string, message interface{}) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, message.([]byte))
	return redis.NewIntCmd(context.Background()) // zero-value cmd: Err() is nil, a "successful publish"
}

func (f *fakePublisher) captured() []LogEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]LogEntry, 0, len(f.msgs))
	for _, m := range f.msgs {
		var e LogEntry
		Expect(json.Unmarshal(m, &e)).To(Succeed())
		out = append(out, e)
	}
	return out
}

var _ = Describe("RelayHubToRedis (worker -> Redis publish)", func() {
	It("stamps every published record Source=worker and the given host", func() {
		hub := NewLogHub(10)
		hub.Publish(LogEntry{Msg: "already in the ring before relay starts"})

		pub := &fakePublisher{}
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			RelayHubToRedis(ctx, hub, pub, "boomtime-worker-abc123")
			close(done)
		}()

		// Give the goroutine time to drain the backfill, then push a live
		// entry through the hub it's subscribed to.
		Eventually(func() []LogEntry { return pub.captured() }).Should(HaveLen(1))
		hub.Publish(LogEntry{Msg: "live entry"})
		Eventually(func() []LogEntry { return pub.captured() }).Should(HaveLen(2))

		cancel()
		<-done

		for _, e := range pub.captured() {
			Expect(e.Source).To(Equal("worker"))
			Expect(e.Host).To(Equal("boomtime-worker-abc123"))
		}
		Expect(pub.captured()[0].Msg).To(Equal("already in the ring before relay starts"))
		Expect(pub.captured()[1].Msg).To(Equal("live entry"))
	})

	It("drops a publish error without panicking (best-effort, never blocks logging)", func() {
		hub := NewLogHub(10)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		erroring := publisherFunc(func(ctx context.Context, _ string, _ interface{}) *redis.IntCmd {
			cmd := redis.NewIntCmd(ctx)
			cmd.SetErr(errBoom)
			return cmd
		})

		go RelayHubToRedis(ctx, hub, erroring, "host-1")
		Expect(func() { hub.Publish(LogEntry{Msg: "x"}) }).NotTo(Panic())
	})
})

var _ = Describe("decodeWorkerLogRecord (Redis -> server hub subscribe)", func() {
	It("round-trips a record published by RelayHubToRedis", func() {
		pub := &fakePublisher{}
		hub := NewLogHub(10)
		hub.Publish(LogEntry{Level: "INFO", Msg: "comfyui job finished", Attrs: map[string]string{"job_id": "42"}})

		ctx, cancel := context.WithCancel(context.Background())
		go RelayHubToRedis(ctx, hub, pub, "worker-1")
		Eventually(func() []LogEntry { return pub.captured() }).Should(HaveLen(1))
		cancel()

		wire := pub.msgs[0]
		got, ok := decodeWorkerLogRecord(wire)
		Expect(ok).To(BeTrue())
		Expect(got.Msg).To(Equal("comfyui job finished"))
		Expect(got.Source).To(Equal("worker"))
		Expect(got.Host).To(Equal("worker-1"))
		Expect(got.Attrs).To(HaveKeyWithValue("job_id", "42"))
	})

	It("forces Source=worker even if the wire payload claims otherwise", func() {
		body, err := json.Marshal(LogEntry{Msg: "spoofed", Source: "server"})
		Expect(err).NotTo(HaveOccurred())

		got, ok := decodeWorkerLogRecord(body)
		Expect(ok).To(BeTrue())
		Expect(got.Source).To(Equal("worker"))
	})

	It("drops malformed JSON instead of erroring", func() {
		_, ok := decodeWorkerLogRecord([]byte("}{not json"))
		Expect(ok).To(BeFalse())
	})

	It("feeds a decoded record straight into hub.Publish with the ID/ring-buffer contract intact", func() {
		hub := NewLogHub(10)
		body, err := json.Marshal(LogEntry{Msg: "from worker"})
		Expect(err).NotTo(HaveOccurred())

		e, ok := decodeWorkerLogRecord(body)
		Expect(ok).To(BeTrue())
		hub.Publish(e)

		all := hub.Backfill(0)
		Expect(all).To(HaveLen(1))
		Expect(all[0].Source).To(Equal("worker"))
		Expect(all[0].ID).To(BeEquivalentTo(1))
	})
})

// publisherFunc adapts a plain function to redisPublisher.
type publisherFunc func(ctx context.Context, channel string, message interface{}) *redis.IntCmd

func (f publisherFunc) Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd {
	return f(ctx, channel, message)
}

var errBoom = &staticErr{"boom"}

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }
