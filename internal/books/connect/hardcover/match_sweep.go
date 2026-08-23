package hardcover

import (
	"context"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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

// matchRetryWindow is how long a no-match row is skipped before the sweep retries
// it (migration 00071 negative cache). A row the ladder proved has no Hardcover
// book is stamped match_attempted_at; the next sweep within this window excludes
// it — sparing the expensive fuzzy tail — but after the window it is tried once
// more in case Hardcover added the book. The on-demand force-rematch
// (MatchUnmatched's force arg / MatchPayload.Force) ignores this window entirely.
const matchRetryWindow = 30 * 24 * time.Hour

// MatchPayload is the optional job payload for the hardcover-match kind. Force
// makes the sweep ignore the negative-cache window (the on-demand force-rematch,
// PART 3) — it loads the FULL unmatched worklist instead of the windowed one. An
// absent/empty payload defaults Force=false (the scheduled/normal sweep). The
// LOCAL shelf rung already runs regardless of the window, so Force only changes
// which rows reach the exact-id + fuzzy phases.
type MatchPayload struct {
	Force bool `json:"force"`
}

// shelfMatchFloor / shelfMatchMargin are the shelf-rung acceptance bar: a row is
// linked to its best shelf candidate IFF the best score clears the floor AND beats
// the runner-up by the margin. The margin guards against a personal shelf's series
// clustering (don't link Book 2 for Book 1). Stricter than the Typesense fuzzy
// floor (0.6) because a false shelf link is silently wrong, not a visible miss.
const (
	shelfMatchFloor  = 0.75
	shelfMatchMargin = 0.10
)

// MatchSweepResult reports what one MatchUnmatched sweep did. (Named distinctly
// from match.go's per-row MatchResult, which this file's counters aggregate.)
type MatchSweepResult struct {
	Scanned   int // unmatched rows considered
	Matched   int // rows resolved to a Hardcover book + linkage cached
	NoMatch   int // ladder returned no confident hit (left for manual review)
	Skipped   int // a per-row error (match call or link write) — best-effort, sweep continues
	Enriched  int // kindle bare-ASIN rows whose title/author/cover were backfilled via LookupByASIN
	CacheHits int // rows resolved from the GLOBAL match cache (boom-wzgr) — zero Hardcover API calls
	BatchHits int // rows resolved by the BATCHED exact-id rung (editions _in) — many rows per request
	ShelfHits int // rows resolved by the LOCAL shelf-match rung (owner's own Hardcover shelf) — zero API
}

// matcher is the narrow, read-only Hardcover client surface the sweep needs.
// *Client satisfies it; tests inject a fake so MatchUnmatched exercises without a
// network (see match_sweep_test.go). editionsByField is the batch exact-id rung —
// unexported because it is an intra-package seam (same package as *Client).
type matcher interface {
	Match(ctx context.Context, in MatchInput) (MatchResult, error)
	LookupByASIN(ctx context.Context, asin string) (*BookMeta, error)
	editionsByField(ctx context.Context, field string, values []string) (map[string]hcEdition, error)
}

// MatchUnmatched runs the explicit match stage for owner: load the user's
// Hardcover client, then resolve every still-unmatched reading_item. It is a
// no-op (zero result, nil error) when the user has not connected Hardcover. On a
// bad token it flips the stored key status to invalid (mirroring the pull/push) so
// the settings UI prompts a re-paste.
// force ignores the negative-cache window (loads the full unmatched worklist for
// the exact-id + fuzzy phases) — the on-demand force-rematch. The LOCAL shelf rung
// runs regardless of force.
func (s *SyncService) MatchUnmatched(ctx context.Context, owner string, force bool) (MatchSweepResult, error) {
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
	return s.matchWith(ctx, owner, client, force)
}

// matchRow carries one candidate reading_item plus the two exact-id keys derived
// once (its ASIN and its normalized ISBN-13) so the phases don't re-derive them.
type matchRow struct {
	it      db.ReadingItem
	asin    string // firstNonEmpty(external_id, amazon_asin), trimmed — "" when none
	isbnKey string // normalizeISBN(isbn) — "" when none
}

// matchWith is the injectable core of MatchUnmatched (tests pass a fake matcher).
// It runs in PHASES so the exact-id rungs — the bulk of the backlog — collapse
// into a few batched requests instead of one-per-row, WITHOUT touching Hardcover's
// 1-req/s ceiling or the match quality (only exact-id hits are cached; each row is
// still tried once). Best-effort per row: one row's link failure is Skipped and
// the sweep continues. All phases honor ctx cancellation and abort the whole sweep
// on ErrBadToken/ErrRateLimited (mirroring the pull — retrying would only burn the
// budget).
//
//	Phase 0   load candidates (unmatched, minus recently no-matched — the neg cache;
//	          force loads the FULL set, ignoring the window)
//	Phase 1   GLOBAL cache, zero API — link every already-resolved id
//	Phase 2   BATCH exact-id (editions _in) — ASINs then ISBNs, many rows per request
//	Phase 2.5 LOCAL shelf-match (zero API) — score the still-unmatched rows against
//	          the owner's OWN mirrored Hardcover shelf; runs on the negative-cache-
//	          EXEMPT full set so a newly-shelved book matches on the next pull
//	Phase 3   per-row FUZZY (Typesense) — the rate-limited tail; no-match stamps the
//	          attempt cache so the NEXT sweep skips it (the real repeat-sweep win)
func (s *SyncService) matchWith(ctx context.Context, owner string, client matcher, force bool) (MatchSweepResult, error) {
	var res MatchSweepResult

	// Phase 0 — candidate load. Normally exclude rows the ladder recently proved have
	// no Hardcover book (migration 00071 negative cache): a no-match is retried at
	// most once per matchRetryWindow, so a repeat sweep skips the fuzzy tail it
	// already walked. force loads the FULL unmatched set (no window) — the on-demand
	// force-rematch. Either way the LOCAL shelf pass below runs on the full set.
	var items []db.ReadingItem
	var err error
	if force {
		items, err = s.DB.ListUnmatchedReadingItems(ctx, owner)
	} else {
		retryBefore := time.Now().UTC().Add(-matchRetryWindow)
		items, err = s.DB.ListUnmatchedReadingItemsForMatch(ctx, owner, retryBefore)
	}
	if err != nil {
		return res, err
	}
	res.Scanned = len(items)
	rows := make([]*matchRow, 0, len(items))
	for i := range items {
		it := items[i]
		rows = append(rows, &matchRow{
			it:      it,
			asin:    firstNonEmpty(it.ExternalID, it.AmazonASIN),
			isbnKey: normalizeISBN(it.ISBN),
		})
	}

	// Phase 1 — GLOBAL cache (boom-wzgr), zero Hardcover API calls. A match is an
	// objective fact about a BOOK, so once ANY user resolved an ASIN/ISBN13 we serve
	// it from our own DB. Everything not served here falls to the batch rung.
	var misses []*matchRow
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if m, ok := s.cacheLookup(ctx, owner, r); ok {
			res.CacheHits++
			s.linkAndEnrich(ctx, owner, r, m, client, &res)
			continue
		}
		misses = append(misses, r)
	}
	s.logInfo(ctx, "hardcover match: cache phase done", "user", owner,
		"scanned", res.Scanned, "cachehits", res.CacheHits, "remaining", len(misses))

	// Phase 2 — BATCH exact-id. ASINs first, then the STILL-unmatched rows' ISBN-13s.
	// Each chunk of ~100 ids is a SINGLE request (editions _in), so N rows resolve in
	// ceil(N/100) requests instead of N. Both rungs cache the exact-id hit for the
	// next user + link it here.
	afterAsin, err := s.batchExact(ctx, owner, client, "asin", "asin", MatchByASIN, misses, &res)
	if err != nil {
		return res, err
	}
	afterIsbn, err := s.batchExact(ctx, owner, client, "isbn_13", "isbn13", MatchByISBN13, afterAsin, &res)
	if err != nil {
		return res, err
	}
	if res.BatchHits > 0 || len(afterIsbn) > 0 {
		s.logInfo(ctx, "hardcover match: batch phase done", "user", owner,
			"batchhits", res.BatchHits, "remaining", len(afterIsbn))
	}

	// Phase 2.5 — LOCAL shelf-match (zero Hardcover API). Score every still-unmatched
	// row against the owner's OWN mirrored Hardcover shelf (migration 00074). This is
	// negative-cache EXEMPT — it loads the FULL unmatched set (not the windowed one),
	// so a book the user just shelved auto-matches on the next pull without a
	// force-rematch. It runs AFTER the batch phase (which set hardcover_book_id on its
	// hits, dropping them from the full set) and returns the rows it resolved so the
	// fuzzy tail below skips them (no wasted Typesense call / conflicting link).
	shelfResolved := s.shelfMatchPhase(ctx, owner, client, &res)

	// Phase 3 — per-row FUZZY (Typesense), the rate-limited tail. These rows have
	// EXHAUSTED exact-id (both batch rungs missed), so we pass only title/author —
	// the ladder skips the empty asin/isbn rungs and issues at most one search. A
	// fuzzy hit is NEVER cached (a wrong edition would poison every user); a no-match
	// stamps the attempt cache so the next sweep skips this row until the window.
	for i, r := range afterIsbn {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		// Skip a row the LOCAL shelf rung already linked (it may still be in this
		// windowed worklist) — don't spend a fuzzy call on an already-matched row.
		if _, done := shelfResolved[rowKey(r.it.Source, r.it.ExternalID)]; done {
			continue
		}
		if (i+1)%25 == 0 {
			s.logInfo(ctx, "hardcover match: fuzzy scanning", "user", owner,
				"processed", i+1, "of", len(afterIsbn),
				"matched", res.Matched, "nomatch", res.NoMatch)
		}
		m, merr := client.Match(ctx, MatchInput{Title: r.it.Title, Author: r.it.Authors})
		if merr != nil {
			if merr == ErrBadToken || merr == ErrRateLimited {
				s.onError(ctx, owner, "match", merr)
				return res, merr
			}
			s.logWarn(ctx, "hardcover match: match failed — leaving unmatched", "user", owner, "source", r.it.Source, "external", r.it.ExternalID, "err", merr)
			res.Skipped++
			continue
		}
		if m.Method == MatchNone || m.BookID <= 0 {
			res.NoMatch++
			// Negative cache (migration 00071): remember we tried so the next sweep
			// skips the fuzzy cost until the retry window elapses. Best-effort.
			if serr := s.DB.SetReadingItemMatchAttempted(ctx, owner, r.it.Source, r.it.ExternalID); serr != nil {
				s.logWarn(ctx, "hardcover match: attempt-stamp failed", "user", owner, "source", r.it.Source, "external", r.it.ExternalID, "err", serr)
			}
			continue
		}
		// Fuzzy resolution links the per-user row but is NEVER written to the global
		// cache (boom-wzgr caveat).
		s.linkAndEnrich(ctx, owner, r, m, client, &res)
	}

	// Advance the match cursor after a completed sweep (best-effort — the linkage
	// writes above are the primary job; a cursor-write miss just re-scans next run,
	// which is idempotent since matched rows drop out of the worklist).
	now := time.Now().UTC()
	if serr := s.DB.SetBookSyncState(ctx, db.BookSyncState{
		Owner: owner, Source: matchStateSource, LastMatchAt: &now,
	}); serr != nil {
		s.logWarn(ctx, "hardcover match: last_match_at cursor write failed", "user", owner, "err", serr)
	}

	s.logInfo(ctx, "hardcover match: complete",
		"user", owner, "scanned", res.Scanned, "matched", res.Matched,
		"nomatch", res.NoMatch, "skipped", res.Skipped, "enriched", res.Enriched,
		"cachehits", res.CacheHits, "batchhits", res.BatchHits, "shelfhits", res.ShelfHits)
	return res, nil
}

