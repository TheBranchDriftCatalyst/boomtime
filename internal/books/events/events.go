// Package events is the books domain's EVENT REGISTRY: a declaration of every
// event the extraction layer can produce, and a validator that holds producers
// to it.
//
// WHY A REGISTRY OF TYPED GO DECLARATIONS, AND NOT A PARSED DSL.
//
// The read side of this system already works this way —
// internal/shared/query/domains.go declares Domains with Measures/Dimensions in
// an init() — and this is the mirror on the write side. A text/YAML DSL was
// considered and rejected: there are no non-engineer authors and no runtime
// reconfiguration need, so it would trade compile-time safety and
// jump-to-definition for a parser, a schema, a validator and a drift test
// between the data files and the Go types they describe.
//
// WHY EXTRACTION IS A FUNCTION AND NOT A FIELD-MAPPING LANGUAGE.
//
// There is no common WIRE format to write a mapping language against. The
// sources are Fiona CDE JSON records, scraped notebook HTML, the Audible library
// feed, Hardcover GraphQL and pushed wakatime heartbeats — a declarative mapper
// covering those would need JSON paths, DOM selectors, unit conversions and
// fallback chains, at which point it is a programming language with worse tools.
//
// The common format is the Event on the way OUT, not the payload on the way in.
// So the seam is:
//
//	wire → source-specific parser → typed struct → Extractor → Event → Validate
//
// Each source keeps a plain Go Extractor (see the Extractor type), which is the
// same "declare metadata, attach a func" shape the codebase already uses for
// pipeline.Steps, climeta.DBLister and corejobs.HandlerFunc.
//
// WHAT THE DECLARATION IS ACTUALLY FOR.
//
// Not documentation for its own sake. Three of these fields are load-bearing,
// because they encode the things that were previously implicit and unenforceable:
//
//   - Time: whether an event's timestamp is reported by the source, merely
//     observed by us, inferred, or genuinely unknown. Validate REFUSES an event
//     that carries a timestamp on a type declared TimeUnknown — which is the
//     "never fabricate a capture time" rule turned from a code comment into a
//     guard. Kindle highlights have no source timestamp; stamping sync-time
//     would date every highlight to the first scrape and quietly corrupt every
//     time-bucketed chart built on them.
//   - Confidence: whether the event was OBSERVED or DERIVED by us. A reading
//     session was never recorded by Amazon at all — we reconstruct it from two
//     position samples and an assumed reading speed. Analytics that weights that
//     the same as a highlight the user actually made is lying.
//   - Extraction: which change-detection strategy produced it, so the failure
//     mode is legible at the point of consumption. See
//     docs/design/change-detection-patterns.md.
package events

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ExtractionKind names the change-detection strategy behind an event. The four
// kinds fail in different ways and need different defences, which is exactly why
// they are named rather than blurred into "sync".
type ExtractionKind string

const (
	// SetReconcile — the source returns the CURRENT SET for a scope and the event
	// is the set difference against what we hold (book_annotations). Fails by
	// absence-as-deletion; defended by internal/books/reconcile.
	SetReconcile ExtractionKind = "set-reconcile"
	// ScalarDelta — the source exposes one advancing number and the event is the
	// temporal difference between consecutive samples (kindle_reading_positions →
	// reading sessions). Fails by ALIASING: sample slower than the source writes
	// and the advance is never observed. Defended by cadence probing, not by set
	// logic. See docs/design/reading-cadence-measurement.md.
	ScalarDelta ExtractionKind = "scalar-delta"
	// Push — the source sends us the event (wakatime heartbeats). No derivation;
	// fails by delivery, not by inference.
	Push ExtractionKind = "push"
	// Replicated — bidirectional sync with conflict resolution (Hardcover LWW).
	// Not capture at all; fails by echo loops. See boom-m5kq.
	Replicated ExtractionKind = "replicated"
)

// TimeSemantics declares where an event's timestamp comes from — the single most
// abused field in any analytics pipeline.
type TimeSemantics string

const (
	// TimeSourceReported — the source told us when it happened (Audible clip
	// creationTime, Hardcover finished_at). Trustworthy as an event time.
	TimeSourceReported TimeSemantics = "source-reported"
	// TimeObserved — we know when WE saw it, not when it happened. Honest for
	// "when did we first learn of this", wrong for "when did the user do this".
	TimeObserved TimeSemantics = "observed"
	// TimeInferred — we computed it (a session boundary reconstructed from
	// position deltas). Real, but derived, and only as good as the model.
	TimeInferred TimeSemantics = "inferred"
	// TimeUnknown — the source reports NO time and none can honestly be derived.
	// Kindle notebook highlights are this. Validate refuses an event of such a
	// type that carries a timestamp anyway.
	TimeUnknown TimeSemantics = "unknown"
)

// Confidence separates what we saw from what we worked out.
type Confidence string

const (
	// Observed — the source asserted this thing exists.
	Observed Confidence = "observed"
	// Derived — we constructed it. A reading session, a transcript.
	Derived Confidence = "derived"
)

// Field declares one payload attribute.
type Field struct {
	Name     string
	Required bool
	// Enum, when non-empty, is the closed set of permitted values. This is how
	// "kind is one of highlight|note|bookmark|clip" stops being tribal knowledge.
	Enum []string
	Doc  string
}

