// rollup_path.go is the ONE place that owns two intertwined rules:
//
//  1. THE GAP CUTOFF. "A gap longer than DefaultTimeLimitMinutes between two
//     heartbeats doesn't count as time spent." Every SQL comparison against
//     gap_seconds derives from DefaultTimeLimitMinutes — either as a caller-
//     supplied minutes param (`gap_seconds <= ($n * 60)`) or as the bound
//     defaultGapCutoffSeconds for the queries that lock to the default
//     (rollup refresh, leaderboards, time-today, total-project-time,
//     time-between, derived-status). No query restates 900 or 15 as a literal.
//
//  2. THE ROLLUP-VS-RAW FORK. hb_rollup_daily pre-aggregates heartbeats at the
//     default cutoff for the axes in RollupAxes. It can serve a request iff
//     (a) the request uses the default cutoff, (b) no hide rule targets an
//     axis the rollup lacks (impossible today: LoadHiddenSets only admits
//     hiddenAxes, all of which are rollup axes — pinned by
//     rollup_path_test.go), and (c) no requested-Space rule targets an axis
//     the rollup lacks (REACHABLE today: Space rules validate against the
//     ExploreColumn whitelist, which includes `entity`). rollupEligible is
//     the only expression of that fork; handlers call the dispatchers below
//     and never choose a side themselves.
//
//     KNOWN GAP (discovered while pinning this): routing an entity-rule Space
//     to the raw path is currently output-equivalent to staying on the
//     rollup, because inclusionPredicate only arms the 8 dashboard axes — an
//     entity arm is never built, so an entity-only requested space matches
//     NOTHING on either path (see rollup_path_test.go). The gate keeps the
//     (c) leg anyway: it is the documented contract, and it becomes
//     load-bearing the moment entity arms are actually implemented.
//
// Editing DefaultTimeLimitMinutes recompiles the cutoff into every read query,
// the ingest-time rollup refresh, the API's timeLimit default, and the fast-
// path gate — but it does NOT rewrite already-materialized hb_rollup_daily
// rows. Those were summed at the old cutoff and stay stale until the next
// per-sender RefreshRollup (ingest or PATCH-timezone forces one). Rebuild the
// rollup after changing it.
package db

import (
	"context"
	"time"
)

// DefaultTimeLimitMinutes is the canonical gap cutoff in MINUTES: a gap longer
// than this between consecutive heartbeats is not attributed as time spent.
// This is the app default for the ?timeLimit query param, the only cutoff the
// public profile / widgets / awards surfaces use, and the cutoff
// hb_rollup_daily is materialized at.
const DefaultTimeLimitMinutes int64 = 15

// defaultGapCutoffSeconds is the same cutoff in the unit gap_seconds is stored
// in. Derived — never restate it.
const defaultGapCutoffSeconds int64 = DefaultTimeLimitMinutes * 60

// rollupEligible is THE rollup-vs-raw selection rule (see the file comment).
// True means hb_rollup_daily reproduces the raw heartbeats scan byte-for-byte
// (pinned by rollup_parity_test.go), so the dispatchers may take the fast path.
func rollupEligible(limitMinutes int64, hs HiddenSets, ms MemberSets, spaceRequested bool) bool {
	return limitMinutes == DefaultTimeLimitMinutes &&
		!hs.HasHiddenOutside(RollupAxes) &&
		(!spaceRequested || !ms.HasMemberOutside(RollupAxes))
}

// UserActivity returns the per-day activity StatRows for one user, serving
// from the pre-aggregated rollup when rollupEligible and falling back to the
// raw heartbeats scan otherwise (non-default limit, or a requested-Space rule
// on an axis the rollup lacks — entity today). tz is used by the raw path
// only; the rollup's day column is already in the sender's TZ.
func (d *DB) UserActivity(ctx context.Context, user string, start, end time.Time, limitMinutes int64, tz string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]StatRow, error) {
	if rollupEligible(limitMinutes, hs, ms, spaceRequested) {
		return d.GetUserActivityRollup(ctx, user, start, end, hs, rs, ms, spaceRequested)
	}
	return d.GetUserActivity(ctx, user, start, end, limitMinutes, tz, hs, rs, ms, spaceRequested)
}

// CategoryDaily returns per-day-per-category time, rollup fast path when
// eligible (category IS a rollup axis, so the output shape is identical).
func (d *DB) CategoryDaily(ctx context.Context, sender string, start, end time.Time, limitMinutes int64, tz string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]CategoryDailyRow, error) {
	if rollupEligible(limitMinutes, hs, ms, spaceRequested) {
		return d.GetCategoryDailyRollup(ctx, sender, start, end, hs, rs, ms, spaceRequested)
	}
	return d.GetCategoryDaily(ctx, sender, start, end, limitMinutes, tz, hs, rs, ms, spaceRequested)
}

// Momentum returns per-project weekly momentum, rollup fast path when eligible
// (project is a rollup axis; per-day sums roll up to weeks identically).
func (d *DB) Momentum(ctx context.Context, sender string, start, end time.Time, limitMinutes int64, tz string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]MomentumRow, error) {
	if rollupEligible(limitMinutes, hs, ms, spaceRequested) {
		return d.GetMomentumRollup(ctx, sender, start, end, hs, rs, ms, spaceRequested)
	}
	return d.GetMomentum(ctx, sender, start, end, limitMinutes, tz, hs, rs, ms, spaceRequested)
}

// Leaderboards returns the cross-user leaderboard. It has no timeLimit knob —
// the raw query binds the default cutoff — so eligibility reduces to the
// requester's hide/Space rules staying within the rollup axes.
func (d *DB) Leaderboards(ctx context.Context, start, end time.Time, requester string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]LeaderboardRow, error) {
	if rollupEligible(DefaultTimeLimitMinutes, hs, ms, spaceRequested) {
		return d.GetLeaderboardsRollup(ctx, start, end, requester, hs, rs, ms, spaceRequested)
	}
	return d.GetLeaderboards(ctx, start, end, requester, hs, rs, ms, spaceRequested)
}
