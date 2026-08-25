// Package jobs is the catalyst-books job wiring (boom-zp2s), lifted verbatim out of
// cmd/boomtime/main.go so the books domain owns its own kinds, fleet caps, and
// schedules — driven by books.Module.RegisterJobs instead of hand-wired in the host.
//
// The host's composition ORDER is preserved exactly (byte-identical): Register runs at
// the same point the old inline block did (before the jobs provider is built), so the
// job enqueuer is late-bound via WireEnqueuer once the provider exists — the handlers
// close over the shared Services either way. Schedules register later (they need the
// scheduler) via RegisterSchedules, gated exactly as the old inline block.
//
// The same packages mount into the standalone cmd/catalyst-books image too.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/ingest/audible"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/ingest/kindle"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/pipeline"
	corejobs "github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/notify"
)

// Services holds the shared per-domain service instances the registered job handlers
// close over. Constructed once by Register (nil when Books is disabled); the host
// late-wires the provider onto them via WireEnqueuer after the jobs provider exists.
type Services struct {
	audio      *audible.Service
	hcCuration *hardcover.PushService
	liberation *liberate.Service
	// libEnq is late-bound by WireEnqueuer: the SWEEP handler needs to enqueue a
	// per-book job, but the provider does not exist yet when Register runs.
	libEnq corejobs.Enqueuer
}

// Liberation returns the liberation service (nil when books or the liberation
// flag is off, or when no library path is configured). The HTTP handler shares
// this SAME instance so a manual "liberate this book" and a swept one run
// identical code.
func (s *Services) Liberation() *liberate.Service {
	if s == nil {
		return nil
	}
	return s.liberation
}

// CurationPush returns the inline Hardcover curation-push service (the per-row sync
// button pushes through it INLINE, bypassing the capped queue). nil when Books is off.
func (s *Services) CurationPush() *hardcover.PushService {
	if s == nil {
		return nil
	}
	return s.hcCuration
}

// WireEnqueuer routes the audiobooks service's finished-book Hardcover pushes onto the
// capped HardcoverPushKind queue (instead of pushing inline) now that the provider
// (a corejobs.Enqueuer) exists. No-op when Books is disabled. Byte-identical to the old
// `if audioSvc != nil { audioSvc.SetEnqueuer(provider) }`.
func (s *Services) WireEnqueuer(enq corejobs.Enqueuer) {
	if s == nil {
		return
	}
	// The liberation sweep enqueues one job per book, so it needs the provider
	// too. Stored rather than pushed onto the service because liberate.Service
	// deliberately knows nothing about the jobs subsystem — LiberateAll returns
	// a list of ASINs and the HANDLER decides what to do with them.
	s.libEnq = enq
	if s.audio == nil {
		return
	}
	s.audio.SetEnqueuer(enq)
}

