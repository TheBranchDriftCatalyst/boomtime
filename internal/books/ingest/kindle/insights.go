// insights.go — the Kindle Reading-Insights ingest: fetch the reading HISTORY
// (per-book finish DATES + streaks/goals/achievements) and backfill it onto the
// reading_items rows the Cloud Reader library sync (ingest.go) created.
//
// WHY this exists: the Cloud Reader library feed carries NO per-book timestamps,
// so every kindle reading_items.finished_at is otherwise null/un-windowable. The
// insights endpoint (internal/amazon/insights.go) is the missing finish-DATE
// source — goal_info.titles_read[] gives (asin, date_read) back to ~2020.
//
// Pipeline:
//
//  1. device credential → ExchangeWebsiteCookies (SAME website-cookie auth as the
//     library sync — reused verbatim; no ADP signing, no CSRF token).
//  2. FetchKindleInsights → typed insights (titles_read + streaks + raw snapshot).
//  3. store the raw snapshot verbatim (kindle_reading_insights) so streaks/goals/
//     achievements are retained for a future surface without a schema now.
//  4. per title_read: match the existing kindle reading_items row by ASIN and set
//     finished_at = date_read (COALESCE — never clobber a richer existing date).
//  5. log ONE summary line (titles / matched / dates backfilled) — no per-book
//     flood; the line inherits the running job's job_id via logInfo(ctx, …).
package kindle

import (
	"context"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// SyncInsights fetches the user's Kindle Reading-Insights, stores the raw
// snapshot, and backfills finish DATES onto their existing kindle reading_items.
// Returns the number of rows whose finished_at was NEWLY set from insights (the
// backfilled-date count). Idempotent — a re-run over already-dated rows re-stores
// the snapshot and backfills nothing new (COALESCE guards the dates). This is the
// job-handler body for KindleInsightsKind.
//
// A title with no matching library row yet (its Cloud Reader row hasn't synced)
// is skipped, NOT created — the library sync owns row creation; this ingest only
// dates rows that already exist. So run it AFTER KindleSyncKind (the pipeline
// orders it that way).
func (s *Service) SyncInsights(ctx context.Context, owner string) (int, error) {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return 0, err
	}

	cookies, err := s.kindle.ExchangeWebsiteCookies(ctx, cred)
	if err != nil {
		_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusInvalid)
		return 0, err
	}
	ins, err := s.kindle.FetchKindleInsights(ctx, cookies)
	if err != nil {
		_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusInvalid)
		return 0, err
	}
	_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusValid)

	// Store the raw snapshot verbatim (streaks/goals/achievements retained for a
	// future surface). A store failure is non-fatal — the finish-date backfill,
	// the actual point of this job, still runs.
	if len(ins.Raw) > 0 {
		if serr := s.DB.UpsertKindleReadingInsights(ctx, owner, ins.Raw); serr != nil {
			s.logWarn(ctx, "kindle insights: snapshot store failed", "user", owner, "err", serr)
		}
	}

	backfilled := 0
	matched := 0
	skippedNoDate := 0
	for _, t := range ins.TitlesRead {
		// Stop promptly on cancellation — a multi-year history is a large loop.
		if err := ctx.Err(); err != nil {
			return backfilled, err
		}
		if t.ASIN == "" || t.DateRead.IsZero() {
			skippedNoDate++
			continue // un-keyable or undated → nothing to backfill
		}
		newlyDated, found, serr := s.DB.SetReadingItemFinishedFromInsights(ctx, owner, t.ASIN, t.DateRead)
		if serr != nil {
			return backfilled, serr
		}
		if found {
			matched++
		}
		if newlyDated {
			backfilled++
		}
	}

	// kindle-minutes (gaka-books): the forward reading-TIME path now lives in
	// reading_time.go (PollReadingTime / KindleReadingTimeKind) — it polls each
	// in-progress book's last-page-read POSITION, gap-sums consecutive samples
	// into reading SESSIONS, and writes reading-seconds into
	// reading_activity(source='kindle'), the text analogue of Audible's
	// listening_seconds. It is a SEPARATE job from this finish-date backfill (a
	// position poll wants a much tighter cadence than the yearly-history ingest),
	// so it deliberately does NOT run here — this seam is intentionally left as a
	// pointer, not an inline call.

	s.logInfo(ctx, "kindle insights: backfilled finish dates",
		"user", owner,
		"titlesRead", len(ins.TitlesRead),
		"matched", matched,
		"datesBackfilled", backfilled,
		"skippedNoDate", skippedNoDate,
		"currentDailyStreak", ins.CurrentDailyStreak.Duration,
		"isBackfillCompleted", ins.IsBackfillCompleted,
	)
	return backfilled, nil
}
