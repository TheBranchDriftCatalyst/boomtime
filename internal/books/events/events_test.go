// events_test.go — the registry's contract. The declaration is only worth
// writing down if something enforces it, and Validate is that something.
//
// The headline test is TestValidateRejectsFabricatedTimestamp: it is the reason
// this package exists rather than a comment block.
package events

import (
	"strings"
	"testing"
	"time"
)

func mkEvent(over func(*Event)) Event {
	e := Event{
		Type:  "annotation.highlight.captured",
		Owner: "alice",
		Scope: "B08X4WWQCN",
		Key:   "highlight:1234",
		Attrs: map[string]string{
			"kind":          "highlight",
			"positionUnit":  "location",
			"positionStart": "1234",
		},
	}
	if over != nil {
		over(&e)
	}
	return e
}

func TestValidateAcceptsAWellFormedEvent(t *testing.T) {
	if err := Validate(mkEvent(nil)); err != nil {
		t.Fatalf("a well-formed event was rejected: %v", err)
	}
}

// ⚠ THE POINT OF THE REGISTRY. annotation.highlight.captured declares
// Time=unknown because the Kindle notebook reports no capture time. A producer
// that "helpfully" stamps time.Now() would date the ENTIRE corpus to the first
// sync and silently corrupt every time-bucketed chart built on it.
//
// Mutation: delete the TimeUnknown branch in Validate → the fabricated timestamp
// sails through and reaches the database.
func TestValidateRejectsFabricatedTimestamp(t *testing.T) {
	now := time.Now()
	err := Validate(mkEvent(func(e *Event) { e.At = &now }))
	if err == nil {
		t.Fatal("an event declared Time=unknown accepted a timestamp — every highlight would be dated to the first sync")
	}
	if !strings.Contains(err.Error(), "fabricated") {
		t.Fatalf("the error should name the problem plainly, got: %v", err)
	}
}

// The control for the test above: a type that legitimately carries a time must
// still be able to. Without this, Validate could reject ALL timestamps and the
// guard test would still pass.
func TestValidateAcceptsATimestampOnATimedType(t *testing.T) {
	Register(Type{
		Name: "test.timed.captured", Source: "test",
		Extraction: SetReconcile, Time: TimeSourceReported, Confidence: Observed,
		Fields: []Field{{Name: "kind", Required: true}},
	})
	now := time.Now()
	err := Validate(Event{
		Type: "test.timed.captured", Owner: "alice", Scope: "s", Key: "k",
		At: &now, Attrs: map[string]string{"kind": "clip"},
	})
	if err != nil {
		t.Fatalf("a source-reported time must be accepted: %v", err)
	}
}

func TestValidateRejectsUnregisteredType(t *testing.T) {
	if err := Validate(mkEvent(func(e *Event) { e.Type = "annotation.invented" })); err == nil {
		t.Fatal("an unregistered event type must be rejected")
	}
}

// An event with no idempotency key duplicates on every single sync.
func TestValidateRejectsUnkeyedAndUnownedEvents(t *testing.T) {
	if err := Validate(mkEvent(func(e *Event) { e.Key = "" })); err == nil {
		t.Fatal("an event with no key must be rejected")
	}
	if err := Validate(mkEvent(func(e *Event) { e.Owner = "  " })); err == nil {
		t.Fatal("an event with no owner must be rejected")
	}
}

func TestValidateEnforcesRequiredFieldsAndEnums(t *testing.T) {
	missing := mkEvent(func(e *Event) { delete(e.Attrs, "positionUnit") })
	if err := Validate(missing); err == nil {
		t.Fatal("a missing required field must be rejected")
	}

	// The unit enum is the guard against a source inventing a unit nobody can
	// interpret downstream — the clip cutter refuses anything that is not millis.
	bogus := mkEvent(func(e *Event) { e.Attrs["positionUnit"] = "furlongs" })
	err := Validate(bogus)
	if err == nil {
		t.Fatal("a value outside the declared enum must be rejected")
	}
	if !strings.Contains(err.Error(), "furlongs") {
		t.Fatalf("the error should name the offending value, got: %v", err)
	}
}

// The catalog must actually describe the books domain, and every entry must be
// coherent — this is what makes the registry usable as documentation.
func TestRegistryCatalogIsCoherent(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("no event types registered")
	}
	seenKindle := false
	for _, ty := range all {
		if ty.Extraction == "" || ty.Time == "" || ty.Confidence == "" {
			t.Fatalf("%s: incomplete declaration", ty.Name)
		}
		if ty.Source == "kindle" {
			seenKindle = true
			// Every Kindle annotation type must declare TimeUnknown: the notebook
			// reports no capture time, and any other declaration here would be a
			// licence to fabricate one.
			if ty.Time != TimeUnknown {
				t.Fatalf("%s declares Time=%s, but the Kindle notebook reports no capture time", ty.Name, ty.Time)
			}
		}
		for _, f := range ty.Fields {
			if f.Name == "" {
				t.Fatalf("%s: field with no name", ty.Name)
			}
		}
	}
	if !seenKindle {
		t.Fatal("expected the Kindle annotation types in the catalog")
	}
}

// EmitsActivity is the machine-checkable answer to "which events can project
// into a reading heartbeat": only those carrying a trustworthy event time.
func TestEmitsActivityFollowsTimeSemantics(t *testing.T) {
	cases := map[TimeSemantics]bool{
		TimeSourceReported: true,
		TimeInferred:       true,
		TimeObserved:       false,
		TimeUnknown:        false,
	}
	for ts, want := range cases {
		got := Type{Time: ts}.EmitsActivity()
		if got != want {
			t.Fatalf("Time=%s EmitsActivity=%v, want %v", ts, got, want)
		}
	}
	// The concrete consequence: a Kindle highlight cannot become a heartbeat,
	// because there is no honest instant to put it at.
	hl, _ := Lookup("annotation.highlight.captured")
	if hl.EmitsActivity() {
		t.Fatal("a timeless highlight must not project into the activity stream")
	}
}