// Register registers the catalyst-books job KINDS + fleet-wide per-kind concurrency
// caps on reg, and returns the shared Services (nil when Books is disabled). notifyHub
// receives finished-book / reading-monitor toasts. This is the old cmd/boomtime block
// verbatim — same kinds, same handler bodies, same caps, same gating.
func Register(reg *corejobs.Registry, database *db.DB, cfg *config.Config, notifyHub *notify.Hub, logger *slog.Logger) *Services {
	// The audible sync/backfill caps were set UNCONDITIONALLY in the old host block
	// (outside the BooksEnabled gate — a cap on a kind that only registers when Books
	// is on is harmless, and preserved here byte-identically).
	reg.SetConcurrency(audible.AudibleSyncKind, 1)     // audiobooks-audible-sync
	reg.SetConcurrency(audible.AudibleBackfillKind, 1) // audiobooks-audible-backfill (spec's "books-audible-backfill")

	if !cfg.BooksEnabled() {
		return nil
	}

	audioSvc := audible.New(database, amazon.NewStore(database), logger)
	audioSvc.SetNotify(notifyHub)
	audioSvc.SetHardcover(hardcover.NewStore(database))

	// Forward: fan over every connected user, delta-sync each. A
	// per-user error is logged + skipped so one bad credential
	// doesn't fail the batch.
	reg.Register(audible.AudibleSyncKind, corejobs.HandlerFunc(func(jctx context.Context, _ corejobs.Job) error {
		users, uerr := database.ListUsersWithAmazonDevice(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, serr := audioSvc.SyncUser(jctx, u); serr != nil {
				logger.Warn("audible forward: user sync failed", "user", u, "err", serr)
			}
		}
		logger.Info("audible forward: batch complete", "users", len(users))
		return nil
	}))

	// Backfill: one-shot per user (owner-scoped payload), enqueued
	// on demand from the connect flow / admin. Single attempt — an
	// all-time sweep is heavy and re-runnable by hand.
	reg.Register(audible.AudibleBackfillKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner == "" {
			return fmt.Errorf("audible backfill: missing owner")
		}
		return audioSvc.BackfillUser(jctx, job.Owner)
	}))

	// hardcover-push kind: mirror ONE finished book to Hardcover.
	// Split out of the audible-sync handler so all users' Hardcover
	// pushes share the single concurrency-capped queue (cap=1 below)
	// — Hardcover's rate limit is a global resource. The payload is
	// self-contained; owner falls back to the job's owner field.
	reg.Register(audible.HardcoverPushKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		var p audible.HardcoverPushPayload
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &p); err != nil {
				return fmt.Errorf("hardcover-push: bad payload: %w", err)
			}
		}
		if p.Owner == "" {
			p.Owner = job.Owner
		}
		return audioSvc.RunHardcoverPush(jctx, p)
	}))

	// hardcover-pull kind (boom-books): the INBOUND half of the
	// bidirectional Hardcover sync. Owner-scoped (needs the user's
	// token) — reads the shelf + reconciles each entry's status /
	// updated_at onto the matching reading_item's minimal linkage. No
	// local shelf mirror. Enqueued on demand from POST
	// /api/v1/hardcover/pull; capped at 1 below (shares Hardcover's
	// global rate limit with the push).
	hcPull := hardcover.NewSyncService(database, hardcover.NewStore(database), logger)
	reg.Register(hardcover.PullJobKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner != "" {
			_, perr := hcPull.SyncHardcoverPull(jctx, job.Owner)
			return perr
		}
		// Batch/scheduled variant (owner-less): fan the pull over every
		// Hardcover-connected user. A per-user error is logged + skipped so
		// one bad token doesn't fail the batch (mirrors AudibleSyncKind).
		users, uerr := database.ListUsersWithHardcoverKey(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, perr := hcPull.SyncHardcoverPull(jctx, u); perr != nil {
				logger.Warn("hardcover pull: user sync failed", "user", u, "err", perr)
			}
		}
		logger.Info("hardcover pull: batch complete", "users", len(users))
		return nil
	}))

	// hardcover-push-curation kind (boom-books, migration 00069): the
	// OUTBOUND half of a per-item curation edit. Enqueued by PATCH
	// /api/v1/books/items/:id/curation — mirrors the row's EFFECTIVE
	// status/rating/finish onto the user's Hardcover shelf (dry-run-gated).
	// Owner-scoped; capped at 1 below (shares Hardcover's global rate limit).
	// The books.Module also hands this SAME instance to its HTTP handler so the
	// manual per-row sync button can push INLINE (bypass this queue).
	hcCurationPush := hardcover.NewPushService(database, hardcover.NewStore(database), logger)
	reg.Register(hardcover.CurationPushKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		var p hardcover.CurationPushPayload
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &p); err != nil {
				return fmt.Errorf("hardcover-push-curation: bad payload: %w", err)
			}
		}
		if p.Owner == "" {
			p.Owner = job.Owner
		}
		return hcCurationPush.PushCuration(jctx, p)
	}))

	// catalyst-books (Kindle) — the ebook mirror of the Audible
	// wiring above. ONE Amazon device credential feeds both; Hardcover
	// resolves an ASIN → title/author/cover + book_id/edition_id.
	// Consolidated reading-monitor tuning (single source of truth) —
	// loaded once, threaded to the engine job + the non-engine poll.
	rmCfg := kindle.LoadMonitorConfig()
	kindleSvc := kindle.New(database, amazon.NewStore(database), logger).
		SetHardcover(hardcover.NewStore(database)).
		SetNotify(notifyHub). // persistent reading-monitor toasts
		SetMonitorConfig(rmCfg)

	// Forward: fan the periodic Kindle sync over every connected user;
	// a per-user error is logged + skipped so one bad credential
	// doesn't fail the batch (mirrors AudibleSyncKind).
	reg.Register(kindle.KindleSyncKind, corejobs.HandlerFunc(func(jctx context.Context, _ corejobs.Job) error {
		users, uerr := database.ListUsersWithAmazonDevice(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, serr := kindleSvc.SyncUser(jctx, u); serr != nil {
				logger.Warn("kindle forward: user sync failed", "user", u, "err", serr)
			}
		}
		logger.Info("kindle forward: batch complete", "users", len(users))
		return nil
	}))

	// Backfill: one-shot per user (owner-scoped payload), enqueued on
	// demand from the connect flow / admin (mirrors AudibleBackfillKind).
	reg.Register(kindle.KindleBackfillKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner == "" {
			return fmt.Errorf("kindle backfill: missing owner")
		}
		_, berr := kindleSvc.BackfillUser(jctx, job.Owner)
		return berr
	}))

	// books-kindle-insights kind: backfill per-book finish DATES
	// (+ store the streaks/goals snapshot) from the Kindle
	// Reading-Insights history onto the kindle reading_items the
	// library sync created. An owner-scoped job runs one user; an
	// owner-less (scheduled/batch) job fans over every connected user
	// — a per-user error is logged + skipped so one bad credential
	// doesn't fail the batch.
	reg.Register(kindle.KindleInsightsKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner != "" {
			_, ierr := kindleSvc.SyncInsights(jctx, job.Owner)
			return ierr
		}
		users, uerr := database.ListUsersWithAmazonDevice(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, serr := kindleSvc.SyncInsights(jctx, u); serr != nil {
				logger.Warn("kindle insights: user sync failed", "user", u, "err", serr)
			}
		}
		logger.Info("kindle insights: batch complete", "users", len(users))
		return nil
	}))

	// books-kindle-status-reconcile kind: the honest-STATUS sweep —
	// for every non-read kindle book, poll the CDE sidecar for a
	// last-page-read record and set status='reading' when one exists
	// (leaving un-opened books 'want'), since the Cloud Reader library
	// feed reports percentageRead=0 for everything. An owner-scoped job
	// runs one user; an owner-less (scheduled/batch) job fans over every
	// connected user — a per-user error is logged + skipped so one bad
	// credential doesn't fail the batch (mirrors KindleInsightsKind). It
	// NEVER clobbers a read/finished row, so running it after insights is
	// safe.
	reg.Register(kindle.KindleStatusReconcileKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner != "" {
			_, rerr := kindleSvc.ReconcileKindleStatus(jctx, job.Owner)
			return rerr
		}
		users, uerr := database.ListUsersWithAmazonDevice(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, rerr := kindleSvc.ReconcileKindleStatus(jctx, u); rerr != nil {
				logger.Warn("kindle status reconcile: user sweep failed", "user", u, "err", rerr)
			}
		}
		logger.Info("kindle status reconcile: batch complete", "users", len(users))
		return nil
	}))

	// books-kindle-reading-time kind: the FORWARD reading-TIME poll —
	// sample each in-progress kindle book's last-page-read position,
	// gap-sum consecutive samples into reading sessions, and write
	// reading-seconds into reading_activity(source='kindle') so Kindle
	// reading-time unifies with Audible listening-time under the reading
	// `seconds` measure. An owner-scoped job runs one user; an owner-less
	// (scheduled/batch) job fans over every connected user — a per-user
	// error is logged + skipped so one bad credential doesn't fail the
	// batch (mirrors KindleInsightsKind).
	reg.Register(kindle.KindleReadingTimeKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner != "" {
			_, rerr := kindleSvc.PollReadingTime(jctx, job.Owner)
			return rerr
		}
		users, uerr := database.ListUsersWithAmazonDevice(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, rerr := kindleSvc.PollReadingTime(jctx, u); rerr != nil {
				logger.Warn("kindle reading-time: user poll failed", "user", u, "err", rerr)
			}
		}
		logger.Info("kindle reading-time: batch complete", "users", len(users))
		return nil
	}))

	// books-reading-monitor kind (catalyst-books §5.1): the
	// PERSISTENT server-side two-level reading-monitor. Scheduled
	// leader-singleton on a short base tick (below); each run drives
	// an internal loop (RunMonitorLoop) that polls all in-progress
	// books coarsely (L1) + the actively-advancing ones finely (L2),
	// emitting metrics + Loki logs + owner-scoped toasts and landing
	// reading_activity — for every user with reading_monitor_enabled,
	// whether or not the admin panel is open. The internal loop gives
	// sub-minute L2 cadence despite the ~1-min scheduler granularity;
	// its budget is kept under the schedule period so runs don't pile
	// up (concurrency capped at 1 below).
	reg.Register(kindle.ReadingMonitorKind, corejobs.HandlerFunc(func(jctx context.Context, _ corejobs.Job) error {
		return kindleSvc.RunMonitorLoop(jctx, rmCfg)
	}))

	// hardcover-match kind (boom-books): the EXPLICIT match stage of
	// the pipeline (backfill → match → sync). Owner-scoped — resolve
	// every still-unmatched reading_item to a Hardcover
	// book_id/edition_id via the read-only ladder and cache the
	// linkage. Reuses the pull's SyncService (same Store + DB); capped
	// at 1 below so it shares Hardcover's global rate budget with the
	// pull + push.
	reg.Register(hardcover.HardcoverMatchKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		// Optional payload: {force:true} = the on-demand force-rematch that
		// ignores the 30d negative-cache window. Absent/empty → normal sweep.
		var p hardcover.MatchPayload
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &p); err != nil {
				return fmt.Errorf("hardcover-match: bad payload: %w", err)
			}
		}
		if job.Owner != "" {
			_, merr := hcPull.MatchUnmatched(jctx, job.Owner, p.Force)
			return merr
		}
		// Batch/scheduled variant (owner-less): fan the match sweep over
		// every Hardcover-connected user. Per-user error logged + skipped.
		users, uerr := database.ListUsersWithHardcoverKey(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, merr := hcPull.MatchUnmatched(jctx, u, p.Force); merr != nil {
				logger.Warn("hardcover match: user sweep failed", "user", u, "err", merr)
			}
		}
		logger.Info("hardcover match: batch complete", "users", len(users))
		return nil
	}))

	// books-sync-all kind (orchestrator): chain the whole
	// reading-sync pipeline in dependency order — audible ingest →
	// kindle ingest → hardcover match → hardcover pull — for one
	// owner, consolidating the four individual kinds into ONE. Reuses
	// the SAME service instances built above (audioSvc, kindleSvc,
	// hcPull) so there is exactly one shared instance set; the
	// ordering guarantee (match after both ingests, pull after match)
	// falls out of running the stages sequentially. A per-step error
	// is logged + recorded in the Summary but does NOT abort the
	// chain. An owner-scoped job runs the pipeline for job.Owner; an
	// owner-less (scheduled/batch) job fans it over every user with a
	// connected Amazon device. Capped at 1 below — it drives the same
	// global Hardcover rate budget as its constituent stages.
	booksPipeline := pipeline.New(pipeline.Steps{
		AudibleSync:    audioSvc.SyncUser,
		KindleSync:     kindleSvc.SyncUser,
		KindleInsights: kindleSvc.SyncInsights,
		KindleReconcile: func(jctx context.Context, owner string) (int, error) {
			res, rerr := kindleSvc.ReconcileKindleStatus(jctx, owner)
			return res.MarkedReading, rerr
		},
		Match: func(jctx context.Context, owner string) (int, error) {
			res, merr := hcPull.MatchUnmatched(jctx, owner, false)
			return res.Matched, merr
		},
		Pull: func(jctx context.Context, owner string) (int, error) {
			res, perr := hcPull.SyncHardcoverPull(jctx, owner)
			return res.Fetched, perr
		},
	}, logger)
	reg.Register(pipeline.BooksSyncAllKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
		if job.Owner != "" {
			_, rerr := booksPipeline.RunPipeline(jctx, job.Owner)
			return rerr
		}
		// Batch/scheduled variant: fan over every connected user. A
		// per-user pipeline error is logged + skipped so one bad
		// credential doesn't fail the whole batch.
		users, uerr := database.ListUsersWithAmazonDevice(jctx)
		if uerr != nil {
			return uerr
		}
		for _, u := range users {
			if _, rerr := booksPipeline.RunPipeline(jctx, u); rerr != nil {
				logger.Warn("books-sync-all: user pipeline failed", "user", u, "err", rerr)
			}
		}
		logger.Info("books-sync-all: batch complete", "users", len(users))
		return nil
	}))

	// Per-kind fleet-wide concurrency caps (books subset). Each external-API kind
	// shares ONE throttled queue across pods+users — Hardcover's rate limit is global.
	reg.SetConcurrency(audible.HardcoverPushKind, 1) // hardcover-push (global Hardcover rate limit)
	// ── catalyst-books LIBERATION (boom-w20s.14) ─────────────────────────────
	// Gated on LiberationEnabled(): books on AND the liberation flag on AND a
	// library path configured. When it is off, libSvc stays nil and NEITHER kind
	// registers — so an operator who flips only the flag, without a mounted
	// library, gets a dark feature rather than audiobooks written to the pod's
	// ephemeral layer.
	//
	// NOTE on heartbeating: no manual heartbeat is needed in the handlers. The
	// jobs executor runs a background ticker for the whole duration of a handler
	// (internal/jobs/exec.go startHeartbeat), which is what keeps a 20-minute
	// download from being reaped. The service's Progress callback is for
	// reporting, not for staying alive.
	svcs := &Services{audio: audioSvc, hcCuration: hcCurationPush}
	if libSvc := buildLiberation(cfg, database, notifyHub, logger); libSvc != nil {
		svcs.liberation = libSvc

		// One book, end to end.
		reg.Register(liberate.LiberateBookKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
			var p liberate.BookPayload
			if len(job.Payload) > 0 {
				if err := json.Unmarshal(job.Payload, &p); err != nil {
					return fmt.Errorf("books-liberate-book: bad payload: %w", err)
				}
			}
			if p.Owner == "" {
				p.Owner = job.Owner
			}
			if p.Owner == "" || p.ASIN == "" {
				return fmt.Errorf("books-liberate-book: owner and asin are required")
			}
			_, err := libSvc.LiberateBook(jctx, p.Owner, p.ASIN, liberate.Options{Force: p.Force})
			return err
		}))

		// The sweep: fan the owner's pending library out into per-book jobs.
		reg.Register(liberate.LiberateSweepKind, corejobs.HandlerFunc(func(jctx context.Context, job corejobs.Job) error {
			var p liberate.SweepPayload
			if len(job.Payload) > 0 {
				if err := json.Unmarshal(job.Payload, &p); err != nil {
					return fmt.Errorf("books-liberate-sweep: bad payload: %w", err)
				}
			}
			if p.Owner == "" {
				p.Owner = job.Owner
			}
			if p.Owner == "" {
				return fmt.Errorf("books-liberate-sweep: missing owner")
			}
			pending, err := libSvc.LiberateAll(jctx, p.Owner, p.Limit)
			if err != nil {
				return err
			}
			if svcs.libEnq == nil {
				return fmt.Errorf("books-liberate-sweep: no job enqueuer wired")
			}
			var enqueued int
			for _, asin := range pending {
				payload, merr := json.Marshal(liberate.BookPayload{Owner: p.Owner, ASIN: asin, Force: p.Force})
				if merr != nil {
					continue
				}
				if _, eerr := svcs.libEnq.Enqueue(jctx, liberate.LiberateBookKind, payload, corejobs.Owner(p.Owner)); eerr != nil {
					// One book failing to enqueue must not abandon the rest of
					// the library; the next sweep picks it up again.
					logger.Warn("liberate sweep: enqueue failed", "owner", p.Owner, "asin", asin, "err", eerr)
					continue
				}
				enqueued++
			}
			logger.Info("liberate sweep: complete", "owner", p.Owner, "pending", len(pending), "enqueued", enqueued)
			return nil
		}))

		// Cap 2 by default, NOT 1. Unlike the Hardcover kinds — capped at 1
		// because Hardcover's rate limit is a GLOBAL resource — liberation is
		// bounded by disk and CDN throughput, so a small fan-out is safe and
		// meaningfully faster. Configurable via BOOM_BOOKS_LIBERATE_CONCURRENCY.
		conc := cfg.BooksLiberateConcurrency
		if conc < 1 {
			conc = 1
		}
		reg.SetConcurrency(liberate.LiberateBookKind, conc)
		reg.SetConcurrency(liberate.LiberateSweepKind, 1)

		// OFFLOAD the per-book kind to the scale-to-zero worker fleet (boom-piig).
		// DeriveKindFilter reads this: the always-on server stops claiming the
		// kind automatically, and the KEDA ScaledJob's drain pods claim it. That
		// is what turns BOOM_BOOKS_LIBERATE_CONCURRENCY from "5 goroutines in one
		// pod on one node" into work spread across the cluster.
		//
		// Declared HERE rather than in cmd/boomtime/main.go (where avatar-render
		// and label-image still are) because execution policy belongs to the
		// domain that owns the kind.
		//
		// The SWEEP kind is deliberately NOT offloaded: it only enqueues rows,
		// finishes in milliseconds, and needs neither the library mount nor the
		// scratch volume that make a drain pod expensive to schedule.
		//
		// PAIRED WITH keda-scaledjob-jobs.yaml — the kind must ALSO appear in that
		// file's BOOM_JOBS_KINDS and in its postgres trigger query, or the server
		// stops claiming it and nothing else starts. See the plan's §1.3.
		reg.SetOffload(liberate.LiberateBookKind)
		logger.Info("jobs: liberation handlers registered",
			"libraryPath", cfg.BooksLibraryPath, "concurrency", conc)
	}

	reg.SetConcurrency(hardcover.CurationPushKind, 1)       // hardcover-push-curation (global Hardcover rate limit)
	reg.SetConcurrency(hardcover.PullJobKind, 1)            // hardcover-pull (global Hardcover rate limit)
	reg.SetConcurrency(kindle.KindleSyncKind, 1)            // books-kindle-sync
	reg.SetConcurrency(kindle.KindleBackfillKind, 1)        // books-kindle-backfill
	reg.SetConcurrency(kindle.KindleInsightsKind, 1)        // books-kindle-insights
	reg.SetConcurrency(kindle.KindleStatusReconcileKind, 1) // books-kindle-status-reconcile
	reg.SetConcurrency(kindle.KindleReadingTimeKind, 1)     // books-kindle-reading-time
	reg.SetConcurrency(kindle.ReadingMonitorKind, 1)        // books-reading-monitor (leader-singleton engine)
	reg.SetConcurrency(hardcover.HardcoverMatchKind, 1)     // hardcover-match (global Hardcover rate limit)
	reg.SetConcurrency(pipeline.BooksSyncAllKind, 1)        // books-sync-all orchestrator (chains the rate-limited stages)

	logger.Info("jobs: audiobooks handlers registered", "audibleSyncEnabled", cfg.AudibleSyncEnabled())
	return svcs
}

