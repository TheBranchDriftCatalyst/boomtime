// Package books is the catalyst-books ingestion domain: KINDLE reading (ebooks).
// THIN by design — it maps the read.amazon.com Cloud Reader library (fetched via
// the SHARED internal/amazon device credential, exchanged for website cookies)
// into reading state, and registers its periodic sync job on the catalyst-go-jobs
// scheduler. It owns no auth (that's internal/amazon) and no push (that's
// internal/hardcover).
//
// Library source (gaka: Cloud Reader cutover): the Cloud Reader
// /kindle-library/search feed returns the user's FULL library with title,
// authors, percentageRead, and cover directly from Amazon — so titles no longer
// depend on a Hardcover match. This replaced the whispersync/CloudCollections
// path (kindle.go), which only saw shelf-filed books (no titles). Hardcover is
// now used only for the optional book_id/edition_id linkage.
//
// Standard domain layout (identical across internal/domains/*, split out as the
// code grows):
//
//	<name>.go   — package doc + Service{deps} constructor (this file)
//	ingest.go   — SyncUser: pull from source → upsert reading state
//	jobs.go     — <Name>SyncKind + RegisterJobs(reg, sched)
//	model.go    — source DTOs      (added with the data model)
//	routes.go   — query endpoints  (added with the query API)
package books

import (
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
)

// KindleSyncKind is the catalyst-go-jobs kind for the periodic Kindle sync.
const KindleSyncKind = "books-kindle-sync"

// KindleBackfillKind is the one-shot, owner-scoped Kindle backfill kind (enqueued
// on demand from the connect flow / admin, never scheduled). BackfillUser is a
// full sweep — the Cloud Reader library is already the complete current state —
// so the backfill shares SyncUser's code path.
const KindleBackfillKind = "books-kindle-backfill"

// source is the reading_items.source tag for every row this domain writes.
const source = "kindle"

// Service is the catalyst-books domain entrypoint. Thin: it leans on the shared
// Amazon credential (auth + signing), the shared Hardcover connector (ASIN →
// metadata + linkage), and the DB for storage.
type Service struct {
	DB     *db.DB
	Amazon *amazon.Store
	Logger *slog.Logger

	// Hardcover (nil-safe) resolves an ASIN to title/author/cover + the
	// hardcover_book_id/edition_id linkage. nil (or user-not-connected) => rows
	// ingest with ASIN only and title left blank.
	Hardcover *hardcover.Store

	// kindle is the Cloud Reader library client; swappable in tests via a narrow
	// interface so SyncUser exercises without a network.
	kindle kindleSource
}

// New constructs the books (Kindle) domain service. Hardcover is wired after
// construction (SetHardcover) so callers without it keep working.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{DB: database, Amazon: az, Logger: logger, kindle: amazon.NewCloudReaderClient()}
}

// SetHardcover wires the Hardcover connector (nil-safe).
func (s *Service) SetHardcover(store *hardcover.Store) *Service { s.Hardcover = store; return s }
