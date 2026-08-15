// reading_time.go — the FORWARD Kindle reading-TIME composition (gaka-books):
// poll each in-progress book's last-page-read POSITION over time, gap-sum
// consecutive samples into reading SESSIONS, and write reading-seconds into
// reading_activity(source='kindle') so Kindle reading-time unifies with Audible
// listening-time under the reading `seconds` measure (internal/query/domains.go).
//
// This is the heartbeat model applied to reading position instead of edit
// activity. The coding rollup gap-sums heartbeats: consecutive heartbeats within
// the 15-minute gap cutoff (internal/stats leaderboards / bigbets) count as one
// continuous session; a larger gap breaks the session. We mirror that exactly:
//
//   - Consecutive position samples within KindleSessionGap are the same session;
//     the reading-seconds for the interval is the wall-clock delta between them.
//   - A gap larger than KindleSessionGap breaks the session — the idle span is
//     NOT counted.
//   - The position must ADVANCE (change) across the pair for the interval to
//     count as reading. A static position within the gap = the reader stepped
//     away with the book open — idle, not reading (mirrors the heartbeat
//     "activity" requirement: no edit → no time).
//
// composeSessions is a PURE function (samples -> per-day reading-seconds) so the
// gap model is exhaustively table-tested without a DB or network. Day-crossing
// intervals are split at UTC midnight so each day's bucket is exact.
package books

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// positionSource is the narrow last-page-read wire surface PollReadingTime
// depends on. The real implementation is *amazon.KindleSidecarClient; tests
// supply a fake so the composition + storage path exercises without a network
// (and independently of the pending sidecar wire shape).
type positionSource interface {
	FetchLastPagePosition(ctx context.Context, cred *amazon.DeviceCredential, asin string) (position int64, sampledAt time.Time, ok bool, err error)
}

// KindleSessionGap is the DEFAULT max wall-clock gap between two position samples
// for the interval between them to count as one continuous reading session. It
// matches the coding heartbeat gap cutoff (15 min) so a "reading session" and a
// "coding session" mean the same thing on the fused calendar. The runtime value is
// MonitorConfig.SessionGap (this const is its default + the value the pure-function
// table tests compose under); withDefaults folds the same 15m in.
const KindleSessionGap = 15 * time.Minute

// PositionSample is one observed last-page-read position for a book. The pure
// composition consumes these (the db.KindleReadingPosition rows map onto them).
type PositionSample struct {
	Position  int64
	SampledAt time.Time
}

// DailyReadingSeconds is composed reading time attributed to one UTC calendar day.
type DailyReadingSeconds struct {
	Day     time.Time // UTC midnight of the day
	Seconds int64
}