// buildLiberation constructs the liberation service, or returns nil when the
// feature is off or its storage cannot be opened.
//
// A BAD LIBRARY PATH RETURNS NIL rather than panicking or registering a kind
// that fails on every job: liberate.NewFSSink deliberately refuses a root that
// does not exist (an unmounted volume), and the right response at wiring time is
// a loud log plus a dark feature, exactly as an absent ffmpeg is handled.
func buildLiberation(cfg *config.Config, database *db.DB, notifyHub *notify.Hub, logger *slog.Logger) *liberate.Service {
	if !cfg.LiberationEnabled() {
		return nil
	}
	sink, err := liberate.NewFSSink(cfg.BooksLibraryPath)
	if err != nil {
		logger.Error("jobs: liberation DISABLED — library path unusable",
			"path", cfg.BooksLibraryPath, "err", err)
		return nil
	}
	dec := liberate.NewFFmpegDecryptor(cfg.BooksFfmpegPath)
	// Probe at wiring time so a missing ffmpeg is a startup ERROR rather than a
	// surprise on the first book. Non-fatal: the kinds still register, because
	// an operator who installs ffmpeg and restarts should not also have to
	// rediscover why liberation vanished.
	if perr := dec.Available(context.Background()); perr != nil {
		logger.Error("jobs: ffmpeg is NOT usable — liberation jobs will fail until it is installed",
			"ffmpegPath", cfg.BooksFfmpegPath, "err", perr)
	}
	return &liberate.Service{
		Store:     liberate.NewStore(database.Pool),
		Amazon:    amazon.NewStore(database),
		Sink:      sink,
		Decryptor: dec,
		Logger:    logger,
		WorkDir:   cfg.BooksWorkPath,
		Template:  cfg.BooksNamingTemplate,
		// nil-safe: a nil hub means no toast, exactly as the audiobooks service
		// treats it.
		Notify: notifyHub,
	}
}

