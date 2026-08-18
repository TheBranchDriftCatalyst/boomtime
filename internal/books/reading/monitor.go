// monitor.go — the PERSISTENT server-side two-level Kindle reading-monitor
// (catalyst-books §5.1). Unlike the admin WS probe (internal/admin/books_monitor.go),
// which only runs while a browser tab is mounted, this engine is driven by the
// leader-singleton scheduler (books-reading-monitor kind) so it runs whether or
// not the panel is open: a user flips reading_monitor_enabled on, walks away, and
// comes back to the full report.
//
// THE TWO-LEVEL "LIMIT HACK" (§5.1). Amazon never pushes and the sidecar is
// per-book, so learning "is anyone reading right now?" means polling every
// in-progress book — an ADP-signed call each. That per-book detect sweep is the
// cost floor we minimize:
//
//	L1 (detect, coarse)  — poll ALL in-progress books every DetectInterval (T1,
//	                       default 120s) to spot which one ADVANCED.
//	L2 (capture, fine)   — a book that advanced is "active": poll ONLY it every
//	                       CaptureInterval (T2, default 30s) for accurate session
//	                       cadence, until no advance for IdleGap (G, default 300s)
//	                       → it drops back to L1.
//
// This collapses cost from "every book fast, always" to "every book slow + the
// 1–2 books actually being read fast, only while they're read."
//
// creationTime nuance (§5.1): the kindle.lpr record carries Amazon's OWN event
// time for the furthest-page-read. We anchor state + histograms on it (not our
// poll time), so a coarse poll never loses total-reading DETECTION — a larger T1
// costs toast latency, not data. It also lets us tell a genuine live advance
// (fresh creationTime) from the first sight of a stale position (old
// creationTime), so enabling the monitor over a library of long-finished books
// doesn't fire a flurry of false "Reading:" toasts.
//
// On each detected advance the engine: (a) records the pinned Prometheus metrics,
// (b) emits the structured Loki log `reading monitor: advance`, (c) toasts via
// notify.Hub honoring the per-user mode (debounced ↔ verbose), and (d) appends a
// position sample + drives the EXISTING reading_time composition
// (recomposeReadingActivity) so reading_activity(source='kindle') lands — it does
// NOT reinvent the gap model.
//
// The engine is DB-state-driven (kindle_reading_monitor_state), so it is
// stateless across ticks/pods and safe under the leader-singleton scheduler.
package reading

import (
	"context"
	"fmt"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/notify"
)

// The engine's tuning now lives in MonitorConfig (monitorconfig.go) — the single
// source of truth for every reading-monitor knob + coefficient. RunMonitorLoop /
// RunMonitorOnce / runMonitorPass all take a MonitorConfig.

// monitorPassResult is the per-owner outcome of one evaluation pass — returned
// for tests (and folded into the scheduler-run totals).
type monitorPassResult struct {
	Polled      int // books actually polled this pass (were "due")
	Advances    int // genuine advances detected this pass
	ActiveBooks int // owner's books in L2 after the pass
}

// RunMonitorLoop is the books-reading-monitor job-handler body. Because the jobs
// scheduler fires at ~1-minute granularity but L2 wants sub-minute capture, one
// job run drives an INTERNAL loop: it runs a full monitor pass every baseTick
// until budget elapses (or ctx is cancelled), then returns so the next scheduled
// job re-arms it. Leader-singleton (the scheduler enqueues once per period) +
// concurrency cap 1 mean exactly one of these runs fleet-wide. A per-pass error
// is logged and the loop continues — one bad tick never aborts the run.
func (s *Service) RunMonitorLoop(ctx context.Context, cfg MonitorConfig) error {
	cfg = cfg.withDefaults()
	baseTick, budget := cfg.BaseTick, cfg.RunBudget
	deadline := time.Now().Add(budget)
	for {
		if _, err := s.RunMonitorOnce(ctx, cfg, time.Now().UTC()); err != nil {
			s.logWarn(ctx, "reading monitor: pass failed", "err", err)
		}
		if budget <= 0 || !time.Now().Before(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(baseTick):
		}
	}
}