// rowKey identifies a reading_item by its (source, external_id) — the composite the
// shelf pass tags a resolved row under so the fuzzy tail can skip it.
func rowKey(source, externalID string) string { return source + "\x00" + externalID }

// shelfMatchPhase is the LOCAL shelf-match rung (Phase 2.5). It loads the owner's
// whole mirrored Hardcover shelf ONCE, then scores every still-unmatched
// reading_item against it (reusing scoreCandidate — title Jaccard + author bonus).
// A row is linked to its best shelf candidate IFF it clears shelfMatchFloor AND
// beats the runner-up by shelfMatchMargin. On accept it writes the per-user link
// (edition 0/NULL — the status push works off book_id) and promotes the resolution
// to the GLOBAL cache under the row's exact-id key when it has one (method "shelf",
// so the provenance is auditable). It is negative-cache EXEMPT: it loads the FULL
// unmatched set so a newly-shelved book matches even when match_attempted_at is
// recent. Best-effort throughout — a shelf-load or link miss never fails the sweep.
// Returns the set of rowKeys it resolved so the fuzzy tail skips them.
func (s *SyncService) shelfMatchPhase(ctx context.Context, owner string, client matcher, res *MatchSweepResult) map[string]struct{} {
	resolved := map[string]struct{}{}

	shelf, err := s.DB.ListHardcoverShelf(ctx, owner)
	if err != nil {
		s.logWarn(ctx, "hardcover match: shelf load failed — skipping shelf rung", "user", owner, "err", err)
		return resolved
	}
	if len(shelf) == 0 {
		return resolved // nothing shelved locally → no candidates
	}

	// Full unmatched set (no window). Batch-resolved rows already carry
	// hardcover_book_id and drop out here, so we never re-score them.
	items, err := s.DB.ListUnmatchedReadingItems(ctx, owner)
	if err != nil {
		s.logWarn(ctx, "hardcover match: unmatched load for shelf rung failed", "user", owner, "err", err)
		return resolved
	}

	for i := range items {
		if err := ctx.Err(); err != nil {
			return resolved
		}
		it := items[i]
		entry, score, ok := bestShelfMatch(MatchInput{Title: it.Title, Author: it.Authors}, shelf)
		if !ok {
			continue
		}
		r := &matchRow{
			it:      it,
			asin:    firstNonEmpty(it.ExternalID, it.AmazonASIN),
			isbnKey: normalizeISBN(it.ISBN),
		}
		m := MatchResult{BookID: entry.BookID, Slug: entry.Slug, Method: MatchByShelf, Confidence: score}
		if !s.linkAndEnrich(ctx, owner, r, m, client, res) {
			continue // link write failed → counted Skipped, leave for a later sweep
		}
		res.ShelfHits++
		resolved[rowKey(it.Source, it.ExternalID)] = struct{}{}

		// Promote to the GLOBAL cache under the row's exact-id key when it has one
		// (method "shelf" — distinguishes it from an exact-id hit). Only keyed on a
		// present asin/isbn; a title-only row has no cache key. Best-effort.
		if idType, key := shelfCacheKey(r); key != "" {
			if perr := s.DB.PutHardcoverMatch(ctx, idType, key, m.BookID, m.EditionID, string(MatchByShelf), m.Slug); perr != nil {
				s.logWarn(ctx, "hardcover match: shelf cache put failed", "user", owner, "idtype", idType, "external", key, "err", perr)
			}
		}
	}
	if res.ShelfHits > 0 {
		s.logInfo(ctx, "hardcover match: shelf phase done", "user", owner, "shelfhits", res.ShelfHits, "shelfsize", len(shelf))
	}
	return resolved
}