// composeSessions gap-sums one book's ordered position samples into per-day
// reading-seconds. For each consecutive pair (a, b):
//
//   - delta = b.SampledAt - a.SampledAt
//   - counts as reading IFF delta > 0 && delta <= gap && b.Position != a.Position
//   - the counted delta is attributed to the UTC day(s) the [a, b] interval spans
//     (split at midnight for a day-crossing interval)
//
// Samples are sorted defensively; duplicate timestamps (delta 0) contribute
// nothing. Output is ordered by day ascending.
func composeSessions(samples []PositionSample, gap time.Duration) []DailyReadingSeconds {
	if len(samples) < 2 {
		return nil
	}
	ordered := append([]PositionSample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SampledAt.Before(ordered[j].SampledAt) })

	byDay := map[time.Time]int64{}
	for i := 1; i < len(ordered); i++ {
		a, b := ordered[i-1], ordered[i]
		delta := b.SampledAt.Sub(a.SampledAt)
		if delta <= 0 || delta > gap {
			continue // duplicate/out-of-order, or a session break (idle span)
		}
		if b.Position == a.Position {
			continue // position didn't advance → idle, not reading
		}
		splitIntervalByDay(a.SampledAt.UTC(), b.SampledAt.UTC(), byDay)
	}

	out := make([]DailyReadingSeconds, 0, len(byDay))
	for day, secs := range byDay {
		if secs > 0 {
			out = append(out, DailyReadingSeconds{Day: day, Seconds: secs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}

// splitIntervalByDay adds the seconds of [start, end] into `into`, keyed by UTC
// midnight, splitting at each day boundary the interval crosses so per-day
// buckets are exact. start/end must already be UTC and start < end.
func splitIntervalByDay(start, end time.Time, into map[time.Time]int64) {
	for start.Before(end) {
		day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
		nextMidnight := day.AddDate(0, 0, 1)
		segEnd := end
		if nextMidnight.Before(segEnd) {
			segEnd = nextMidnight
		}
		into[day] += int64(segEnd.Sub(start).Seconds())
		start = segEnd
	}
}

// PollReadingTime is the job-handler body for KindleReadingTimeKind. For the
// owner's in-progress kindle books it (1) polls the current last-page-read
// position via the sidecar and appends a sample, then (2) recomposes every
// in-progress book's recent samples into per-day reading-seconds, summed ACROSS
// books, and upserts them into reading_activity(source='kindle'). Returns the
// number of NEW position samples captured this run.
//
// Idempotency: step 2 recomputes each day bucket in the lookback window from the
// full sample history and OVERWRITES it (UpsertReadingActivity is keyed by
// owner+source+bucket_date+granularity). Re-running with no new samples writes
// identical values — never double-counts. Cross-book same-day reading sums into
// ONE bucket (the seconds measure groups by source, not book), so one bucket per
// day carries the day's total kindle reading time.
func (s *Service) PollReadingTime(ctx context.Context, owner string) (int, error) {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return 0, err
	}

	items, err := s.DB.ListReadingItems(ctx, owner, source)
	if err != nil {
		return 0, err
	}

	// In-progress kindle books only — a finished/unstarted book has no live
	// reading position to poll.
	inProgress := make([]db.ReadingItem, 0, len(items))
	for _, it := range items {
		if it.Status == "reading" && it.ExternalID != "" {
			inProgress = append(inProgress, it)
		}
	}

	// (1) Poll + append a sample per in-progress book. The sidecar returns a
	// SNAPSHOT (current furthest-page-read + Amazon's creationTime for it); an
	// unchanged creationTime dedupes to no new row via the (owner,asin,sampled_at)
	// unique index, so re-polling a book nobody touched captures nothing.
	sampled := 0
	now := time.Now().UTC()
	for _, it := range inProgress {
		if err := ctx.Err(); err != nil {
			return sampled, err
		}
		pos, at, ok, ferr := s.sidecar.FetchLastPagePosition(ctx, cred, it.ExternalID)
		if ferr != nil {
			// A per-book fetch/parse error is logged + skipped so one bad book
			// doesn't fail the sweep.
			s.logWarn(ctx, "kindle reading-time: position fetch failed", "user", owner, "asin", it.ExternalID, "err", ferr)
			continue
		}
		if !ok {
			continue // clean miss — no position recorded yet (a stateless book 404s)
		}
		// creationTime is the sample's EVENT time (Amazon's own timestamp for when
		// the furthest position was set — more accurate than poll time). Fall back
		// to poll time only if the wire value was empty/unparseable.
		if at.IsZero() {
			at = now
		}
		inserted, ierr := s.DB.InsertKindleReadingPosition(ctx, owner, it.ExternalID, pos, at.UTC())
		if ierr != nil {
			return sampled, ierr
		}
		if inserted {
			sampled++
		}
	}

	// (2) Recompose every in-progress book's recent samples → per-day seconds,
	// summed across books, and upsert one reading_activity bucket per day.
	buckets, err := s.recomposeReadingActivity(ctx, owner, inProgress, s.monitorCfg, now)
	if err != nil {
		return sampled, err
	}

	s.logInfo(ctx, "kindle reading-time: poll complete",
		"user", owner,
		"inProgress", len(inProgress),
		"samplesCaptured", sampled,
		"dayBucketsWritten", buckets,
	)
	return sampled, nil
}

// recomposeReadingActivity gap-sums every in-progress book's recent position
// samples into per-day reading-seconds, summed ACROSS books, and upserts one
// reading_activity(source='kindle') bucket per day. It is the reading_time
// COMPOSITION shared by the periodic PollReadingTime job and the persistent
// reading-monitor (monitor.go) so both land reading_activity identically —
// neither reinvents the gap model. Idempotent: it OVERWRITES each day bucket in
// the lookback window from the full sample history, so re-running with no new
// samples writes identical values (UpsertReadingActivity keys on
// owner+source+bucket_date+granularity). Returns the number of day buckets
// written.
//
// Method selection (PART 3, gaka-0gdp): it classifies the owner's observed advance
// cadence (RecommendIntervals) and SELECTS the composition method — session-boundary
// ⇒ position-delta (Δlocation × reading-speed), else ⇒ the default temporal gap-sum.
// Because classification stays 'unknown' until high-fidelity calibration data lands,
// gap-sum remains active in practice; the switch activates automatically the moment
// calibration classifies session-boundary. The method used is logged.
func (s *Service) recomposeReadingActivity(ctx context.Context, owner string, inProgress []db.ReadingItem, cfg MonitorConfig, now time.Time) (int, error) {
	cfg = cfg.withDefaults()
	since := now.Add(-cfg.ReadingTimeLookback)

	// Pick the composition method from the owner's observed advance cadence.
	method := MethodGapSum
	secPerLoc := cfg.DefaultSecPerLocation
	pairs, perr := s.DB.ListRecentReadingMonitorAdvances(ctx, owner, now.Add(-cfg.RecommendLookback), cfg.WindowCap)
	if perr != nil {
		return 0, perr
	}
	if rec := RecommendIntervals(pairs, cfg); rec != nil {
		if rec.ImpliedMethod == MethodPositionDelta {
			method = MethodPositionDelta
		}
		if obs := observedSecPerLocation(pairs); obs > 0 {
			secPerLoc = obs // prefer the measured reading-speed over the default
		}
	}

	byDay := map[time.Time]int64{}
	for _, it := range inProgress {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		rows, lerr := s.DB.ListKindleReadingPositions(ctx, owner, it.ExternalID, since)
		if lerr != nil {
			return 0, lerr
		}
		samples := toPositionSamples(rows)
		var days []DailyReadingSeconds
		if method == MethodPositionDelta {
			days = composeSessionsPositionDelta(samples, secPerLoc)
		} else {
			days = composeSessions(samples, cfg.SessionGap)
		}
		for _, d := range days {
			byDay[d.Day] += d.Seconds
		}
	}

	s.logInfo(ctx, "kindle reading-time: recomposed",
		"owner", owner, "method", method, "secPerLocation", secPerLoc, "days", len(byDay))

	buckets := 0
	for day, secs := range byDay {
		if err := ctx.Err(); err != nil {
			return buckets, err
		}
		if uerr := s.DB.UpsertReadingActivity(ctx, db.ReadingActivity{
			Owner:            owner,
			Source:           source, // "kindle"
			Granularity:      "day",
			BucketDate:       day,
			ListeningSeconds: secs, // "activity seconds" — reading time, the text analogue of Audible listening_seconds
		}); uerr != nil {
			return buckets, uerr
		}
		buckets++
	}
	return buckets, nil
}

// toPositionSamples maps db rows onto the pure composition's input type.
func toPositionSamples(rows []db.KindleReadingPosition) []PositionSample {
	out := make([]PositionSample, len(rows))
	for i, r := range rows {
		out[i] = PositionSample{Position: r.Position, SampledAt: r.SampledAt}
	}
	return out
}

// composeSessionsPositionDelta composes reading-seconds by the POSITION-DELTA
// method (§5.1 session-boundary composition). For each consecutive advancing pair
// (a, b):
//
//   - dloc = b.Position - a.Position (only a forward move counts)
//   - reading-seconds = round(dloc × secPerLocation)
//   - attributed WHOLLY to the UTC day of b.SampledAt — b's creationTime is the
//     advance's anchor (when the furthest position was set)
//
// Unlike gap-sum it needs NO intra-session poll cadence: it reconstructs time from
// how far the position moved, so it works when whispersync only writes the LPR at
// session boundaries (device close/open) and there are no mid-session samples to
// sum. secPerLocation is the observed reading-speed (median interval/dloc) or a
// configured default. PURE (samples -> per-day seconds), mirroring composeSessions
// for the same exhaustive table-testing. Output is ordered by day ascending.
func composeSessionsPositionDelta(samples []PositionSample, secPerLocation float64) []DailyReadingSeconds {
	if len(samples) < 2 || secPerLocation <= 0 {
		return nil
	}
	ordered := append([]PositionSample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SampledAt.Before(ordered[j].SampledAt) })

	byDay := map[time.Time]int64{}
	for i := 1; i < len(ordered); i++ {
		a, b := ordered[i-1], ordered[i]
		dloc := b.Position - a.Position
		if dloc <= 0 {
			continue // no forward advance → no reading time
		}
		secs := int64(math.Round(float64(dloc) * secPerLocation))
		if secs <= 0 {
			continue
		}
		bt := b.SampledAt.UTC()
		day := time.Date(bt.Year(), bt.Month(), bt.Day(), 0, 0, 0, 0, time.UTC)
		byDay[day] += secs
	}

	out := make([]DailyReadingSeconds, 0, len(byDay))
	for day, secs := range byDay {
		if secs > 0 {
			out = append(out, DailyReadingSeconds{Day: day, Seconds: secs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}

// observedSecPerLocation returns the median observed seconds-per-location over the
// advance samples (interval_secs / dloc for each pair with a positive dloc), or 0
// when no usable sample exists — the position-delta method's measured reading-speed,
// preferred over the configured default. Order-independent (it sorts internally).
func observedSecPerLocation(pairs []db.KindleAdvancePair) float64 {
	rates := make([]float64, 0, len(pairs))
	for _, p := range pairs {
		if p.IntervalSecs > 0 && p.DLoc > 0 {
			rates = append(rates, p.IntervalSecs/float64(p.DLoc))
		}
	}
	if len(rates) == 0 {
		return 0
	}
	sort.Float64s(rates)
	return percentile(rates, 50)
}
