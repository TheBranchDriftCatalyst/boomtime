package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// mkStep returns a StepFunc that records its name into *order, returns n, and
// optionally fails.
func mkStep(name string, n int, err error, order *[]string) StepFunc {
	return func(_ context.Context, _ string) (int, error) {
		*order = append(*order, name)
		return n, err
	}
}

func TestRunPipeline_OrderAndAggregation(t *testing.T) {
	var order []string
	p := New(Steps{
		AudibleSync:       mkStep("audible", 3, nil, &order),
		KindleSync:        mkStep("kindle", 5, nil, &order),
		KindleInsights:    mkStep("insights", 4, nil, &order),
		KindleReconcile:   mkStep("reconcile", 6, nil, &order),
		KindleAnnotations: mkStep("annotations", 14, nil, &order),
		Match:             mkStep("match", 7, nil, &order),
		Pull:              mkStep("pull", 9, nil, &order),
	}, nil)

	sum, err := p.RunPipeline(context.Background(), "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Steps MUST run in dependency order: ingests, kindle-insights (dates the
	// kindle rows), kindle-status-reconcile (honest status after insights), then
	// match, then pull.
	want := []string{"audible", "kindle", "insights", "reconcile", "annotations", "match", "pull"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("step order = %v, want %v", order, want)
	}

	// Counts aggregate into the right Summary fields.
	if sum.AudibleSynced != 3 || sum.KindleSynced != 5 || sum.InsightsBackfilled != 4 || sum.StatusReconciled != 6 ||
		sum.KindleAnnotations != 14 || sum.Matched != 7 || sum.Pulled != 9 {
		t.Fatalf("summary counts = %+v, want {3,5,4,6,14,7,9}", sum)
	}
	if len(sum.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", sum.Errors)
	}
}

func TestRunPipeline_EarlyFailureDoesNotAbort(t *testing.T) {
	var order []string
	boom := errors.New("audible boom")
	p := New(Steps{
		// First step fails — later steps must STILL run (ordering preserved).
		AudibleSync: mkStep("audible", 0, boom, &order),
		KindleSync:  mkStep("kindle", 5, nil, &order),
		Match:       mkStep("match", 7, nil, &order),
		Pull:        mkStep("pull", 9, nil, &order),
	}, nil)

	sum, err := p.RunPipeline(context.Background(), "bob")
	if err != nil {
		t.Fatalf("per-step failure must not surface as a top-level error, got %v", err)
	}

	want := []string{"audible", "kindle", "match", "pull"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("chain aborted early: order = %v, want %v", order, want)
	}

	// The failing step is captured in Errors; the successful ones still aggregate.
	if len(sum.Errors) != 1 {
		t.Fatalf("want 1 captured error, got %v", sum.Errors)
	}
	if sum.Errors[0] != "audible-sync: audible boom" {
		t.Fatalf("error text = %q", sum.Errors[0])
	}
	if sum.AudibleSynced != 0 || sum.KindleSynced != 5 || sum.Matched != 7 || sum.Pulled != 9 {
		t.Fatalf("summary after early failure = %+v, want {0,5,7,9}", sum)
	}
}

func TestRunPipeline_MultipleFailuresCaptured(t *testing.T) {
	var order []string
	p := New(Steps{
		AudibleSync: mkStep("audible", 1, nil, &order),
		KindleSync:  mkStep("kindle", 0, errors.New("kindle down"), &order),
		Match:       mkStep("match", 0, errors.New("match down"), &order),
		Pull:        mkStep("pull", 4, nil, &order),
	}, nil)

	sum, err := p.RunPipeline(context.Background(), "carol")
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("all steps must run, ran %v", order)
	}
	if len(sum.Errors) != 2 {
		t.Fatalf("want 2 captured errors, got %v", sum.Errors)
	}
	if sum.AudibleSynced != 1 || sum.Pulled != 4 {
		t.Fatalf("successful steps must still aggregate: %+v", sum)
	}
}

func TestRunPipeline_EmptyOwner(t *testing.T) {
	p := New(Steps{}, nil)
	if _, err := p.RunPipeline(context.Background(), ""); err == nil {
		t.Fatal("empty owner must return an error")
	}
}

func TestRunPipeline_NilStepsSkipped(t *testing.T) {
	var order []string
	// Only two stages wired; the nil ones must be skipped without panic and
	// ordering of the wired ones preserved.
	p := New(Steps{
		AudibleSync: mkStep("audible", 2, nil, &order),
		Pull:        mkStep("pull", 8, nil, &order),
	}, nil)

	sum, err := p.RunPipeline(context.Background(), "dave")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"audible", "pull"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if sum.AudibleSynced != 2 || sum.Pulled != 8 || sum.KindleSynced != 0 || sum.Matched != 0 {
		t.Fatalf("summary = %+v", sum)
	}
}

// The annotations stage depends on the Kindle ingest having created the
// reading_items rows it annotates. Asserting the INDICES rather than the whole
// slice means a future reorder fails with the real dependency named, instead of
// a diff of seven strings that says nothing about why the order matters.
func TestRunPipeline_AnnotationsRunAfterTheKindleIngest(t *testing.T) {
	var order []string
	p := New(Steps{
		AudibleSync:       mkStep("audible", 1, nil, &order),
		KindleSync:        mkStep("kindle", 1, nil, &order),
		KindleAnnotations: mkStep("annotations", 1, nil, &order),
		Match:             mkStep("match", 1, nil, &order),
	}, nil)
	if _, err := p.RunPipeline(context.Background(), "alice"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idx := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	if idx("annotations") < idx("kindle") {
		t.Fatalf("annotations ran before the kindle ingest (%v) — on a fresh account it would find no books to annotate", order)
	}
	if idx("annotations") > idx("match") {
		t.Fatalf("annotations ran after the Hardcover stages (%v) — it belongs at the end of the Amazon block", order)
	}
}

// FLAG-OFF IS BYTE-IDENTICAL. With the annotations flag off, jobs.go leaves the
// step nil, and the pipeline must then behave exactly as it did before the stage
// existed: same six stages, same order, and a zero annotation count.
func TestRunPipeline_NilAnnotationsStepIsInvisible(t *testing.T) {
	var order []string
	p := New(Steps{
		AudibleSync:       mkStep("audible", 3, nil, &order),
		KindleSync:        mkStep("kindle", 5, nil, &order),
		KindleInsights:    mkStep("insights", 4, nil, &order),
		KindleReconcile:   mkStep("reconcile", 6, nil, &order),
		KindleAnnotations: nil, // flag off
		Match:             mkStep("match", 7, nil, &order),
		Pull:              mkStep("pull", 9, nil, &order),
	}, nil)

	sum, err := p.RunPipeline(context.Background(), "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"audible", "kindle", "insights", "reconcile", "match", "pull"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("with the flag off the order must be the pre-existing one: %v, want %v", order, want)
	}
	if sum.KindleAnnotations != 0 {
		t.Fatalf("annotation count = %d with no step wired", sum.KindleAnnotations)
	}
	if len(sum.Errors) != 0 {
		t.Fatalf("a nil stage must not be an error: %v", sum.Errors)
	}
}
