// Package books is the catalyst-books ingestion domain: KINDLE reading (ebooks).
// THIN by design — it maps Kindle library + whispersync positions (fetched via
// the SHARED internal/amazon device credential) into reading state, and
// registers its periodic sync job on the catalyst-go-jobs scheduler. It owns no
// auth (that's internal/amazon) and no push (that's internal/hardcover).
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
	"context"
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// KindleSyncKind is the catalyst-go-jobs kind for the periodic Kindle sync.
const KindleSyncKind = "books-kindle-sync"

// Service is the catalyst-books domain entrypoint. Thin: it leans on the shared
// Amazon credential (auth + signing) and the DB for storage.
type Service struct {
	DB     *db.DB
	Amazon *amazon.Store
	Logger *slog.Logger
}

// New constructs the books (Kindle) domain service.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{DB: database, Amazon: az, Logger: logger}
}

// SyncUser pulls the user's Kindle library + reading positions (via the shared
// Amazon device credential) and upserts them into reading state. Idempotent —
// same shape as internal/github.Service.SyncUser.
//
// TODO(catalyst-books, next chunk): load the amazon cred → sign Fiona/whispersync
// requests (internal/amazon.Sign, verified live) → derive progress % from
// position → upsert reading_state. See book-tracking-research.md §3.2.
func (s *Service) SyncUser(ctx context.Context, username string) error {
	_ = ctx
	_ = username
	return nil
}
