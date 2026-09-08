// reconcile_test.go — the set-diff kernel. Most of this file exists for ONE
// assertion: an empty fetch retires nothing. Everything else is the supporting
// cast that stops that guard from being satisfied by a function that simply
// never retires anything.
package reconcile

import (
	"reflect"
	"testing"
)

var complete = Options{Policy: RetireOnCompleteFetch, FetchComplete: true}

func TestBuildPlanClassifiesNewSurvivingAndMissing(t *testing.T) {
	p := BuildPlan(
		[]string{"b", "c", "d"}, // fetched
		[]string{"a", "b", "c"}, // stored
		complete,
	)
	if !reflect.DeepEqual(p.Seen, []string{"b", "c", "d"}) {
		t.Fatalf("Seen = %v", p.Seen)
	}
	if !reflect.DeepEqual(p.New, []string{"d"}) {
		t.Fatalf("New = %v, want [d]", p.New)
	}
	if !reflect.DeepEqual(p.Missing, []string{"a"}) {
		t.Fatalf("Missing = %v, want [a]", p.Missing)
	}
	if !p.Retire {
		t.Fatalf("a complete, non-empty fetch should permit retirement: %s", p.Reason)
	}
}

// ⚠ THE GUARD. A fetch that returned nothing is indistinguishable from a parser
// that has stopped understanding the source. Mutation: delete the `seen == 0`
// branch in retireVerdict → the plan authorises retiring the entire scope.
func TestBuildPlanEmptyFetchRetiresNothing(t *testing.T) {
	for _, fetched := range [][]string{nil, {}, {""}} {
		p := BuildPlan(fetched, []string{"a", "b", "c"}, complete)
		if p.Retire {
			t.Fatalf("fetched=%#v authorised retiring %d stored keys — this is how a DOM change empties a corpus",
				fetched, len(p.Missing))
		}
		// Missing is still POPULATED even though Retire is false: knowing what
		// would have been retired is exactly the signal that a parser broke.
		if len(p.Missing) != 3 {
			t.Fatalf("Missing should still be reported for observability, got %v", p.Missing)
		}
		if p.Reason == "" {
			t.Fatal("a withheld retirement must say why, or the log line is useless")
		}
	}
}

// An incomplete fetch — pagination bailed, an error was swallowed — must not
// read the keys it never saw as deletions.
func TestBuildPlanIncompleteFetchRetiresNothing(t *testing.T) {
	p := BuildPlan([]string{"a"}, []string{"a", "b"}, Options{
		Policy:        RetireOnCompleteFetch,
		FetchComplete: false,
	})
	if p.Retire {
		t.Fatal("a partial fetch authorised retirement — the unseen page would be deleted")
	}
	if len(p.Missing) != 1 {
		t.Fatalf("Missing = %v", p.Missing)
	}
}

// A source whose feed is structurally partial opts out entirely.
func TestBuildPlanRetireNeverPolicy(t *testing.T) {
	p := BuildPlan([]string{"a"}, []string{"a", "b"}, Options{Policy: RetireNever, FetchComplete: true})
	if p.Retire {
		t.Fatal("RetireNever must never authorise retirement")
	}
	if p.Reason == "" {
		t.Fatal("RetireNever should still explain itself")
	}
}

// ⚠ THE CONTROL. Without this, every guard above would be satisfied by a
// function that returns Retire=false unconditionally — which is just as broken,
// in the direction nobody notices.
func TestBuildPlanDoesRetireWhenItShould(t *testing.T) {
	p := BuildPlan([]string{"a"}, []string{"a", "b", "c"}, complete)
	if !p.Retire {
		t.Fatalf("a healthy complete fetch must authorise retirement, got: %s", p.Reason)
	}
	if !reflect.DeepEqual(p.Missing, []string{"b", "c"}) {
		t.Fatalf("Missing = %v, want [b c]", p.Missing)
	}
}

func TestBuildPlanDedupesAndIgnoresEmptyKeys(t *testing.T) {
	p := BuildPlan([]string{"a", "a", "", "b"}, []string{"b", "b"}, complete)
	if !reflect.DeepEqual(p.Seen, []string{"a", "b"}) {
		t.Fatalf("Seen = %v, want [a b] (deduped, empty dropped)", p.Seen)
	}
	if !reflect.DeepEqual(p.New, []string{"a"}) {
		t.Fatalf("New = %v", p.New)
	}
	if len(p.Missing) != 0 {
		t.Fatalf("Missing = %v, want none", p.Missing)
	}
}

// A first sync against an empty store is all-new and retires nothing, and must
// not be mistaken for a broken fetch.
func TestBuildPlanFirstSync(t *testing.T) {
	p := BuildPlan([]string{"a", "b"}, nil, complete)
	if len(p.New) != 2 || len(p.Missing) != 0 {
		t.Fatalf("first sync: New=%v Missing=%v", p.New, p.Missing)
	}
	if !p.Retire {
		t.Fatalf("a non-empty first fetch is healthy: %s", p.Reason)
	}
}

func TestPlanCounts(t *testing.T) {
	seen, added, missing := BuildPlan([]string{"a", "b"}, []string{"b", "c"}, complete).Counts()
	if seen != 2 || added != 1 || missing != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", seen, added, missing)
	}
}
