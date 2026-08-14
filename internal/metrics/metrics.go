// Package metrics is a tiny, process-global, in-memory rolling time-series
// registry for observability. It is the "generic pattern" backing the admin
// Metrics dashboard: instrument anything with a single call —
//
//	metrics.Inc("http.requests", 1)   // a counter-rate series (events/bucket)
//	metrics.Observe("jobs.inflight", 3) // a gauge series (last value/bucket)
//
// and it automatically appears in Snapshot() (and therefore on the graph).
// No pre-registration, no wiring: the first call for a name lazily creates the
// series.
//
// DESIGN GOALS
//
//   - Non-blocking + concurrency-safe. This is INSTRUMENTATION — it must never
//     slow, block, or panic the thing it measures. The hot path (Inc/Observe)
//     takes a short per-series lock and does O(1) work; it allocates nothing
//     after a series' first touch.
//   - Bounded memory. Each series holds a fixed ring of per-minute buckets
//     covering the last Window (~2h). Old buckets are overwritten in place; the
//     registry never grows unless a genuinely new NAME is introduced, so keep
//     cardinality bounded at the call sites (group by route class / kind, not
//     by raw id — see Name()).
//   - Rate semantics. A counter series' bucket value is the COUNT of events in
//     that one-minute bucket, i.e. a per-minute rate. The FE renders that
//     directly as "rate over time".
//
// The registry is a package-global singleton (like a Prometheus default
// registry) so any package can instrument without threading a handle through.
// Tests that need isolation construct their own *Registry via NewRegistry.
package metrics

import (
	"sort"
	"sync"
	"time"
)

// BucketDur is the width of one time bucket. A counter series' value in a
// bucket is the number of events observed during that minute (a per-minute
// rate); a gauge series' value is the last value observed during it.
const BucketDur = time.Minute

// ringSize is the number of buckets retained per series. BucketDur*ringSize is
// the rolling window shown on the graph.
const ringSize = 120

// Window is the total time span retained per series (BucketDur * ringSize).
const Window = BucketDur * ringSize

// Kind distinguishes the two supported metric shapes.
type Kind int

const (
	// Counter accumulates events into the current bucket; the bucket value is a
	// per-bucket count (a rate). Inc feeds counter series.
	Counter Kind = iota
	// Gauge records the last observed value in the current bucket (e.g. current
	// in-flight count). Observe feeds gauge series.
	Gauge
)

func (k Kind) String() string {
	if k == Gauge {
		return "gauge"
	}
	return "counter"
}

// Point is one bucket of a series: the bucket's start (aligned to BucketDur)
// and its value (a per-minute count for a Counter, the last value for a Gauge).
type Point struct {
	// Bucket is the RFC3339 start of the one-minute bucket.
	Bucket time.Time `json:"bucket"`
	Value  float64   `json:"value"`
}