// shelfCacheKey returns the global-cache (id_type, external_id) a shelf-matched row
// should be cached under: its ASIN first, else its normalized ISBN-13. ("", "") when
// the row is title-only (no exact-id key to cache against).
func shelfCacheKey(r *matchRow) (idType, key string) {
	if r.asin != "" {
		return "asin", r.asin
	}
	if r.isbnKey != "" {
		return "isbn13", r.isbnKey
	}
	return "", ""
}

// bestShelfMatch scores in against every shelf entry (reusing scoreCandidate's
// title-Jaccard + author bonus) and returns the best entry IFF it clears the
// shelf-match bar: best score >= shelfMatchFloor AND it beats the runner-up by >=
// shelfMatchMargin. ok=false when nothing clears the bar (below the floor, an
// ambiguous top pair, or a zero-id best). A single-entry shelf has no runner-up, so
// only the floor applies.
func bestShelfMatch(in MatchInput, shelf []db.ShelfEntry) (db.ShelfEntry, float64, bool) {
	var best db.ShelfEntry
	bestScore, runnerScore := -1.0, -1.0
	for _, e := range shelf {
		sc := scoreCandidate(in, searchCandidate{BookID: e.BookID, Title: e.Title, Authors: []string{e.Author}})
		switch {
		case sc > bestScore:
			runnerScore = bestScore
			best, bestScore = e, sc
		case sc > runnerScore:
			runnerScore = sc
		}
	}
	if bestScore < shelfMatchFloor || best.BookID <= 0 {
		return db.ShelfEntry{}, 0, false
	}
	if runnerScore >= 0 && bestScore-runnerScore < shelfMatchMargin {
		return db.ShelfEntry{}, 0, false // ambiguous — a clustered series, don't guess
	}
	return best, bestScore, true
}

