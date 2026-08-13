// ingest.go — the Kindle SyncUser sweep: whispersync shelves → per-book sidecar
// reading position → Hardcover metadata → reading_items(source='kindle').
//
// Pipeline (docs/design/catalyst-books-domain-architecture.md §6.3, Option A):
//
//  1. whispersync datasets → keep the CloudCollections shelves.
//  2. shelf name → status (Currently Reading→reading, Done Reading→read, Have
//     Not Read→want; series/other shelves contribute membership only). Union the
//     ASINs across shelves, keeping the STRONGEST status seen (read>reading>want).
//  3. per ASIN: sidecar kindle.lpr → reading position + last-read date.
//  4. per ASIN: Hardcover LookupByASIN → title/author/cover + book_id/edition_id.
//  5. upsert reading_items + cache the Hardcover linkage (pre-linked).
//
// The sweep is factored out of SyncUser so it is unit-testable against fake
// amazon + hardcover clients with no network + no DB.
package books

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
)

// kindleSource is the narrow Kindle wire surface SyncUser depends on. The real
// implementation is *amazon.KindleClient; tests supply a fake.
type kindleSource interface {
	Datasets(ctx context.Context, cred *amazon.DeviceCredential) ([]amazon.Dataset, error)
	CollectionRecords(ctx context.Context, cred *amazon.DeviceCredential, datasetID string) ([]amazon.CollectionRecord, error)
	Sidecar(ctx context.Context, cred *amazon.DeviceCredential, asin string) (*amazon.SidecarPosition, error)
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
}

// shelfInfo is the accumulated per-ASIN state during a shelf union: the best
// status seen and the most recent shelf-membership timestamp.
type shelfInfo struct {
	status      string
	lastUpdated *time.Time
}

// statusRank orders shelf statuses so a union keeps the strongest: read beats
// reading beats want beats unknown. A book on both "Currently Reading" and a
// series shelf keeps "reading"; on both "Currently Reading" and "Done Reading"
// keeps "read".
func statusRank(status string) int {
	switch status {
	case "read":
		return 3
	case "reading":
		return 2
	case "want":
		return 1
	default:
		return 0
	}
}

// shelfStatus maps a CloudCollections shelf label to a reading status. The
// second return is false for shelves that convey membership only (series, custom
// collections) — those still ingest the book but set no status. Phrase order
// matters: "Have Not Read" and "Done Reading" both contain "read", so the
// specific phrases are tested before the generic contains() fallbacks.
func shelfStatus(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "currently readings", "currently reading":
		return "reading", true
	case "done reading", "read", "done", "finished":
		return "read", true
	case "have not read", "want to read", "want", "to read":
		return "want", true
	}
	switch {
	case strings.Contains(n, "currently read"):
		return "reading", true
	case strings.Contains(n, "have not read"), strings.Contains(n, "want"):
		return "want", true
	case strings.Contains(n, "done read"), strings.Contains(n, "finished"):
		return "read", true
	}
	return "", false
}

// collectShelves unions the CloudCollections shelves into a per-ASIN best-status
// map. Series/other shelves contribute membership (an entry with empty status)
// so the book is still ingested; status shelves upgrade it to reading/read/want.
// Deleted tombstones are skipped.
func (s *Service) collectShelves(ctx context.Context, cred *amazon.DeviceCredential, datasets []amazon.Dataset) (map[string]*shelfInfo, error) {
	byASIN := map[string]*shelfInfo{}
	for _, ds := range amazon.CloudCollections(datasets) {
		status, _ := shelfStatus(ds.Name)
		records, err := s.kindle.CollectionRecords(ctx, cred, ds.Identifier)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			if rec.ASIN == "" || rec.IsDeleted {
				continue
			}
			info := byASIN[rec.ASIN]
			if info == nil {
				info = &shelfInfo{}
				byASIN[rec.ASIN] = info
			}
			if statusRank(status) > statusRank(info.status) {
				info.status = status
			}
			if rec.LastUpdated != nil && (info.lastUpdated == nil || rec.LastUpdated.After(*info.lastUpdated)) {
				info.lastUpdated = rec.LastUpdated
			}
		}
	}
	return byASIN, nil
}

// sweep runs the full read-side pipeline and returns the mapped items. Pure of
// the DB: it takes the credential + a (possibly nil) Hardcover resolver and uses
// s.kindle for the wire. hardcoverConnected reports whether metadata resolution
// was available (false => rows carry ASIN only), for the caller to log/note.
func (s *Service) sweep(ctx context.Context, cred *amazon.DeviceCredential, owner string, res metaResolver) ([]kindleItem, bool, error) {
	datasets, err := s.kindle.Datasets(ctx, cred)
	if err != nil {
		return nil, res != nil, err
	}
	byASIN, err := s.collectShelves(ctx, cred, datasets)
	if err != nil {
		return nil, res != nil, err
	}

	items := make([]kindleItem, 0, len(byASIN))
	for asin, info := range byASIN {
		// sidecar reading position (best-effort — a miss/err leaves side nil).
		var side *amazon.SidecarPosition
		if sp, err := s.kindle.Sidecar(ctx, cred, asin); err != nil {
			s.logWarn("kindle sidecar failed — position skipped", "user", owner, "asin", asin, "err", err)
		} else {
			side = sp
		}

		// Hardcover metadata (best-effort — nil when not connected / no match).
		var meta *hardcover.BookMeta
		if res != nil {
			if m, err := res.LookupByASIN(ctx, asin); err != nil {
				s.logWarn("kindle: hardcover lookup failed — ingesting ASIN only", "user", owner, "asin", asin, "err", err)
			} else {
				meta = m
			}
		}

		items = append(items, buildReadingItem(owner, asin, info, side, meta))
	}
	return items, res != nil, nil
}