// Type is one registered event declaration.
type Type struct {
	// Name is the event's stable identity, dotted: "annotation.captured".
	Name string
	// Source is the ingest that produces it: kindle | audible | hardcover | wakatime.
	Source string
	// Extraction names the change-detection strategy (and therefore the failure mode).
	Extraction ExtractionKind
	// Time declares where the timestamp comes from. Enforced by Validate.
	Time TimeSemantics
	// Confidence separates observed from derived.
	Confidence Confidence
	// KeyDoc is prose describing the idempotency key and WHY it is that and not
	// something else — the reasoning is the part that gets lost.
	KeyDoc string
	// Fields declares the payload.
	Fields []Field
	// Doc is a one-paragraph description for the catalog.
	Doc string
}

// Event is the common outbound format every extractor produces. It is
// deliberately flat and stringly-typed in Attrs: this is a transport between the
// ingest and storage, not a domain model. The typed row structs (db.BookAnnotation
// and friends) remain the storage model.
type Event struct {
	// Type is the registered Type.Name.
	Type string
	// Owner is the user the event belongs to.
	Owner string
	// Scope is the thing it happened to — an ASIN for books.
	Scope string
	// Key is the idempotency key within (Owner, Scope).
	Key string
	// At is the event time, or nil when the type's Time is TimeUnknown or the
	// source simply omitted one.
	At *time.Time
	// Attrs is the declared payload.
	Attrs map[string]string
}

// Extractor is the per-source seam: turn one already-parsed source record into
// an Event. Generic over the source's own typed struct, so each ingest keeps its
// natural input type and no wire format leaks between sources.
//
// ok=false means "this record is not an event" — an unkeyable row, or a record
// the source includes that we deliberately drop (audible.last_heard is the
// playhead, not a bookmark).
type Extractor[In any] func(owner, scope string, in In) (Event, bool)

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

var registry = map[string]Type{}

// Register declares an event type. Panics on a duplicate or an incoherent
// declaration — this runs at init(), so a mistake fails the process at startup
// rather than producing mislabelled events for a month.
func Register(t Type) {
	if t.Name == "" {
		panic("events: Register with an empty Name")
	}
	if _, dup := registry[t.Name]; dup {
		panic("events: duplicate event type " + t.Name)
	}
	if t.Time == "" || t.Confidence == "" || t.Extraction == "" {
		panic("events: " + t.Name + " must declare Extraction, Time and Confidence")
	}
	registry[t.Name] = t
}

// Lookup returns a registered type.
func Lookup(name string) (Type, bool) { t, ok := registry[name]; return t, ok }

// All returns every registered type, name-sorted — the catalog, for docs, tests
// and the admin surface.
func All() []Type {
	out := make([]Type, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Validate holds a produced event to its declaration. It is the point of the
// registry: without it the declaration is a comment.
func Validate(e Event) error {
	t, ok := registry[e.Type]
	if !ok {
		return fmt.Errorf("events: unregistered event type %q", e.Type)
	}
	if strings.TrimSpace(e.Owner) == "" {
		return fmt.Errorf("events: %s: empty owner", e.Type)
	}
	if strings.TrimSpace(e.Key) == "" {
		// An event with no idempotency key duplicates on every sync.
		return fmt.Errorf("events: %s: empty key", e.Type)
	}

	// THE TIMESTAMP GUARD. A type that declares it has no knowable event time
	// must not carry one — otherwise a producer "helpfully" stamps time.Now() and
	// every time-bucketed chart silently dates the whole corpus to the first sync.
	if t.Time == TimeUnknown && e.At != nil {
		return fmt.Errorf("events: %s declares Time=unknown but the event carries a timestamp (%s) — "+
			"the source reports no event time, so this is a fabricated one", e.Type, e.At.Format(time.RFC3339))
	}

	for _, f := range t.Fields {
		v, present := e.Attrs[f.Name]
		if f.Required && (!present || v == "") {
			return fmt.Errorf("events: %s: required field %q is missing", e.Type, f.Name)
		}
		if present && len(f.Enum) > 0 && v != "" && !contains(f.Enum, v) {
			return fmt.Errorf("events: %s: field %q = %q is not one of %v", e.Type, f.Name, v, f.Enum)
		}
	}
	return nil
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// EmitsActivity reports whether events of this type can honestly project into
// the reading-ACTIVITY stream (the reading analogue of a coding heartbeat).
//
// The precondition is simply whether there is a trustworthy instant to place the
// event at, which the Time declaration already answers. A source-reported or
// inferred time can anchor an activity sample; an observed time (when WE saw it)
// or no time at all cannot, and forcing one would fabricate reading activity that
// never happened.
//
// This is the machine-checkable form of a real architectural claim: the heartbeat
// is a PROJECTION that some events support, not the universal envelope every
// event lives in. A Kindle highlight carries no time and is therefore an artifact
// only; an Audible clip carries a real creationTime and is independent evidence
// of listening at that instant — evidence the position sampler cannot produce,
// because it is bounded by its own poll cadence.
func (t Type) EmitsActivity() bool {
	return t.Time == TimeSourceReported || t.Time == TimeInferred
}
