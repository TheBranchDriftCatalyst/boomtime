// Package bookspipeline chains the reading-sync stages into ONE orchestrator so
// a single job (or a single POST) runs the whole ingest → match → pull flow in
// dependency order for a user, instead of the caller enqueuing four kinds by
// hand.
//
// Dependency order (guaranteed by running sequentially):
//
//  1. Audible ingest   — forward-sync the audiobook library
//  2. Kindle ingest    — forward-sync the ebook library
//  3. Hardcover match  — resolve every now-ingested reading_item to a Hardcover
//     book/edition (must run AFTER both ingests so it sees
//     the freshly-added rows)
//  4. Hardcover pull   — reconcile the remote shelf's status/updated_at onto the
//     now-matched linkage (must run AFTER match)
//
// Each step is best-effort: a per-step error is logged and recorded in the
// Summary but does NOT abort the chain. Because the steps run sequentially the
// ordering guarantee holds regardless — match still runs after both ingests and
// pull after match even if an earlier step errored.
//
// The four steps are injected as plain funcs (see Steps) so the orchestrator is
// unit-testable with fakes, without constructing the real audiobooks / books /
// hardcover services. main.go wires the real services in via closures.
package bookspipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
)

// BooksSyncAllKind is the jobs registry kind for the consolidated reading-sync
// orchestrator. An owner-scoped job runs the pipeline for job.Owner; a job with
// no owner fans the pipeline over every user with a connected Amazon device.
const BooksSyncAllKind = "books-sync-all"

// StepFunc is one owner-scoped pipeline stage: it does its work for owner and
// returns a count of what it processed (interpretation is per-stage) plus an
// error. A returned error is captured in the Summary and does not abort the
// chain.
type StepFunc func(ctx context.Context, owner string) (int, error)

// Steps is the injectable set of the four pipeline stages, in dependency order.
// main.go builds these from the already-constructed audiobooks / books /
// hardcover services so there is ONE shared instance set; tests pass fakes.
type Steps struct {
	AudibleSync StepFunc // 1. Audible forward-sync
	KindleSync  StepFunc // 2. Kindle forward-sync
	Match       StepFunc // 3. Hardcover match-unmatched (after both ingests)
	Pull        StepFunc // 4. Hardcover shelf pull (after match)
}

// Summary aggregates what one RunPipeline call did for a single owner. Counts
// are per-stage; Errors collects every per-step failure (empty on a clean run).
type Summary struct {
	AudibleSynced int      `json:"audibleSynced"`
	KindleSynced  int      `json:"kindleSynced"`
	Matched       int      `json:"matched"`
	Pulled        int      `json:"pulled"`
	Errors        []string `json:"errors,omitempty"`
}

// Pipeline is the orchestrator over a fixed Steps set.
type Pipeline struct {
	steps  Steps
	logger *slog.Logger
}

// New builds a Pipeline over steps. A nil logger is tolerated (logging is
// skipped) so tests can omit it.
func New(steps Steps, logger *slog.Logger) *Pipeline {
	return &Pipeline{steps: steps, logger: logger}
}

// RunPipeline runs the four stages IN ORDER for one owner. Every stage runs even
// if an earlier one failed (best-effort); each failure is logged and appended to
// Summary.Errors. It returns a non-nil error only for a caller mistake (empty
// owner) — step failures live in the Summary so the caller can decide whether a
// partial run is a job failure.
func (p *Pipeline) RunPipeline(ctx context.Context, owner string) (Summary, error) {
	var sum Summary
	if owner == "" {
		return sum, fmt.Errorf("bookspipeline: empty owner")
	}

	// The stages, in strict dependency order. Each entry names the stage (for
	// logs + Summary.Errors) and points at the count it feeds.
	stages := []struct {
		name string
		fn   StepFunc
		into *int
	}{
		{"audible-sync", p.steps.AudibleSync, &sum.AudibleSynced},
		{"kindle-sync", p.steps.KindleSync, &sum.KindleSynced},
		{"hardcover-match", p.steps.Match, &sum.Matched},
		{"hardcover-pull", p.steps.Pull, &sum.Pulled},
	}

	// Resolve the job-scoped logger from ctx (job_id/kind/owner attrs) so every
	// pipeline line carries the running job's id in the Admin viewer; fall back to
	// the injected logger off a job (nil-tolerant for tests).
	log := logctx.FromContext(ctx, p.logger)

	for _, st := range stages {
		if st.fn == nil {
			continue // unwired stage — skip, keep ordering
		}
		// Per-step start: a running pipeline shows which stage is live, not one
		// line at the very end.
		if log != nil {
			log.Info("books-sync-all: step start", "step", st.name, "user", owner)
		}
		n, err := st.fn(ctx, owner)
		if err != nil {
			msg := fmt.Sprintf("%s: %v", st.name, err)
			sum.Errors = append(sum.Errors, msg)
			if log != nil {
				log.Warn("books-sync-all: step failed", "step", st.name, "user", owner, "err", err)
			}
			// best-effort: record the count we did get (usually 0) and continue.
		} else if log != nil {
			// Per-step summary on success: the stage's processed count.
			log.Info("books-sync-all: step done", "step", st.name, "user", owner, "count", n)
		}
		*st.into = n
	}

	if log != nil {
		log.Info("books-sync-all: pipeline complete",
			"user", owner,
			"audibleSynced", sum.AudibleSynced,
			"kindleSynced", sum.KindleSynced,
			"matched", sum.Matched,
			"pulled", sum.Pulled,
			"errors", len(sum.Errors),
		)
	}
	return sum, nil
}
