// Package audiobooks is the catalyst-audiobooks ingestion domain: AUDIBLE
// listening. THIN by design — it maps the Audible library + listening stats
// (fetched via the SHARED internal/amazon device credential) into reading
// (listening) state, and registers its periodic sync job on the catalyst-go-jobs
// scheduler. It owns no auth (that's internal/amazon) and no push (that's
// internal/hardcover).
//
// Mirrors internal/domains/books exactly (standard domain layout):
//
//	<name>.go   — package doc + Service{deps} constructor (this file)
//	ingest.go   — SyncUser: pull from source → upsert reading state
//	jobs.go     — <Name>SyncKind + RegisterJobs(reg, sched)
//	model.go    — source DTOs      (added with the data model)
//	routes.go   — query endpoints  (added with the query API)
package audiobooks

import (
	"context"
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// AudibleSyncKind is the catalyst-go-jobs kind for the periodic Audible sync.
const AudibleSyncKind = "audiobooks-audible-sync"

// Service is the catalyst-audiobooks domain entrypoint. Thin: it leans on the
// shared Amazon credential (auth + signing) and the DB for storage.
type Service struct {
	DB     *db.DB
	Amazon *amazon.Store
	Logger *slog.Logger
}

// New constructs the audiobooks (Audible) domain service.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{DB: database, Amazon: az, Logger: logger}
}

// SyncUser pulls the user's Audible library + listening progress/finish dates
// (via the shared Amazon device credential) and upserts them into reading state.
// Idempotent — same shape as internal/github.Service.SyncUser.
//
// TODO(catalyst-audiobooks, next chunk): load the amazon cred → GET /1.0/library
// with the is_finished,percent_complete response_groups + /1.0/stats/status/finished
// → upsert reading_state. See book-tracking-research.md §3.1.
func (s *Service) SyncUser(ctx context.Context, username string) error {
	_ = ctx
	_ = username
	return nil
}
