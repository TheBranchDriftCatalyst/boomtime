package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestLabels_ListSeeded exercises the initial migration seed: ListLabels
// returns the 114 rows the migration inserted, and the row shape decodes
// cleanly (condition JSONB round-trips, kind is one of the four expected
// values, etc.). Non-tautological: the seed count + kind distribution is
// baked into the migration and would flag any drop-or-drift.
func TestLabels_ListSeeded(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	labels, err := d.ListLabels(ctx)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(labels) != 114 {
		t.Fatalf("ListLabels count=%d want 114 (seed drift?)", len(labels))
	}

	// Kind bucketing: must have all four bands with the seed's expected
	// counts.
	byKind := map[string]int{}
	for _, l := range labels {
		byKind[l.Kind]++
	}
	want := map[string]int{"tier": 45, "archetype": 14, "tribe": 7, "meme": 48}
	for k, n := range want {
		if byKind[k] != n {
			t.Errorf("kind=%s count=%d want %d", k, byKind[k], n)
		}
	}

	// Every row must have a non-empty condition JSONB that decodes to an
	// object with a "kind" discriminant — otherwise the FE evaluator would
	// silently no-match at award time.
	for _, l := range labels {
		var probe map[string]any
		if err := json.Unmarshal(l.Condition, &probe); err != nil {
			t.Errorf("%s: condition unmarshal: %v", l.ID, err)
			continue
		}
		if _, ok := probe["kind"]; !ok {
			t.Errorf("%s: condition has no `kind` discriminant: %s", l.ID, string(l.Condition))
		}
	}
}