// cacheLookup serves a row from the GLOBAL match cache under its exact-id keys
// (asin then isbn13). ok=false falls through to the live rungs. A lookup error
// never fails the row — it is logged and treated as a miss.
func (s *SyncService) cacheLookup(ctx context.Context, owner string, r *matchRow) (MatchResult, bool) {
	if r.asin != "" {
		if cached, ok, lerr := s.DB.LookupHardcoverMatch(ctx, "asin", r.asin); lerr != nil {
			s.logWarn(ctx, "hardcover match: cache lookup failed — falling through", "user", owner, "idtype", "asin", "external", r.asin, "err", lerr)
		} else if ok && cached.BookID > 0 {
			return MatchResult{BookID: cached.BookID, EditionID: cached.EditionID, Slug: cached.Slug, Method: MatchMethod(cached.Method)}, true
		}
	}
	if r.isbnKey != "" {
		if cached, ok, lerr := s.DB.LookupHardcoverMatch(ctx, "isbn13", r.isbnKey); lerr != nil {
			s.logWarn(ctx, "hardcover match: cache lookup failed — falling through", "user", owner, "idtype", "isbn13", "external", r.isbnKey, "err", lerr)
		} else if ok && cached.BookID > 0 {
			return MatchResult{BookID: cached.BookID, EditionID: cached.EditionID, Slug: cached.Slug, Method: MatchMethod(cached.Method)}, true
		}
	}
	return MatchResult{}, false
}

