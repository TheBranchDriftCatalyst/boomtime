// ingest.go — the Kindle SyncUser sweep: read.amazon.com Cloud Reader library →
// reading_items(source='kindle'), with optional Hardcover linkage.
//
// Pipeline (Cloud Reader cutover):
//
//  1. device credential → ExchangeWebsiteCookies (refresh_token → .amazon.com
//     website cookies).
//  2. KindleCloudLibrary → the FULL paginated library (asin, title, authors,
//     percentageRead, cover) directly from Amazon.
//  3. per item: percentageRead → status/progress/finished; title/authors/cover
//     come from Amazon (no Hardcover needed).
//  4. per item (optional): Hardcover LookupByASIN → book_id/edition_id linkage
//     only (title already set). Skipped when Hardcover is not connected.
//  5. upsert reading_items + cache the Hardcover linkage when matched.
//
// The sweep is factored out of SyncUser so it is unit-testable against fake
// amazon + hardcover clients with no network + no DB.
package books

import (
	"context"
	"encoding/json"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
)

// kindleSource is the narrow Cloud Reader wire surface SyncUser depends on. The
// real implementation is *amazon.CloudReaderClient; tests supply a fake.
type kindleSource interface {
	ExchangeWebsiteCookies(ctx context.Context, cred *amazon.DeviceCredential) (map[string]string, error)
	KindleCloudLibrary(ctx context.Context, cookies map[string]string) ([]amazon.CloudLibraryItem, error)
	// FetchKindleInsights pulls the reading-insights history (finish dates +
	// streaks) — the per-book finish-DATE source the library feed lacks. Same
	// website-cookie auth as KindleCloudLibrary.
	FetchKindleInsights(ctx context.Context, cookies map[string]string) (*amazon.KindleInsights, error)
}

// metaResolver resolves an ASIN to Hardcover metadata + linkage ids. The real
// implementation is *hardcover.Client; nil means "Hardcover not connected".
type metaResolver interface {
	LookupByASIN(ctx context.Context, asin string) (*hardcover.BookMeta, error)
}

// kindleItem bundles a mapped reading_items row with the Hardcover match ids so
// SyncUser can upsert the row and then cache the linkage in one pass.
type kindleItem struct {
	Item      db.ReadingItem
	BookID    int64
	EditionID int64
	MatchConf string
	Slug      string // the book's Hardcover slug (deep-link segment); "" when unresolved
}

// statusFromPercent maps a Cloud Reader percentageRead (0..100) to a reading
// status + finished flag:
//
//	100      -> "read"   (finished)
//	1..99    -> "reading"
//	0        -> "want"   (owned but not started)
//
// This is the single source of the reading state now that Amazon reports real
// progress (the old shelf-label heuristic is gone).
func statusFromPercent(pct int) (status string, finished bool) {
	switch {
	case pct >= 100:
		return "read", true
	case pct <= 0:
		return "want", false
	default:
		return "reading", false
	}
}

// sweep runs the full read-side pipeline and returns the mapped items. Pure of
// the DB: it takes the credential + a (possibly nil) Hardcover resolver and uses
// s.kindle for the wire. hardcoverConnected reports whether metadata resolution
// was available (for the caller to log/note) — it no longer gates titles, only
// the book_id/edition_id linkage.
func (s *Service) sweep(ctx context.Context, cred *amazon.DeviceCredential, owner string, res metaResolver) ([]kindleItem, bool, error) {
	cookies, err := s.kindle.ExchangeWebsiteCookies(ctx, cred)
	if err != nil {
		return nil, res != nil, err
	}
	library, err := s.kindle.KindleCloudLibrary(ctx, cookies)
	if err != nil {
		return nil, res != nil, err
	}
	// Progress: the Cloud Reader fetch is the first slow phase — log the raw
	// library size so a running sync shows activity before the mapping loop.
	s.logInfo(ctx, "kindle: fetched library", "user", owner, "items", len(library))

	items := make([]kindleItem, 0, len(library))
	for _, lib := range library {
		// Stop promptly on cancellation — a multi-thousand-book library is a large
		// mapping loop.
		if err := ctx.Err(); err != nil {
			return nil, res != nil, err
		}
		if lib.ASIN == "" || lib.IsSample() {
			continue // skip un-keyable rows and Kindle samples
		}

		// Ingest is Amazon-only: title/authors/cover/progress all come from the
		// Cloud Reader item, so ingest resolves the whole library with ZERO
		// Hardcover calls. Hardcover linkage (book_id/edition_id) is deliberately
		// NOT resolved here — a per-book LookupByASIN over a 2500-book library is
		// thousands of rate-limited Hardcover calls (hours) AND bypasses the
		// cache-first match step. The hardcover-match job fills linkage for
		// unmatched rows cache-first (per-user link → global hardcover_match_cache →
		// Hardcover only for a book new to all of boomtime). gaka-wzgr.
		items = append(items, buildReadingItem(owner, lib, nil))
	}
	return items, res != nil, nil
}