// TestLabels_UpsertRoundtrip: insert a new label, read it back, then update
// it, read again. Every editable field survives the round trip. Non-
// tautological: JSONB round-trip is the load-bearing property here — the
// evaluator relies on the exact tree shape, and a naive json.Marshal
// re-order in pgx would silently break composed predicates.
func TestLabels_UpsertRoundtrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	id := "test-upsert-label"
	t.Cleanup(func() { _ = d.DeleteLabel(ctx, id) })

	cond := json.RawMessage(`{"kind":"all","of":[{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":5},{"kind":"streak","which":"current","op":">=","days":10}]}`)
	orig := Label{
		ID:              id,
		Kind:            "archetype",
		Label:           "TEST ROUNDTRIP",
		Glyph:           "★",
		Description:     "roundtrip fixture",
		OptimizedPrompt: "cyberpunk emblem",
		Rank:            77,
		Tier:            "",
		Condition:       cond,
	}
	if err := d.UpsertLabel(ctx, orig); err != nil {
		t.Fatalf("first UpsertLabel: %v", err)
	}

	first, err := d.GetLabel(ctx, id)
	if err != nil {
		t.Fatalf("GetLabel: %v", err)
	}
	if first == nil {
		t.Fatal("GetLabel: expected row, got nil")
	}
	// Assert every editable field round-tripped.
	if first.Kind != "archetype" {
		t.Errorf("kind=%q", first.Kind)
	}
	if first.Label != "TEST ROUNDTRIP" {
		t.Errorf("label=%q", first.Label)
	}
	if first.Glyph != "★" {
		t.Errorf("glyph=%q", first.Glyph)
	}
	if first.Description != "roundtrip fixture" {
		t.Errorf("description=%q", first.Description)
	}
	if first.OptimizedPrompt != "cyberpunk emblem" {
		t.Errorf("optimizedPrompt=%q", first.OptimizedPrompt)
	}
	if first.Rank != 77 {
		t.Errorf("rank=%d", first.Rank)
	}
	// Condition JSONB round-trip: the shape MUST still parse into the
	// same nested structure (order of keys in Postgres JSONB is not
	// preserved but structure is). We reparse both sides and deep-compare
	// after re-serialization.
	var got, want any
	if err := json.Unmarshal(first.Condition, &got); err != nil {
		t.Fatalf("re-parse condition: %v", err)
	}
	if err := json.Unmarshal(cond, &want); err != nil {
		t.Fatalf("re-parse want: %v", err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("condition drift: got=%s want=%s", gotJSON, wantJSON)
	}

	// Update: change rank + prompt + condition, upsert, read back.
	newCond := json.RawMessage(`{"kind":"daily-avg","op":">=","hours":8}`)
	updated := orig
	updated.Rank = 999
	updated.OptimizedPrompt = "updated cyberpunk emblem"
	updated.Condition = newCond
	if err := d.UpsertLabel(ctx, updated); err != nil {
		t.Fatalf("second UpsertLabel: %v", err)
	}
	second, err := d.GetLabel(ctx, id)
	if err != nil {
		t.Fatalf("second GetLabel: %v", err)
	}
	if second.Rank != 999 {
		t.Errorf("rank not updated: %d", second.Rank)
	}
	if second.OptimizedPrompt != "updated cyberpunk emblem" {
		t.Errorf("optimizedPrompt not updated: %q", second.OptimizedPrompt)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) && !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("updated_at regressed: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("created_at drifted on update: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
}

// TestLabels_UpsertRejectsBadInput: empty id / empty kind / empty label /
// empty condition all fail — they're structural invariants the FE evaluator
// depends on.
func TestLabels_UpsertRejectsBadInput(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	good := Label{ID: "x", Kind: "archetype", Label: "X", Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`)}
	cases := []struct {
		name string
		mut  func(l *Label)
	}{
		{"empty id", func(l *Label) { l.ID = "" }},
		{"empty kind", func(l *Label) { l.Kind = "" }},
		{"empty label", func(l *Label) { l.Label = "" }},
		{"empty condition", func(l *Label) { l.Condition = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := good
			tc.mut(&bad)
			if err := d.UpsertLabel(ctx, bad); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// TestLabels_CheckConstraintRejectsBadKind: the CHECK constraint on `kind`
// only permits tier/archetype/tribe/meme. Attempting to insert 'bogus'
// must fail at the DB layer even if the Go validation would let it
// through (belt-and-braces).
func TestLabels_CheckConstraintRejectsBadKind(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	bad := Label{ID: "test-bad-kind", Kind: "bogus", Label: "X",
		Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`)}
	err := d.UpsertLabel(ctx, bad)
	if err == nil {
		_ = d.DeleteLabel(ctx, "test-bad-kind")
		t.Fatal("expected CHECK constraint violation on kind='bogus'")
	}
	if !strings.Contains(err.Error(), "labels_kind_check") && !strings.Contains(err.Error(), "check") {
		t.Errorf("expected check constraint error; got %v", err)
	}
}

// TestLabels_DeleteIdempotent: DELETE on a missing id returns nil. Callers
// use this to blindly clean up during regen without an existence check.
func TestLabels_DeleteIdempotent(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	if err := d.DeleteLabel(ctx, "definitely-does-not-exist-xyz"); err != nil {
		t.Errorf("DeleteLabel on missing id: %v", err)
	}
}

// TestLabelGenConfig_Roundtrip: get returns the seeded value (post-manifest
// UPDATE runs the systemPrompt into the singleton row); set updates it,
// re-get returns the new value.
func TestLabelGenConfig_Roundtrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	seeded, err := d.GetGenConfig(ctx)
	if err != nil {
		t.Fatalf("GetGenConfig seeded: %v", err)
	}
	// Restore whatever was there originally so we don't strand the test DB
	// in a modified state between suite runs.
	t.Cleanup(func() { _ = d.SetGenConfig(ctx, seeded) })

	newPrompt := "test system prompt from unit test — do not ship"
	if err := d.SetGenConfig(ctx, newPrompt); err != nil {
		t.Fatalf("SetGenConfig: %v", err)
	}
	got, err := d.GetGenConfig(ctx)
	if err != nil {
		t.Fatalf("GetGenConfig post-set: %v", err)
	}
	if got != newPrompt {
		t.Errorf("systemPrompt=%q want %q", got, newPrompt)
	}
}