// batchExact resolves rows via the BATCHED exact-id rung for one field: it gathers
// each row's key (asin | isbn13), asks editionsByField for all of them in ~100-id
// chunks, and for every hit caches the exact-id resolution (idempotent, cross-user)
// + links it + enriches kindle bare rows. It returns the rows it did NOT resolve
// (for the next rung / the fuzzy tail). graphField is the Hardcover column name
// (asin|isbn_13); cacheType is the cache id_type (asin|isbn13); method is the
// MatchMethod stamped on the link+cache. On ErrBadToken/ErrRateLimited it aborts
// the whole sweep (nil rows + the error); any other batch error is non-fatal —
// logged, and every input row falls through unmatched.
func (s *SyncService) batchExact(ctx context.Context, owner string, client matcher, graphField, cacheType string, method MatchMethod, rows []*matchRow, res *MatchSweepResult) ([]*matchRow, error) {
	keyOf := func(r *matchRow) string {
		if cacheType == "asin" {
			return r.asin
		}
		return r.isbnKey
	}
	values := make([]string, 0, len(rows))
	for _, r := range rows {
		if k := keyOf(r); k != "" {
			values = append(values, k)
		}
	}
	if len(values) == 0 {
		return rows, nil
	}

	edMap, err := client.editionsByField(ctx, graphField, values)
	if err != nil {
		if err == ErrBadToken || err == ErrRateLimited {
			s.onError(ctx, owner, "match", err)
			return nil, err
		}
		// A non-sentinel batch error must not strand the backlog — treat every row as
		// unmatched and let the next rung / fuzzy tail try them.
		s.logWarn(ctx, "hardcover match: batch resolve failed — falling through", "user", owner, "field", graphField, "err", err)
		return rows, nil
	}

	rest := make([]*matchRow, 0, len(rows))
	for _, r := range rows {
		k := keyOf(r)
		ed, ok := edMap[k]
		if k == "" || !ok || ed.BookID <= 0 {
			rest = append(rest, r)
			continue
		}
		m := MatchResult{BookID: ed.BookID, EditionID: ed.ID, Slug: ed.Book.Slug, Method: method}
		// Populate the GLOBAL cache from this confident EXACT-ID hit (best-effort).
		if perr := s.DB.PutHardcoverMatch(ctx, cacheType, k, m.BookID, m.EditionID, string(method), m.Slug); perr != nil {
			s.logWarn(ctx, "hardcover match: cache put failed", "user", owner, "idtype", cacheType, "external", k, "err", perr)
		}
		if s.linkAndEnrich(ctx, owner, r, m, client, res) {
			res.BatchHits++
		}
	}
	return rest, nil
}

