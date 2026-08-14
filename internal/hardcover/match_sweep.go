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
	Scanned   int // unmatched rows considered
	Matched   int // rows resolved to a Hardcover book + linkage cached
	NoMatch   int // ladder returned no confident hit (left for manual review)
	Skipped   int // a per-row error (match call or link write) — best-effort, sweep continues
	Enriched  int // kindle bare-ASIN rows whose title/author/cover were backfilled via LookupByASIN
	CacheHits int // rows resolved from the GLOBAL match cache (gaka-wzgr) — zero Hardcover API calls
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
		s.logWarn(ctx, "hardcover match: client load failed", "user", owner, "err", err)
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
		// Stop before each per-row Hardcover Match call on cancellation — the match
		// ladder is the expensive, rate-limited part of the sweep.
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Scanned++
		// Periodic progress: a large backlog resolves one rate-limited row at a
		// time (minutes-to-hours for thousands of rows), so emit a heartbeat every
		// ~25 rows so the job reads as ALIVE in the viewer, with matched/nomatch/
		// cachehits so the story is legible (final summary logged after the loop).
		if res.Scanned%25 == 0 {
			s.logInfo(ctx, "hardcover match: scanned", "user", owner,
				"scanned", res.Scanned, "of", len(items),
				"matched", res.Matched, "nomatch", res.NoMatch, "cachehits", res.CacheHits)
		}
		asin := firstNonEmpty(it.ExternalID, it.AmazonASIN)

		// gaka-wzgr — cache-first. A match (ASIN/ISBN13 → book/edition) is an
		// objective fact about a BOOK, so once ANY user has resolved it we can serve
		// it from our own DB with zero Hardcover API calls. Try the global cache
		// under each exact-id key BEFORE spending a live Match. A lookup error never
		// fails the row — we just fall through to the API.
		asinKey := strings.TrimSpace(asin)
		isbnKey := normalizeISBN(it.ISBN)

		var (
			m         MatchResult
			fromCache bool
		)
		if asinKey != "" {
			if cached, ok, lerr := s.DB.LookupHardcoverMatch(ctx, "asin", asinKey); lerr != nil {
				s.logWarn(ctx, "hardcover match: cache lookup failed — falling through to API", "user", owner, "idtype", "asin", "external", asinKey, "err", lerr)
			} else if ok && cached.BookID > 0 {
				m = MatchResult{BookID: cached.BookID, EditionID: cached.EditionID, Slug: cached.Slug, Method: MatchMethod(cached.Method)}
				fromCache = true
			}
		}
		if !fromCache && isbnKey != "" {
			if cached, ok, lerr := s.DB.LookupHardcoverMatch(ctx, "isbn13", isbnKey); lerr != nil {
				s.logWarn(ctx, "hardcover match: cache lookup failed — falling through to API", "user", owner, "idtype", "isbn13", "external", isbnKey, "err", lerr)
			} else if ok && cached.BookID > 0 {
				m = MatchResult{BookID: cached.BookID, EditionID: cached.EditionID, Slug: cached.Slug, Method: MatchMethod(cached.Method)}
				fromCache = true
			}
		}

		if fromCache {
			res.CacheHits++
		} else {
			var merr error
			m, merr = client.Match(ctx, MatchInput{
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
				s.logWarn(ctx, "hardcover match: match failed — leaving unmatched", "user", owner, "source", it.Source, "external", it.ExternalID, "err", merr)
				res.Skipped++
				continue
			}
			if m.Method == MatchNone || m.BookID <= 0 {
				res.NoMatch++
				continue
			}

			// Populate the GLOBAL cache from a confident EXACT-ID hit only. The fuzzy
			// Typesense rung (MatchBySearch) is NEVER cached — a wrong edition would
			// then poison the match for every user (gaka-wzgr caveat). A Put error is
			// best-effort: log it and keep going so the per-user link still gets written.
			switch {
			case m.Method == MatchByASIN && asinKey != "":
				if perr := s.DB.PutHardcoverMatch(ctx, "asin", asinKey, m.BookID, m.EditionID, string(m.Method), m.Slug); perr != nil {
					s.logWarn(ctx, "hardcover match: cache put failed", "user", owner, "idtype", "asin", "external", asinKey, "err", perr)
				}
			case m.Method == MatchByISBN13 && isbnKey != "":
				if perr := s.DB.PutHardcoverMatch(ctx, "isbn13", isbnKey, m.BookID, m.EditionID, string(m.Method), m.Slug); perr != nil {
					s.logWarn(ctx, "hardcover match: cache put failed", "user", owner, "idtype", "isbn13", "external", isbnKey, "err", perr)
				}
			}
		}

		if lerr := s.DB.SetReadingItemHardcoverLink(ctx, owner, it.Source, it.ExternalID, m.BookID, m.EditionID, string(m.Method), m.Slug); lerr != nil {
			s.logWarn(ctx, "hardcover match: link write failed", "user", owner, "source", it.Source, "external", it.ExternalID, "err", lerr)
			res.Skipped++
			continue
		}
		res.Matched++

		// Kindle bare-ASIN rows arrive with a blank title (the ingest had only the
		// ASIN). Now that we've matched, pull display metadata so the Books view can
		// render a real row. Best-effort — a lookup/enrich miss never fails the row.
		if it.Source == kindleReadingSource && strings.TrimSpace(it.Title) == "" && asin != "" {
			if meta, lerr := client.LookupByASIN(ctx, asin); lerr != nil {
				s.logWarn(ctx, "hardcover match: lookup for enrich failed", "user", owner, "asin", asin, "err", lerr)
			} else if meta != nil {
				if n, uerr := s.DB.UpdateReadingItemDisplayMeta(ctx, owner, it.Source, it.ExternalID, meta.Title, meta.Authors, meta.CoverURL); uerr != nil {
					s.logWarn(ctx, "hardcover match: display-meta backfill failed", "user", owner, "asin", asin, "err", uerr)
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
		s.logWarn(ctx, "hardcover match: last_match_at cursor write failed", "user", owner, "err", serr)
	}

	s.logInfo(ctx, "hardcover match: complete",
		"user", owner, "scanned", res.Scanned, "matched", res.Matched,
		"nomatch", res.NoMatch, "skipped", res.Skipped, "enriched", res.Enriched,
		"cachehits", res.CacheHits)
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
