package hardcover

import (
	"context"
	"testing"
)

// Guards the dry-run safety gate: mutations must never reach the network when
// dry-run is on (the fail-safe default), while queries pass through. Non-
// tautological — fails if the gate is removed or mis-classifies operations.

func TestIsMutation(t *testing.T) {
	cases := map[string]bool{
		"mutation UpsertUserBook($o: X!) { insert_user_book }": true,
		"  mutation X { y }":                                   true,
		"query { me { id } }":                                  false,
		"{ me { id } }":                                        false,
		"query Editions($a: String!) { editions }":            false,
	}
	for doc, want := range cases {
		if got := isMutation(doc); got != want {
			t.Errorf("isMutation(%q) = %v, want %v", doc, got, want)
		}
	}
}

func TestNewClientDryRunDefaultsOn(t *testing.T) {
	// Fail-safe: an unconfigured client blocks writes.
	if c := NewClient("tok"); !c.DryRun() {
		t.Fatal("dry-run must default ON for an unconfigured client")
	}
}

func TestGraphqlBlocksMutationInDryRun(t *testing.T) {
	c := NewClient("tok") // dry-run on by default
	var out struct{ InsertUserBook struct{ ID int64 } }
	// A blocked mutation short-circuits BEFORE any network call (the endpoint is
	// real; if the gate were absent this would attempt a POST). It returns nil
	// (simulated success) and leaves out zero-valued.
	err := c.graphql(context.Background(), "mutation M { insert_user_book }",
		map[string]any{"bookId": 42}, &out)
	if err != nil {
		t.Fatalf("blocked mutation must return nil, got %v", err)
	}
	if out.InsertUserBook.ID != 0 {
		t.Fatalf("blocked mutation must leave out zero-valued, got id=%d", out.InsertUserBook.ID)
	}
}

func TestSetDryRunOverride(t *testing.T) {
	c := NewClient("tok").SetDryRun(false)
	if c.DryRun() {
		t.Fatal("SetDryRun(false) should disable dry-run on this client")
	}
}