// linkAndEnrich writes the per-user Hardcover link for a resolved row and, for a
// kindle bare-ASIN row, backfills display metadata via LookupByASIN. It is the
// shared tail of every match path (cache / batch / fuzzy). Returns true when the
// link landed (res.Matched incremented); on a link-write failure it counts Skipped
// and returns false. Enrich is best-effort — a lookup/backfill miss never fails
// the row.
func (s *SyncService) linkAndEnrich(ctx context.Context, owner string, r *matchRow, m MatchResult, client matcher, res *MatchSweepResult) bool {
	it := r.it
	if lerr := s.DB.SetReadingItemHardcoverLink(ctx, owner, it.Source, it.ExternalID, m.BookID, m.EditionID, string(m.Method), m.Slug); lerr != nil {
		s.logWarn(ctx, "hardcover match: link write failed", "user", owner, "source", it.Source, "external", it.ExternalID, "err", lerr)
		res.Skipped++
		return false
	}
	res.Matched++

	// Kindle bare-ASIN rows arrive with a blank title (the ingest had only the
	// ASIN). Now that we've matched, pull display metadata so the Books view can
	// render a real row.
	if it.Source == kindleReadingSource && strings.TrimSpace(it.Title) == "" && r.asin != "" {
		if meta, lerr := client.LookupByASIN(ctx, r.asin); lerr != nil {
			s.logWarn(ctx, "hardcover match: lookup for enrich failed", "user", owner, "asin", r.asin, "err", lerr)
		} else if meta != nil {
			if n, uerr := s.DB.UpdateReadingItemDisplayMeta(ctx, owner, it.Source, it.ExternalID, meta.Title, meta.Authors, meta.CoverURL); uerr != nil {
				s.logWarn(ctx, "hardcover match: display-meta backfill failed", "user", owner, "asin", r.asin, "err", uerr)
			} else if n > 0 {
				res.Enriched++
			}
		}
	}
	return true
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