// RunMonitorOnce runs one evaluation pass for EVERY enabled user at time `now`,
// then sets the process-global active-books gauge from the DB. Returns the total
// advances detected across users. A per-user pass error is logged + skipped so
// one bad credential doesn't fail the sweep.
func (s *Service) RunMonitorOnce(ctx context.Context, cfg MonitorConfig, now time.Time) (int, error) {
	users, err := s.DB.ListReadingMonitorUsers(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, u := range users {
		if ctx.Err() != nil {
			break
		}
		res, perr := s.runMonitorPass(ctx, u, cfg, now)
		if perr != nil {
			s.logWarn(ctx, "reading monitor: user pass failed", "user", u, "err", perr)
			continue
		}
		total += res.Advances
	}
	// Fleet-wide gauge: the count of books currently in L2 across all users.
	if n, gerr := s.DB.CountActiveKindleMonitorBooksGlobal(ctx); gerr == nil {
		metrics.ReadingMonitorActiveBooks.WithLabelValues(source).Set(float64(n))
	}
	return total, nil
}

// runMonitorPass is ONE evaluation pass for one owner at time `now`. It is the
// tested unit: `now` is injected (no wall clock), the sidecar is the swappable
// positionSource, and all state lives in the DB — so the whole L1/L2/idle state
// machine, the advance metrics, and the toast modes are exercised deterministically
// without a network. A DISABLED user is a no-op (returns the zero result).
func (s *Service) runMonitorPass(ctx context.Context, owner string, cfg MonitorConfig, now time.Time) (monitorPassResult, error) {
	cfg = cfg.withDefaults()
	var res monitorPassResult

	enabled, mode, err := s.DB.GetReadingMonitorSettings(ctx, owner)
	if err != nil {
		return res, err
	}
	if !enabled {
		return res, nil // monitor off for this user — nothing to do
	}
	verbose := mode == db.ReadingMonitorModeVerbose

	// Calibration window (PART 2): while now < calibrating_until, poll ALL
	// in-progress books at the high-fidelity CalibrationInterval instead of the
	// L1/L2 cadence — normal 60–120s polling ALIASES the sub-60s whispersync
	// cadence, so the optimal-timing recommendation needs a temporary burst. Auto-
	// expires: once calibrating_until passes this test is false and the pass reverts
	// to L1/L2 with no manual step.
	calibratingUntil, err := s.DB.GetReadingMonitorCalibration(ctx, owner)
	if err != nil {
		return res, err
	}
	calibrating := calibratingUntil != nil && now.Before(*calibratingUntil)

	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		// No connected Amazon credential — a no-op, not a sweep failure (mirrors
		// the other kindle jobs' per-user tolerance).
		return res, nil
	}

	items, err := s.DB.ListReadingItems(ctx, owner, source)
	if err != nil {
		return res, err
	}
	inProgress := inProgressKindle(items)
	if len(inProgress) == 0 {
		return res, nil
	}

	states, err := s.DB.ListKindleMonitorStates(ctx, owner)
	if err != nil {
		return res, err
	}

	sampledAny := false
	for _, it := range inProgress {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		asin := it.ExternalID
		st, existed := states[asin]
		st.Owner, st.ASIN = owner, asin

		// Idle-expiry (independent of poll-due): an active book with no advance for
		// IdleGap drops back to L1. No toast — the absence of activity is not an
		// "observed change", and suppressing it keeps the debounced/verbose toast
		// counts about advances only.
		if st.Active && st.LastAdvanceAt != nil && !now.Before(st.LastAdvanceAt.Add(cfg.IdleGap)) {
			st.Active = false
		}

		// Two-level cadence: an active book polls at T2, else T1. A book is "due"
		// when now >= last_polled_at + its level interval (or never polled). While
		// the user is calibrating, EVERY in-progress book polls at the high-fidelity
		// CalibrationInterval regardless of L1/L2 — the burst that de-aliases the
		// whispersync cadence.
		interval := cfg.DetectInterval
		if st.Active {
			interval = cfg.CaptureInterval
		}
		if calibrating {
			interval = cfg.CalibrationInterval
		}
		if st.LastPolledAt != nil && now.Before(st.LastPolledAt.Add(interval)) {
			// Not due this tick — persist any idle-expiry flip and move on.
			if existed {
				if uerr := s.DB.UpsertKindleMonitorState(ctx, st); uerr != nil {
					return res, uerr
				}
			}
			continue
		}

		// Record the poll ATTEMPT time now so a failing/clean-miss book still backs
		// off to its interval instead of being hammered every base tick.
		polledAt := now
		st.LastPolledAt = &polledAt
		res.Polled++

		pos, at, ok, ferr := s.sidecar.FetchLastPagePosition(ctx, cred, asin)
		if ferr != nil {
			s.logWarn(ctx, "reading monitor: position fetch failed", "user", owner, "asin", asin, "err", ferr)
			if uerr := s.DB.UpsertKindleMonitorState(ctx, st); uerr != nil {
				return res, uerr
			}
			continue
		}
		if !ok {
			// Clean miss (404 — no position recorded). Persist the poll time.
			if uerr := s.DB.UpsertKindleMonitorState(ctx, st); uerr != nil {
				return res, uerr
			}
			continue
		}

		if pos <= st.LastLocation {
			// No advance (or a regression we ignore). Persist the poll time only.
			if uerr := s.DB.UpsertKindleMonitorState(ctx, st); uerr != nil {
				return res, uerr
			}
			continue
		}

		// A higher position than we last saw. eventTime is Amazon's creationTime
		// (the true event time) when available, else our poll time.
		eventTime := at
		if eventTime.IsZero() {
			eventTime = now
		}
		dloc := pos - st.LastLocation
		freshEvent := at.IsZero() || now.Sub(at) <= cfg.IdleGap

		// First sight of a STALE position (never-tracked book whose furthest read
		// happened long ago) — anchor state, but do NOT treat it as a live advance:
		// no metric, no toast, no active flip. The anchor sample lets a LATER real
		// advance compose against it (the gap will exclude the stale span).
		if !existed && !freshEvent {
			st.LastLocation = pos
			st.LastAdvanceAt = &eventTime
			if _, ierr := s.DB.InsertKindleReadingPosition(ctx, owner, asin, pos, eventTime.UTC()); ierr != nil {
				return res, ierr
			}
			sampledAny = true
			if uerr := s.DB.UpsertKindleMonitorState(ctx, st); uerr != nil {
				return res, uerr
			}
			continue
		}

		// ── Genuine advance ──────────────────────────────────────────────────
		res.Advances++
		metrics.ReadingMonitorAdvancesTotal.WithLabelValues(source).Inc()

		// wasActive gates the INTRA-session interval signal: an interval is only
		// meaningful when the book was already in a live session (still active). A
		// session-START advance (wasActive false — first sight, or the first advance
		// after an idle-expiry) has no intra-session predecessor, so its gap to the
		// stale last_advance_at is a cross-session boundary, NOT a cadence sample —
		// excluded from the histogram, the reading-seconds counter, and the
		// recommendation window.
		wasActive := st.Active
		intervalS := 0.0
		if wasActive && st.LastAdvanceAt != nil {
			if gap := eventTime.Sub(*st.LastAdvanceAt); gap > 0 {
				intervalS = gap.Seconds()
				metrics.ReadingMonitorAdvanceInterval.WithLabelValues(source).Observe(intervalS)
				if dloc > 0 {
					metrics.ReadingMonitorSecPerLocation.WithLabelValues(source).Observe(intervalS / float64(dloc))
				}
				// Only an in-session gap (<= the composition's session cutoff) is
				// reading-time. Increment the Domain-board counter by exactly those
				// new seconds so it stays monotonic despite the idempotent bucket
				// overwrite below (composeSessions attributes the same seconds).
				if gap <= cfg.SessionGap {
					metrics.ReadingActivitySecondsTotal.WithLabelValues(source).Add(intervalS)
				}
				// Persist the sample so the admin endpoint can derive the interval
				// recommendation (p50/p90) — the queryable twin of the histogram.
				if aerr := s.DB.InsertReadingMonitorAdvance(ctx, owner, source, intervalS, dloc, eventTime.UTC()); aerr != nil {
					return res, aerr
				}
			}
		}

		title := it.Title
		if title == "" {
			title = asin
		}
		s.logInfo(ctx, "reading monitor: advance",
			"owner", owner, "source", source, "book", title, "asin", asin,
			"location", pos, "dloc", dloc, "creation_time", eventTime.UTC().Format(time.RFC3339),
			"interval_s", intervalS,
		)

		// Toast policy: verbose = a toast on EVERY advance; debounced = one toast
		// per advancing book per session (the idle→active START edge), coalescing
		// the burst of advances that follow.
		if s.Notify != nil {
			if verbose {
				s.publishAdvanceToast(owner, title, asin, pos, dloc, source)
			} else if !wasActive {
				s.publishAdvanceToast(owner, title, asin, pos, dloc, source)
			}
		}

		if _, ierr := s.DB.InsertKindleReadingPosition(ctx, owner, asin, pos, eventTime.UTC()); ierr != nil {
			return res, ierr
		}
		sampledAny = true

		st.LastLocation = pos
		st.LastAdvanceAt = &eventTime
		st.Active = true
		if uerr := s.DB.UpsertKindleMonitorState(ctx, st); uerr != nil {
			return res, uerr
		}
	}

	// Land reading_activity(source='kindle') via the SHARED reading_time
	// composition — only when this pass captured a new sample (else the buckets
	// are already correct + this saves a recompute sweep).
	if sampledAny {
		if _, rerr := s.recomposeReadingActivity(ctx, owner, inProgress, cfg, now); rerr != nil {
			return res, rerr
		}
	}

	if n, cerr := s.DB.CountActiveKindleMonitorBooks(ctx, owner); cerr == nil {
		res.ActiveBooks = n
	}
	return res, nil
}

// publishAdvanceToast fans an owner-scoped "Reading: <title> +N loc" toast
// through the notify hub. Fire-and-forget (the hub drops on a slow subscriber).
func (s *Service) publishAdvanceToast(owner, title, asin string, location, dloc int64, src string) {
	s.Notify.Publish(notify.Event{
		Type:  "reading.monitor.advance",
		Owner: owner,
		Title: "Reading: " + title,
		Body:  fmt.Sprintf("+%d loc", dloc),
		Data: map[string]any{
			"asin":     asin,
			"location": location,
			"dloc":     dloc,
			"source":   src,
		},
	})
}

// inProgressKindle filters reading_items to the pollable in-progress set: status
// 'reading' with a non-empty ASIN (external_id). Shared by the engine.
func inProgressKindle(items []db.ReadingItem) []db.ReadingItem {
	out := make([]db.ReadingItem, 0, len(items))
	for _, it := range items {
		if it.Status == "reading" && it.ExternalID != "" {
			out = append(out, it)
		}
	}
	return out
}