// RegisterSchedules registers the catalyst-books leader-singleton schedules on sched,
// each gated exactly as the old cmd/boomtime scheduler block (audible forward sync /
// persistent reading-monitor / periodic Hardcover pull + match). The host owns the
// outer "build the scheduler at all" condition + go sched.Run.
func RegisterSchedules(ctx context.Context, sched *corejobs.Scheduler, cfg *config.Config, logger *slog.Logger) {
	// Audible forward sync (boom-books): leader-singleton via the DB,
	// so running the schedule on every server is safe. The backfill
	// kind is NOT scheduled — it's enqueued on demand.
	if cfg.AudibleSyncEnabled() {
		if serr := sched.Register(ctx, audible.AudibleSyncKind, cfg.AudibleSyncInterval); serr != nil {
			logger.Warn("jobs: audible schedule register failed", "err", serr)
		}
	}
	// Persistent reading-monitor (catalyst-books §5.1): re-arm the
	// engine each schedule period (MonitorConfig.ScheduleInterval). The
	// re-armed job internally loops at BaseTick for RunBudget, so the
	// effective L2 cadence is sub-minute even though the scheduler fires
	// ~1/min. Leader-singleton via ClaimDueSchedules → exactly one runs
	// fleet-wide. Enabled per-user via reading_monitor_enabled.
	if cfg.BooksEnabled() {
		schedInterval := kindle.LoadMonitorConfig().ScheduleInterval
		if serr := sched.Register(ctx, kindle.ReadingMonitorKind, schedInterval); serr != nil {
			logger.Warn("jobs: reading-monitor schedule register failed", "err", serr)
		}
	}
	// Periodic Hardcover pull + match (leader-singleton via the DB, so
	// running the schedule on every server is safe). Both are the
	// owner-less/batch variants (they fan over every Hardcover-connected
	// user). The pull refreshes the shelf mirror + reconciles linkage; the
	// match re-runs the ladder (incl. the LOCAL shelf rung) so a newly-
	// shelved book auto-links within the interval — no manual re-match.
	if cfg.HardcoverSyncEnabled() {
		if serr := sched.Register(ctx, hardcover.PullJobKind, cfg.HardcoverSyncInterval); serr != nil {
			logger.Warn("jobs: hardcover pull schedule register failed", "err", serr)
		}
		if serr := sched.Register(ctx, hardcover.HardcoverMatchKind, cfg.HardcoverSyncInterval); serr != nil {
			logger.Warn("jobs: hardcover match schedule register failed", "err", serr)
		}
	}
}
