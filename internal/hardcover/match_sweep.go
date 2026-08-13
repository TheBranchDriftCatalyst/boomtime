package hardcover

import (
	"context"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// match_sweep.go — the EXPLICIT `hardcover-match` pipeline stage. In the
// catalyst-books flow (backfill → match → sync) this is the middle step: after an
// ingest has upserted bare reading_items, this sweep resolves every still-unmatched
// row to a Hardcover book_id/edition_id via the read-only match ladder (match.go)
// and caches the linkage (reading_items.hardcover_*). It is READ-ONLY against
// Hardcover — Match + LookupByASIN are `editions`/`search` queries that pass the
// dry-run gate cleanly — so it is safe to run while writes are fail-safe-disabled.
//
// Splitting match into its own stage (rather than folding it into ingest) means a
// user who connects Hardcover AFTER ingesting Kindle/Audible rows can re-resolve
// the backlog on demand, and the match cost (the expensive ladder) is isolated on
// its own concurrency-capped queue that shares Hardcover's global rate budget.

// HardcoverMatchKind is the catalyst-go-jobs kind for the explicit match sweep.
// Owner-scoped (needs the user's token); registered + concurrency-capped in
// main.go only inside the BooksEnabled block.
const HardcoverMatchKind = "hardcover-match"

// matchStateSource is the book_sync_state.source tag the match sweep records its
// last_match_at cursor under. The sweep is cross-source (it resolves both audible
// and kindle unmatched rows), so it uses a dedicated 'hardcover' row rather than a
// per-ingest-source ('kindle'/'audible') one — consistent with the pull's
// reading_activity source tag.
const matchStateSource = "hardcover"

// kindleReadingSource is the reading_items.source value written by the Kindle
// ingest. The sweep enriches only these rows (bare ASIN, blank title) via
// LookupByASIN; audible rows already carry full library metadata.
const kindleReadingSource = "kindle"

// MatchSweepResult reports what one MatchUnmatched sweep did. (Named distinctly
// from match.go's per-row MatchResult, which this file's counters aggregate.)
type MatchSweepResult struct {
	Scanned  int // unmatched rows considered
	Matched  int // rows resolved to a Hardcover book + linkage cached
	NoMatch  int // ladder returned no confident hit (left for manual review)
	Skipped  int // a per-row error (match call or link write) — best-effort, sweep continues
	Enriched int // kindle bare-ASIN rows whose title/author/cover were backfilled via LookupByASIN
}

// matcher is the narrow, read-only Hardcover client surface the sweep needs.
// *Client satisfies it; tests inject a fake so MatchUnmatched exercises without a
// network (see match_sweep_test.go).
type matcher interface {
	Match(ctx context.Context, in MatchInput) (MatchResult, error)
	LookupByASIN(ctx context.Context, asin string) (*BookMeta, error)
}

// MatchUnmatched runs the explicit match stage for owner: load the user's
// Hardcover client, then resolve every still-unmatched reading_item. It is a
// no-op (zero result, nil error) when the user has not connected Hardcover. On a
// bad token it flips the stored key status to invalid (mirroring the pull/push) so
// the settings UI prompts a re-paste.
func (s *SyncService) MatchUnmatched(ctx context.Context, owner string) (MatchSweepResult, error) {
	var res MatchSweepResult
	if s.Store == nil {
		return res, nil
	}
	client, ok, err := s.Store.ClientForUser(ctx, owner)
	if err != nil {
		s.logWarn("hardcover match: client load failed", "user", owner, "err", err)
		return res, err
	}
	if !ok {
		return res, nil // user hasn't connected Hardcover — nothing to match
	}
	return s.matchWith(ctx, owner, client)
}

// matchWith is the injectable core of MatchUnmatched (tests pass a fake matcher).
// Best-effort per row: one row's match/link failure is counted as Skipped and the
// sweep continues — a single bad row must not strand the rest of the backlog.
func (s *SyncService) matchWith(ctx context.Context, owner string, client matcher) (MatchSweepResult, error) {
	var res MatchSweepResult
	items, err := s.DB.ListUnmatchedReadingItems(ctx, owner)
	if err != nil {
		return res, err
	}

	for _, it := range items {
		res.Scanned++
		asin := firstNonEmpty(it.ExternalID, it.AmazonASIN)

		m, merr := client.Match(ctx, MatchInput{
			ASIN:   asin,
			ISBN13: it.ISBN,
			Title:  it.Title,
			Author: it.Authors,
		})
		if merr != nil {
			// A bad token / rate-limit is worth reacting to (and aborting on), the
			// same way the pull does — retrying every row would only burn the budget.
			if merr == ErrBadToken || merr == ErrRateLimited {
				s.onError(ctx, owner, "match", merr)
				return res, merr
			}
			s.logWarn("hardcover match: match failed — leaving unmatched", "user", owner, "source", it.Source, "external", it.ExternalID, "err", merr)
			res.Skipped++
			continue
		}
		if m.Method == MatchNone || m.BookID <= 0 {
			res.NoMatch++
			continue
		}

		if lerr := s.DB.SetReadingItemHardcoverLink(ctx, owner, it.Source, it.ExternalID, m.BookID, m.EditionID, string(m.Method)); lerr != nil {
			s.logWarn("hardcover match: link write failed", "user", owner, "source", it.Source, "external", it.ExternalID, "err", lerr)
			res.Skipped++
			continue
		}
		res.Matched++

		// Kindle bare-ASIN rows arrive with a blank title (the ingest had only the
		// ASIN). Now that we've matched, pull display metadata so the Books view can
		// render a real row. Best-effort — a lookup/enrich miss never fails the row.
		if it.Source == kindleReadingSource && strings.TrimSpace(it.Title) == "" && asin != "" {
			if meta, lerr := client.LookupByASIN(ctx, asin); lerr != nil {
				s.logWarn("hardcover match: lookup for enrich failed", "user", owner, "asin", asin, "err", lerr)
			} else if meta != nil {
				if n, uerr := s.DB.UpdateReadingItemDisplayMeta(ctx, owner, it.Source, it.ExternalID, meta.Title, meta.Authors, meta.CoverURL); uerr != nil {
					s.logWarn("hardcover match: display-meta backfill failed", "user", owner, "asin", asin, "err", uerr)
				} else if n > 0 {
					res.Enriched++
				}
			}
		}
	}

	// Advance the match cursor after a completed sweep (best-effort — the linkage
	// writes above are the primary job; a cursor-write miss just re-scans next run,
	// which is idempotent since matched rows drop out of ListUnmatchedReadingItems).
	now := time.Now().UTC()
	if serr := s.DB.SetBookSyncState(ctx, db.BookSyncState{
		Owner: owner, Source: matchStateSource, LastMatchAt: &now,
	}); serr != nil {
		s.logWarn("hardcover match: last_match_at cursor write failed", "user", owner, "err", serr)
	}

	s.logInfo("hardcover match: complete",
		"user", owner, "scanned", res.Scanned, "matched", res.Matched,
		"nomatch", res.NoMatch, "skipped", res.Skipped, "enriched", res.Enriched)
	return res, nil
}

// firstNonEmpty returns the first non-blank (trimmed) argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
