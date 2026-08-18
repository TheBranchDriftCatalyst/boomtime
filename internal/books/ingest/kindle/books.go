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
package kindle

import (
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/notify"
)

// KindleSyncKind is the catalyst-go-jobs kind for the periodic Kindle sync.
const KindleSyncKind = "books-kindle-sync"

// KindleBackfillKind is the one-shot, owner-scoped Kindle backfill kind (enqueued
// on demand from the connect flow / admin, never scheduled). BackfillUser is a
// full sweep — the Cloud Reader library is already the complete current state —
// so the backfill shares SyncUser's code path.
const KindleBackfillKind = "books-kindle-backfill"

// KindleInsightsKind is the catalyst-go-jobs kind for the Kindle Reading-Insights
// ingest: fetch the reading history (finish DATES + streaks) and backfill
// reading_items.finished_at. It is the finish-date companion to KindleSyncKind
// (the library feed carries no timestamps) — run it AFTER the library sync so the
// rows it dates already exist. Owner-scoped or fanned over all connected users.
const KindleInsightsKind = "books-kindle-insights"

// KindleStatusReconcileKind is the catalyst-go-jobs kind for the Kindle STATUS
// reconcile sweep: for every non-read kindle book, poll the CDE sidecar for a
// last-page-read record and set an honest status — 'reading' when an lpr exists,
// left 'want' when it 404s. It exists because the Cloud Reader library feed
// reports percentageRead=0 for every book, so ingest can only default to 'want';
// the sidecar's lpr is the one signal that a book has actually been opened. Run
// it AFTER KindleInsightsKind (which sets 'read' + finished_at from the finished
// history) so this sweep only touches genuinely-non-read rows. Owner-scoped or
// fanned over all connected users. See ReconcileKindleStatus (reconcile.go).
const KindleStatusReconcileKind = "books-kindle-status-reconcile"

// KindleReadingTimeKind is the catalyst-go-jobs kind for the FORWARD Kindle
// reading-TIME poll: sample each in-progress book's last-page-read position,
// gap-sum consecutive samples into reading sessions, and write reading-seconds
// into reading_activity(source='kindle'). Owner-scoped or fanned over all
// connected users. See reading_time.go (PollReadingTime).
const KindleReadingTimeKind = "books-kindle-reading-time"

// ReadingMonitorKind is the catalyst-go-jobs kind for the PERSISTENT server-side
// two-level reading-monitor (catalyst-books §5.1). Scheduled leader-singleton on
// a short base tick; each run drives the L1/L2 engine over every user with
// reading_monitor_enabled — so the monitor survives the admin tab closing. See
// monitor.go (RunMonitorLoop / RunMonitorOnce).
const ReadingMonitorKind = "books-reading-monitor"

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

	// Notify (nil-safe) delivers owner-scoped toasts app-wide — the same seam
	// Audible finishes use. The persistent reading-monitor (monitor.go) publishes
	// through it on an advance / status change; nil => no toasts (the monitor's
	// metrics + reading_activity still land).
	Notify *notify.Hub

	// kindle is the Cloud Reader library client; swappable in tests via a narrow
	// interface so SyncUser exercises without a network.
	kindle kindleSource

	// sidecar is the forward reading-time position source (Fiona CDE last-page-
	// read); swappable in tests via positionSource so PollReadingTime exercises
	// the composition without a network. See reading_time.go.
	sidecar positionSource

	// monitorCfg is the consolidated reading-monitor tuning (monitorconfig.go) —
	// the single source of truth for every knob + coefficient. Used by the
	// non-engine reading-time poll (PollReadingTime) for the session gap, lookback,
	// and composition-method selection. New seeds it with withDefaults(); main.go
	// overrides it with the env-loaded config via SetMonitorConfig. The persistent
	// engine (RunMonitorLoop) receives its MonitorConfig as an explicit argument.
	monitorCfg MonitorConfig
}

// New constructs the books (Kindle) domain service. Hardcover is wired after
// construction (SetHardcover) so callers without it keep working.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{
		DB:         database,
		Amazon:     az,
		Logger:     logger,
		kindle:     amazon.NewCloudReaderClient(),
		sidecar:    amazon.NewKindleSidecarClient(),
		monitorCfg: MonitorConfig{}.withDefaults(),
	}
}

// SetMonitorConfig overrides the consolidated reading-monitor tuning (defaults are
// seeded in New). main.go calls this with books.LoadMonitorConfig() so the
// env-configured values reach the non-engine reading-time poll. Chainable.
func (s *Service) SetMonitorConfig(cfg MonitorConfig) *Service {
	s.monitorCfg = cfg.withDefaults()
	return s
}

// SetHardcover wires the Hardcover connector (nil-safe).
func (s *Service) SetHardcover(store *hardcover.Store) *Service { s.Hardcover = store; return s }

// SetNotify wires the notification hub (nil-safe) so the persistent
// reading-monitor can toast on an advance / status change. Mirrors
// audible.Service.SetNotify.
func (s *Service) SetNotify(hub *notify.Hub) *Service { s.Notify = hub; return s }

// SetSidecar swaps the forward reading-time position source. Production wires
// *amazon.KindleSidecarClient in New; this seam lets a test inject a fake (the
// positionSource interface is unexported, but a value implementing
// FetchLastPagePosition can still be passed in). Mirrors SetHardcover.
func (s *Service) SetSidecar(sc positionSource) *Service { s.sidecar = sc; return s }