// SyncUser pulls the user's full Kindle library from the read.amazon.com Cloud
// Reader (via the shared Amazon device credential, exchanged for website
// cookies), maps each item's percentageRead into reading state, optionally
// resolves the Hardcover linkage, and upserts into reading_items(source='kindle').
// Returns the number of items upserted. Idempotent — a re-run with an unchanged
// library re-upserts the same rows. This is the job-handler body for
// KindleSyncKind.
func (s *Service) SyncUser(ctx context.Context, owner string) (int, error) {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return 0, err
	}

	res := s.resolverFor(ctx, owner)
	items, hcConnected, err := s.sweep(ctx, cred, owner, res)
	if err != nil {
		_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusInvalid)
		return 0, err
	}
	_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusValid)

	count := 0
	for _, ki := range items {
		if err := s.DB.UpsertReadingItem(ctx, ki.Item); err != nil {
			return count, err
		}
		if ki.BookID > 0 {
			if err := s.DB.SetReadingItemHardcoverLink(ctx, owner, source, ki.Item.ExternalID, ki.BookID, ki.EditionID, ki.MatchConf, ki.Slug); err != nil {
				return count, err
			}
		}
		count++
		// Mid-loop progress every ~500 rows so a very large library (thousands of
		// books) shows steady advancement rather than one line at the very end.
		if count%500 == 0 {
			s.logInfo(ctx, "kindle: upserting library", "user", owner, "upsertedSoFar", count, "of", len(items))
		}
	}
	s.logInfo(ctx, "kindle: upserted", "user", owner, "items", count)

	if !hcConnected {
		s.logInfo(ctx, "kindle sync: Hardcover not connected — titles from Amazon, no linkage", "user", owner, "items", count)
	} else {
		s.logInfo(ctx, "kindle sync complete", "user", owner, "items", count)
	}
	return count, nil
}

// BackfillUser is the one-shot mirror of SyncUser. The Cloud Reader library is
// already the full current state (there is no historical library paging as with
// Audible — the search feed returns everything), so a backfill IS a full sweep —
// same code path.
func (s *Service) BackfillUser(ctx context.Context, owner string) (int, error) {
	return s.SyncUser(ctx, owner)
}

// resolverFor returns the user's Hardcover client as a metaResolver, or nil when
// Hardcover isn't configured / the user hasn't connected it (rows then ingest
// with Amazon metadata but no book_id/edition_id linkage). A load error is
// logged and treated as "not connected".
func (s *Service) resolverFor(ctx context.Context, owner string) metaResolver {
	if s.Hardcover == nil {
		return nil
	}
	client, ok, err := s.Hardcover.ClientForUser(ctx, owner)
	if err != nil {
		s.logWarn(ctx, "kindle: hardcover client load failed — ingesting without linkage", "user", owner, "err", err)
		return nil
	}
	if !ok {
		return nil
	}
	return client
}

// buildReadingItem maps one Cloud Reader library item (+ optional Hardcover
// metadata) into a reading_items row (+ the linkage ids for
// SetReadingItemHardcoverLink). Pure — no DB, no network — so it unit-tests the
// mapping directly. Title/authors/cover come from Amazon; Hardcover only
// supplies the linkage and fills any gap Amazon left blank.
func buildReadingItem(owner string, lib amazon.CloudLibraryItem, meta *hardcover.BookMeta) kindleItem {
	status, finished := statusFromPercent(lib.PercentageRead)

	// Progress is the Cloud Reader percentageRead directly (0..100).
	progress := lib.PercentageRead
	if finished {
		progress = 100
	}

	// The Cloud Reader feed carries no per-book timestamps, so started_at /
	// finished_at are left nil (a later sync from a positioned source, or the
	// user, can set them). Status alone drives the reading list.
	ri := db.ReadingItem{
		Owner:           owner,
		Source:          source,
		ExternalID:      lib.ASIN,
		AmazonASIN:      lib.ASIN,
		Title:           lib.Title,
		Authors:         lib.AuthorsCSV(),
		CoverURL:        lib.CoverURL,
		Status:          status,
		ProgressPercent: progress,
		Finished:        finished,
		RawMeta:         rawMeta(lib),
	}

	out := kindleItem{Item: ri}
	if meta != nil {
		// Amazon is the primary title source; only backfill fields Amazon left
		// blank so a good Amazon value isn't clobbered by a weaker match.
		if ri.Title == "" {
			ri.Title = meta.Title
		}
		if ri.Authors == "" {
			ri.Authors = meta.Authors
		}
		if ri.CoverURL == "" {
			ri.CoverURL = meta.CoverURL
		}
		out.Item = ri
		out.BookID = meta.BookID
		out.EditionID = meta.EditionID
		out.Slug = meta.Slug
		out.MatchConf = "asin" // LookupByASIN is an exact-ASIN edition match.
	}
	return out
}

// rawMeta captures the minimal source snapshot for reading_items.raw_meta: the
// Cloud Reader library fields for this book — enough to reconstruct/debug the row
// without re-hitting Amazon.
func rawMeta(lib amazon.CloudLibraryItem) []byte {
	m := map[string]any{
		"asin":           lib.ASIN,
		"source":         source,
		"title":          lib.Title,
		"authors":        lib.Authors,
		"percentageRead": lib.PercentageRead,
	}
	if lib.CoverURL != "" {
		m["coverUrl"] = lib.CoverURL
	}
	if lib.WebReaderURL != "" {
		m["webReaderUrl"] = lib.WebReaderURL
	}
	if lib.ResourceType != "" {
		m["resourceType"] = lib.ResourceType
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

// ---------------------------------------------------------------------------
// logging helpers (nil-safe)
// ---------------------------------------------------------------------------

// logInfo/logWarn resolve the job-scoped logger from ctx (logctx.FromContext),
// falling back to s.Logger off a job. Threading ctx means every handler line
// inherits the running job's job_id/kind/owner so the Admin viewer can filter to
// one job's run (gaka-f0is).
func (s *Service) logInfo(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Info(msg, args...)
	}
}

func (s *Service) logWarn(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Warn(msg, args...)
	}
}