// Series is a named time series plus its retained points, oldest→newest. It is
// the JSON shape returned by Snapshot and consumed by the FE RateChart.
type Series struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`           // "counter" | "gauge"
	Unit   string  `json:"unit,omitempty"` // optional display unit, e.g. "req"
	Points []Point `json:"points"`
}

// ---------------------------------------------------------------------------
// series — one named ring of buckets
// ---------------------------------------------------------------------------

type bucket struct {
	start time.Time // aligned bucket start; zero == never written
	value float64
}

type series struct {
	name string
	kind Kind
	unit string

	mu   sync.Mutex
	buf  []bucket  // ring of ringSize buckets, indexed by head
	head int       // index of the newest (current) bucket
	cur  time.Time // aligned start of buf[head]
}

func newSeries(name string, kind Kind, unit string) *series {
	return &series{
		name: name,
		kind: kind,
		unit: unit,
		buf:  make([]bucket, ringSize),
	}
}

// observe folds v into the series at time now. For a Counter it adds v to the
// current bucket; for a Gauge it replaces the current bucket's value. Advancing
// to a new bucket zeroes the buckets stepped over so a burst-then-silence gap
// reads as zero, not as a stale earlier value.
func (s *series) observe(now time.Time, v float64) {
	aligned := now.Truncate(BucketDur)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cur.IsZero() {
		// First write: seed the current bucket.
		s.cur = aligned
		s.head = 0
		s.buf[0] = bucket{start: aligned, value: 0}
	} else if aligned.After(s.cur) {
		// Advance the ring one bucket at a time so intervening idle minutes are
		// materialized as explicit zero buckets. A gap wider than the ring just
		// resets every bucket (bounded work — at most ringSize steps).
		steps := int(aligned.Sub(s.cur) / BucketDur)
		if steps > ringSize {
			steps = ringSize
		}
		for i := 0; i < steps; i++ {
			s.head = (s.head + 1) % ringSize
			s.buf[s.head] = bucket{start: s.cur.Add(time.Duration(i+1) * BucketDur), value: 0}
		}
		s.cur = aligned
		// The final advanced bucket must carry the true aligned start (the loop
		// above set it from cur+steps*BucketDur which equals aligned, but guard
		// against integer drift by pinning it).
		s.buf[s.head].start = aligned
	} else if aligned.Before(s.cur) {
		// Clock went backwards (NTP step) — fold into the current bucket rather
		// than rewinding the ring.
		aligned = s.cur
	}

	switch s.kind {
	case Gauge:
		s.buf[s.head].value = v
	default:
		s.buf[s.head].value += v
	}
}

// snapshot returns the series' points within [now-Window, now], oldest→newest,
// densified (idle buckets between the first touched bucket and the current
// bucket appear as explicit zero points so the FE draws a continuous line). A
// since cutoff (zero == no filter) drops points before it. Returns nil when the
// series has no points in range.
func (s *series) snapshot(now, since time.Time) Series {
	cutoff := now.Add(-Window)
	if since.After(cutoff) {
		cutoff = since
	}
	curStart := now.Truncate(BucketDur)

	s.mu.Lock()
	byStart := make(map[time.Time]float64, len(s.buf))
	var earliest time.Time
	for _, b := range s.buf {
		if b.start.IsZero() || b.start.Before(cutoff) || b.start.After(curStart) {
			continue
		}
		byStart[b.start] = b.value
		if earliest.IsZero() || b.start.Before(earliest) {
			earliest = b.start
		}
	}
	kind, unit, name := s.kind, s.unit, s.name
	s.mu.Unlock()

	out := Series{Name: name, Kind: kind.String(), Unit: unit}
	if earliest.IsZero() {
		return out
	}
	for t := earliest; !t.After(curStart); t = t.Add(BucketDur) {
		out.Points = append(out.Points, Point{Bucket: t, Value: byStart[t]})
	}
	return out
}

// ---------------------------------------------------------------------------
// Registry — the collection of named series
// ---------------------------------------------------------------------------

// Registry holds a set of named series. The package-level default registry
// (used by Inc/Observe/Snapshot) is the one everything instruments; tests may
// use their own via NewRegistry for isolation.
type Registry struct {
	mu     sync.RWMutex
	series map[string]*series
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{series: make(map[string]*series)}
}

// get returns the series for name, creating it (with the given kind) on first
// use. The kind is fixed at creation — a name first seen as a Counter stays a
// Counter even if a later Observe references it (that Observe is treated as an
// add). Callers should not mix Inc and Observe on one name.
func (r *Registry) get(name string, kind Kind) *series {
	r.mu.RLock()
	s := r.series[name]
	r.mu.RUnlock()
	if s != nil {
		return s
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s = r.series[name]; s != nil { // lost the create race
		return s
	}
	s = newSeries(name, kind, "")
	r.series[name] = s
	return s
}

// Inc adds n to the counter series `name` in the current bucket, creating the
// series on first use. This is the primary instrumentation call: put it
// anywhere an event happens and the series shows up in Snapshot().
func (r *Registry) Inc(name string, n float64) {
	r.get(name, Counter).observe(time.Now(), n)
}

// Observe sets the gauge series `name` to v in the current bucket, creating the
// series on first use. Use for point-in-time values (in-flight counts, queue
// depth) rather than event rates.
func (r *Registry) Observe(name string, v float64) {
	r.get(name, Gauge).observe(time.Now(), v)
}

// Snapshot returns every series with its retained points, oldest→newest,
// sorted by name for stable output. Points before `since` (zero == the full
// Window) are dropped. Series with no points in range are still returned (empty
// Points) so a freshly-created series appears on the graph immediately.
func (r *Registry) Snapshot(since time.Time) []Series {
	now := time.Now()
	r.mu.RLock()
	all := make([]*series, 0, len(r.series))
	for _, s := range r.series {
		all = append(all, s)
	}
	r.mu.RUnlock()

	out := make([]Series, 0, len(all))
	for _, s := range all {
		out = append(out, s.snapshot(now, since))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------------------------------------------------------------------------
// Package-global default registry + convenience helpers
// ---------------------------------------------------------------------------

var defaultRegistry = NewRegistry()

// Default returns the process-global registry that Inc/Observe/Snapshot use.
func Default() *Registry { return defaultRegistry }

// Inc adds n to the counter series `name` on the default registry.
func Inc(name string, n float64) { defaultRegistry.Inc(name, n) }

// Observe sets the gauge series `name` to v on the default registry.
func Observe(name string, v float64) { defaultRegistry.Observe(name, v) }

// Snapshot returns the default registry's series (see Registry.Snapshot).
func Snapshot(since time.Time) []Series { return defaultRegistry.Snapshot(since) }

// Name builds a bounded-cardinality series name of the form
// base{k1=v1,k2=v2}. Use it to attach a SMALL, bounded label (kind, method,
// outcome) — never a per-id or per-user value, which would explode the series
// count. Pairs are appended in the given order (already stable at call sites).
//
//	metrics.Name("jobs.limiter.acquired", "kind", kind) => "jobs.limiter.acquired{kind=github-stats-refresh}"
func Name(base string, pairs ...string) string {
	if len(pairs) < 2 {
		return base
	}
	b := make([]byte, 0, len(base)+16)
	b = append(b, base...)
	b = append(b, '{')
	first := true
	for i := 0; i+1 < len(pairs); i += 2 {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = append(b, pairs[i]...)
		b = append(b, '=')
		b = append(b, pairs[i+1]...)
	}
	b = append(b, '}')
	return string(b)
}