// SyncUser pulls the user's Kindle shelves + reading positions (via the shared
// Amazon device credential), resolves metadata through Hardcover, and upserts
// them into reading_items(source='kindle'). Returns the number of items
// upserted. Idempotent — a re-run with unchanged shelves re-upserts the same
// rows. This is the job-handler body for KindleSyncKind.
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
			if err := s.DB.SetReadingItemHardcoverLink(ctx, owner, source, ki.Item.ExternalID, ki.BookID, ki.EditionID, ki.MatchConf); err != nil {
				return count, err
			}
		}
		count++
	}

	if !hcConnected {
		s.logInfo("kindle sync: Hardcover not connected — ingested ASIN-only rows", "user", owner, "items", count)
	} else {
		s.logInfo("kindle sync complete", "user", owner, "items", count)
	}
	return count, nil
}

// BackfillUser is the one-shot mirror of SyncUser. The Kindle shelves + sidecar
// snapshot are already the full current state (there is no historical library
// paging as with Audible), so a backfill IS a full sweep — same code path.
func (s *Service) BackfillUser(ctx context.Context, owner string) (int, error) {
	return s.SyncUser(ctx, owner)
}

// resolverFor returns the user's Hardcover client as a metaResolver, or nil when
// Hardcover isn't configured / the user hasn't connected it (rows then ingest
// with ASIN only). A load error is logged and treated as "not connected".
func (s *Service) resolverFor(ctx context.Context, owner string) metaResolver {
	if s.Hardcover == nil {
		return nil
	}
	client, ok, err := s.Hardcover.ClientForUser(ctx, owner)
	if err != nil {
		s.logWarn("kindle: hardcover client load failed — ingesting ASIN only", "user", owner, "err", err)
		return nil
	}
	if !ok {
		return nil
	}
	return client
}

// buildReadingItem maps one ASIN's accumulated state (shelf status + sidecar
// position + Hardcover metadata) into a reading_items row (+ the linkage ids for
// SetReadingItemHardcoverLink). Pure — no DB, no network — so it unit-tests the
// mapping directly.
func buildReadingItem(owner, asin string, info *shelfInfo, side *amazon.SidecarPosition, meta *hardcover.BookMeta) kindleItem {
	status := ""
	if info != nil {
		status = info.status
	}
	finished := status == "read"

	// Progress: read → 100; otherwise use the sidecar percent when the LPR record
	// carried one (rare — position is usually an opaque locator with no total).
	progress := 0
	if finished {
		progress = 100
	} else if side != nil && side.Percent != nil {
		progress = clampPercent(*side.Percent)
	}

	// The sidecar timestamp dates the read: it seeds started_at for a book in
	// progress and finished_at for a finished one. Fall back to the shelf
	// membership timestamp when the sidecar has no time.
	var readAt *time.Time
	if side != nil && side.LastUpdated != nil {
		readAt = side.LastUpdated
	} else if info != nil && info.lastUpdated != nil {
		readAt = info.lastUpdated
	}

	var startedAt, finishedAt *time.Time
	switch {
	case finished:
		finishedAt = readAt
		startedAt = readAt
	case status == "reading":
		startedAt = readAt
	}

	ri := db.ReadingItem{
		Owner:           owner,
		Source:          source,
		ExternalID:      asin,
		AmazonASIN:      asin,
		Status:          status,
		ProgressPercent: progress,
		Finished:        finished,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		RawMeta:         rawMeta(asin, status, side),
	}
	out := kindleItem{Item: ri}
	if meta != nil {
		ri.Title = meta.Title
		ri.Authors = meta.Authors
		ri.CoverURL = meta.CoverURL
		out.Item = ri
		out.BookID = meta.BookID
		out.EditionID = meta.EditionID
		out.MatchConf = "asin" // LookupByASIN is an exact-ASIN edition match.
	}
	return out
}

// rawMeta captures the minimal source snapshot for reading_items.raw_meta: the
// ASIN, the shelf-derived status, and the sidecar LPR (position + date) when
// present — enough to reconstruct/debug the row without re-hitting Amazon.
func rawMeta(asin, status string, side *amazon.SidecarPosition) []byte {
	m := map[string]any{"asin": asin, "status": status, "source": source}
	if side != nil {
		lpr := map[string]any{"position": side.Position}
		if side.Percent != nil {
			lpr["percent"] = *side.Percent
		}
		if side.LastUpdated != nil {
			lpr["lastUpdated"] = side.LastUpdated.UTC().Format(time.RFC3339)
		}
		m["kindle.lpr"] = lpr
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

func clampPercent(f float64) int {
	if f <= 0 {
		return 0
	}
	if f >= 100 {
		return 100
	}
	return int(f + 0.5)
}

// ---------------------------------------------------------------------------
// logging helpers (nil-safe)
// ---------------------------------------------------------------------------

func (s *Service) logInfo(msg string, args ...any) {
	if s.Logger != nil {
		s.Logger.Info(msg, args...)
	}
}

func (s *Service) logWarn(msg string, args ...any) {
	if s.Logger != nil {
		s.Logger.Warn(msg, args...)
	}
}
