// reconcile.go — the Kindle STATUS reconcile sweep: give every non-read kindle
// book an HONEST status by asking the CDE sidecar whether it has a last-page-read
// record.
//
// WHY this exists: the Cloud Reader /kindle-library feed reports percentageRead=0
// for EVERY book (a known Amazon quirk), so the library ingest (ingest.go) can
// only ever default a fresh row to 'want'. That is a fine baseline but it is not
// the truth — a book the reader has actually opened has an lpr in the CDE sidecar
// even when Cloud Reader shows 0%. This sweep reads that one honest signal:
//
//   - sidecar HAS an lpr (ok=true)  → the book has been opened → status='reading'
//     (+ seed one kindle_reading_positions sample so the forward reading-TIME job
//     immediately has an anchor to compose from).
//   - sidecar 404s   (ok=false)     → no reading state → leave it 'want'.
//
// It runs AFTER the insights ingest (books-kindle-insights), which sets 'read' +
// finished_at for FINISHED books from the Reading-Insights history. So this sweep
// only ever considers genuinely-non-read rows, and SetReadingItemReading's guard
// (WHERE finished=false AND status<>'read') is a belt-and-suspenders promise that
// a finished book is NEVER demoted to 'reading' by its end-of-book lpr.
//
// It is best-effort + idempotent: a per-book fetch/write error logs and continues
// (one bad book never strands the sweep), and a re-run re-polls every candidate —
// SetReadingItemReading is a no-op on an already-'reading' row and the position
// insert dedupes on (owner,asin,sampled_at). The sweep is cancellable (checks
// ctx.Err() before each book) and rate-paced (a small delay between books) since
// it is ~one sidecar call per non-read book — thousands for a large library.
package kindle

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// reconcilePaceDelay throttles the sweep to at most one sidecar call every
// ~50ms so a large library (~2k non-read books) doesn't hammer the CDE host in a
// tight loop. The job-layer concurrency cap (SetConcurrency=1) already serializes
// the sweep fleet-wide; this paces WITHIN one user's run. A var (not a const) so
// a test can zero it.
var reconcilePaceDelay = 50 * time.Millisecond

// ReconcileSummary reports what one ReconcileKindleStatus sweep did.
type ReconcileSummary struct {
	Scanned       int // non-read candidates considered
	MarkedReading int // rows flipped want->reading (the sidecar had an lpr)
	StillWant     int // no lpr (sidecar 404) — honestly left as 'want'
	Seeded        int // position samples upserted from an lpr (reading-time anchor)
	Errors        int // per-book errors (best-effort — logged, sweep continued)
}

// ReconcileKindleStatus is the job-handler body for KindleStatusReconcileKind.
// For every NON-read kindle reading_item of owner it polls the CDE sidecar and
// sets an honest status: 'reading' when a last-page-read record exists (+ seeds a
// position sample), left 'want' when it 404s. It NEVER touches a read/finished
// row (SetReadingItemReading's guard), so re-running it after the insights ingest
// is safe. Returns the summary; a nil error means the sweep ran to completion
// (individual per-book failures are counted in Summary.Errors, not surfaced).
func (s *Service) ReconcileKindleStatus(ctx context.Context, owner string) (ReconcileSummary, error) {
	var res ReconcileSummary

	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return res, err
	}

	items, err := s.DB.ListReadingItems(ctx, owner, source)
	if err != nil {
		return res, err
	}

	// Candidates: non-read kindle books with an ASIN. A read/finished book is
	// excluded here (and refused again by the db guard) — its lpr is just its end
	// position, not evidence it is still being read. A book already 'reading' stays
	// a candidate so its sample gets re-seeded; the status flip is a no-op.
	candidates := make([]db.ReadingItem, 0, len(items))
	for _, it := range items {
		if it.Finished || it.Status == "read" || it.ExternalID == "" {
			continue
		}
		candidates = append(candidates, it)
	}

	now := time.Now().UTC()
	for _, it := range candidates {
		// Cancellable: the Admin cancel button flows through ctx. Stop BEFORE the
		// next sidecar call so a cancel takes effect promptly on a long sweep.
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Scanned++
		// Periodic heartbeat (mirrors the match sweep) so a multi-thousand-book run
		// reads as ALIVE in the job viewer, with the running tallies.
		if res.Scanned%25 == 0 {
			s.logInfo(ctx, "kindle status reconcile: scanned", "user", owner,
				"scanned", res.Scanned, "of", len(candidates),
				"markedReading", res.MarkedReading, "errors", res.Errors)
		}

		pos, at, ok, ferr := s.sidecar.FetchLastPagePosition(ctx, cred, it.ExternalID)
		if ferr != nil {
			// A per-book fetch/parse error is best-effort: log + count + keep going.
			s.logWarn(ctx, "kindle status reconcile: position fetch failed", "user", owner, "asin", it.ExternalID, "err", ferr)
			res.Errors++
			s.reconcilePace(ctx)
			continue
		}
		if !ok {
			// No lpr → the book has never been opened → honestly still a 'want'.
			res.StillWant++
			s.reconcilePace(ctx)
			continue
		}

		// Has an lpr → the book has been opened → honestly 'reading'. The db guard
		// refuses to touch a read/finished row, so a stray finished-book lpr can
		// never demote it; `changed` is false for an already-'reading' row.
		changed, uerr := s.DB.SetReadingItemReading(ctx, owner, source, it.ExternalID)
		if uerr != nil {
			s.logWarn(ctx, "kindle status reconcile: status flip failed", "user", owner, "asin", it.ExternalID, "err", uerr)
			res.Errors++
			s.reconcilePace(ctx)
			continue
		}
		if changed {
			res.MarkedReading++
		}

		// Seed one position sample from the lpr so the forward reading-TIME job
		// (PollReadingTime) has an immediate anchor for this now-'reading' book —
		// the first of the two samples its gap-sum composition needs. `at` is
		// Amazon's own creationTime for the lpr; fall back to poll time if empty.
		// The insert dedupes on (owner,asin,sampled_at), so a re-run seeds nothing.
		if at.IsZero() {
			at = now
		}
		if inserted, ierr := s.DB.InsertKindleReadingPosition(ctx, owner, it.ExternalID, pos, at.UTC()); ierr != nil {
			s.logWarn(ctx, "kindle status reconcile: seed sample failed", "user", owner, "asin", it.ExternalID, "err", ierr)
			res.Errors++
		} else if inserted {
			res.Seeded++
		}

		s.reconcilePace(ctx)
	}

	s.logInfo(ctx, "kindle status reconcile: complete", "user", owner,
		"scanned", res.Scanned,
		"markedReading", res.MarkedReading,
		"stillWant", res.StillWant,
		"seeded", res.Seeded,
		"errors", res.Errors,
	)
	return res, nil
}

// reconcilePace sleeps reconcilePaceDelay between books, aborting early if ctx is
// cancelled so a paced sweep still cancels promptly.
func (s *Service) reconcilePace(ctx context.Context) {
	if reconcilePaceDelay <= 0 {
		return
	}
	t := time.NewTimer(reconcilePaceDelay)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
