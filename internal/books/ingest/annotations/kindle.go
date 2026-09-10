// kindle.go — the Kindle notebook sweep.
//
// Cheap by construction: GET /notebook IS the index of books that have
// annotations, so the sweep is 1 + N requests where N is books-WITH-annotations
// (14 on the probe account), not books-owned (2512). There is nothing to scope
// or paginate through — the endpoint does the scoping.
package annotations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/reconcile"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// SyncKindleAnnotations pulls every annotated book's highlights and notes for
// one owner and returns the number of annotations upserted.
//
// Error posture, which is the whole design:
//
//   - Loading the credential, exchanging cookies, or listing the index fails ->
//     return the error. Nothing can proceed.
//   - ONE book fails to parse -> log, count, continue, and tombstone NOTHING for
//     that book. A single moved page never strands the sweep.
//   - EVERY book fails to parse -> return an error. A sweep that fetched 14
//     books and understood none of them is a broken parser, not an empty
//     account, and reporting it as success is how a corpus silently empties.
func (s *Service) SyncKindleAnnotations(ctx context.Context, owner string) (int, error) {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return 0, err
	}

	// NOTE: no noteCredentialOutcome here, deliberately — see the package doc.
	// ExchangeWebsiteCookies refuses non-US credentials, and that refusal must
	// not be recorded as a bad Amazon device.
	cookies, err := s.notebook.ExchangeWebsiteCookies(ctx, cred)
	if err != nil {
		return 0, fmt.Errorf("kindle annotations: website cookies: %w", err)
	}

	books, err := s.notebook.ListAnnotatedBooks(ctx, cookies)
	if err != nil {
		return 0, fmt.Errorf("kindle annotations: library index: %w", err)
	}
	if len(books) == 0 {
		s.logInfo(ctx, "kindle annotations: no annotated books", "user", owner)
		return 0, nil
	}
	s.logInfo(ctx, "kindle annotations: sweeping", "user", owner, "books", len(books))

	var (
		total      int
		shapeFails int
		otherFails int
	)
	for i, book := range books {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if i > 0 && kindleAnnotationPace > 0 {
			time.Sleep(kindleAnnotationPace)
		}

		n, err := s.syncOneBook(ctx, cookies, owner, book.ASIN)
		switch {
		case errors.Is(err, amazon.ErrNotebookShapeUnknown):
			shapeFails++
			metrics.BookAnnotationParseFailuresTotal.WithLabelValues(source, "shape-unknown").Inc()
			s.logWarn(ctx, "kindle annotations: page shape not understood", "user", owner, "asin", book.ASIN, "err", err)
		case err != nil:
			otherFails++
			metrics.BookAnnotationParseFailuresTotal.WithLabelValues(source, "transport").Inc()
			s.logWarn(ctx, "kindle annotations: book failed", "user", owner, "asin", book.ASIN, "err", err)
		default:
			total += n
		}
	}

	// The all-books-failed guard. If NOT ONE book came back usable, the sweep has
	// learned nothing and must not report success — otherwise it returns
	// (0, nil), the caller records a clean run, and the corpus silently stops
	// growing. That is the same silent-zero this whole design exists to prevent.
	//
	// It counts BOTH failure kinds on purpose. An earlier version tested only
	// shapeFails, which left a hole exactly as bad as the one it was guarding:
	// every book failing on transport — expired cookies, a 500, rate limiting —
	// left shapeFails at 0, so the guard never fired and the sweep reported
	// success having written nothing.
	//
	// The two are still distinguished in the message, because they call for
	// different responses: a parse failure means the DOM moved and the parser
	// needs work, while a transport failure means the credential or the endpoint
	// needs attention.
	if failed := shapeFails + otherFails; failed == len(books) {
		switch {
		case shapeFails == len(books):
			return total, fmt.Errorf("kindle annotations: every one of %d books failed to parse — the notebook DOM has moved: %w",
				len(books), amazon.ErrNotebookShapeUnknown)
		case otherFails == len(books):
			return total, fmt.Errorf("kindle annotations: every one of %d books failed to fetch — the cookie jar or the endpoint is broken", len(books))
		default:
			return total, fmt.Errorf("kindle annotations: all %d books failed (%d unparseable, %d unreachable)",
				len(books), shapeFails, otherFails)
		}
	}

	s.logInfo(ctx, "kindle annotations: sweep complete",
		"user", owner, "books", len(books), "annotations", total,
		"shape_failures", shapeFails, "other_failures", otherFails)
	return total, nil
}

// syncOneBook fetches, upserts and reconciles one book's annotations.
func (s *Service) syncOneBook(ctx context.Context, cookies map[string]string, owner, asin string) (int, error) {
	anns, err := s.notebook.FetchBookAnnotations(ctx, cookies, asin)
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	seen := make([]string, 0, len(anns))
	var wrote int
	for _, a := range anns {
		row, ok, verr := buildFromKindle(owner, asin, a, now)
		if verr != nil {
			// The event violated its own registered declaration. That is a bug in
			// this package, not bad input from Amazon, so it is surfaced loudly
			// rather than skipped — the registry exists to catch exactly this.
			return wrote, verr
		}
		if !ok {
			s.logWarn(ctx, "kindle annotations: unkeyable annotation dropped", "owner", owner, "asin", asin)
			continue
		}
		if _, uerr := s.DB.UpsertBookAnnotation(ctx, row); uerr != nil {
			return wrote, uerr
		}
		metrics.BookAnnotationsTotal.WithLabelValues(source, row.Kind).Inc()
		seen = append(seen, row.AnnotationKey)
		wrote++
	}

	// Reconcile deletions through the shared kernel rather than an inline rule.
	// FetchBookAnnotations followed every pagination token or returned an error,
	// so reaching here means the fetch was complete for this book.
	stored, kerr := s.DB.ListBookAnnotationKeys(ctx, owner, source, asin)
	if kerr != nil {
		return wrote, kerr
	}
	plan := reconcile.BuildPlan(seen, stored, reconcile.Options{
		Policy:        reconcile.RetireOnCompleteFetch,
		FetchComplete: true,
	})
	if !plan.Retire {
		// Saying WHY nothing was retired is the difference between a healthy
		// empty book and a parser that has quietly stopped understanding the page.
		if len(plan.Missing) > 0 {
			s.logWarn(ctx, "kindle annotations: retirement withheld",
				"owner", owner, "asin", asin, "would_retire", len(plan.Missing), "reason", plan.Reason)
		}
		return wrote, nil
	}
	if len(plan.Missing) == 0 {
		return wrote, nil
	}
	// The accessor keeps its own empty-fetch guard as defence in depth: the
	// kernel decides policy, the storage layer refuses the catastrophic case
	// regardless of what any caller decided.
	if _, terr := s.DB.TombstoneMissingBookAnnotations(ctx, owner, source, asin, plan.Seen); terr != nil {
		return wrote, terr
	}
	s.logInfo(ctx, "kindle annotations: retired missing", "owner", owner, "asin", asin, "retired", len(plan.Missing))
	return wrote, nil
}
