// loc.go — lines-of-code aggregations (gaka-yfg), derived from
// heartbeats.file_lines (the file's total line count at edit time). NO GitHub
// dependency.
//
// The raw file_lines numbers are INFLATED by generated/vendored/lock files
// (node_modules, dist, *.lock, *.pb.go, …) — a single node_modules tree can
// dwarf every hand-written line. locIgnorePatterns is therefore MANDATORY: it
// is spliced into every LOC scan as `entity NOT ILIKE ALL($n)` so those paths
// never reach the sum. See loc_test.go for the before/after de-inflation proof.
package db

import (
	"context"
	"math"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/jackc/pgx/v5"
)

// locIgnorePatterns is the generated/vendored/lock ignore set, expressed as
// case-insensitive SQL LIKE patterns matched against the full `entity` path.
// A row is kept only when its entity matches NONE of these (`NOT ILIKE ALL`).
// Some patterns overlap (yarn.lock also matches %.lock) — redundancy is
// harmless under ALL/NOT and keeps the set self-documenting against the spec.
var locIgnorePatterns = []string{
	// Vendored / generated directories (path segment anywhere).
	"%node_modules/%",
	"%/vendor/%",
	"%/dist/%",
	"%/build/%",
	"%/.venv/%",
	"%/.git/%",
	"%/target/%",
	"%/.next/%",
	"%site-packages/%",
	// Minified / lock / generated files (filename suffix).
	"%.min.%",
	"%.lock",
	"%-lock.json",
	"%package-lock.json",
	"%yarn.lock",
	"%pnpm-lock.yaml",
	"%.pb.go",
	"%_pb2.py",
	"%.generated.%",
	"%.snap",
	// Data files (gaka-yfg follow-up, user call): JSON/CSV/etc. are DATA, not
	// hand-written code — generated caches, fixtures, dumps, exports. These
	// dominate LOC for data-heavy projects (e.g. catalyst-data's 137k-line
	// ner_pass.json / pipeline-cache). YAML is deliberately KEPT (talos-homelab
	// et al. are infra-as-code); flip that here if that ever changes.
	"%.json",
	"%.jsonl",
	"%.ndjson",
	"%.csv",
	"%.tsv",
}

// locMaxOverTimePoints bounds the over-time series: the daily cumulative curve
// is downsampled so a multi-year range never emits thousands of points (mirrors
// useBucketedDaily's "weekly buckets on long ranges" intent). ~90 points keeps
// the payload small while staying visually smooth.
const locMaxOverTimePoints = 90

// locDay is one (day, cumulative-loc) sample returned by the over-time scan.
type locDay struct {
	day time.Time
	loc int64
}

// GetProjectLoc returns each project's CURRENT lines of code within [t0,t1]:
// the sum over the project's files of each file's most-recent file_lines
// (DISTINCT ON (project, entity) latest by time_sent). The generated/vendored
// ignore filter is applied, plus the curation hide exclusion and the optional
// Space scope (both via applyScopes on the raw heartbeats columns). Renames are
// intentionally NOT applied — LOC groups on the raw project name (documented on
// the payload). Returns the per-project rows (loc desc) and their grand total.
func (d *DB) GetProjectLoc(ctx context.Context, owner string, t0, t1 time.Time, hs HiddenSets, ms MemberSets, spaceRequested bool) ([]model.LocProject, int64, error) {
	// $1 owner, $2 start, $3 end, $4 ignore patterns (text[]). Scopes start $5.
	args := []any{owner, t0, t1, locIgnorePatterns}
	query := `
SELECT project, CAST(sum(file_lines) AS int8) AS loc
FROM (
    SELECT DISTINCT ON (project, entity) project, file_lines
    FROM heartbeats
    WHERE sender = $1 AND ty = 'file'
      AND file_lines IS NOT NULL
      AND project IS NOT NULL AND project <> ''
      AND entity IS NOT NULL AND entity <> ''
      AND time_sent >= $2 AND time_sent <= $3
      AND entity NOT ILIKE ALL($4::text[])
    ORDER BY project, entity, time_sent DESC
) latest
GROUP BY project
ORDER BY loc DESC, project ASC`

	query, args, _ = applyScopes(query, "AND time_sent <= $3",
		hs, ms, spaceRequested, rawHeartbeatCols, args, 5)

	out := []model.LocProject{}
	var total int64
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var p model.LocProject
			if err := rows.Scan(&p.Project, &p.Loc); err != nil {
				return err
			}
			out = append(out, p)
			total += p.Loc
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// GetLocOverTime returns the total lines-of-code across all of the owner's
// files as it grew, sampled across [t0,t1] and bounded to
// locMaxOverTimePoints points.
//
// Cost bound (gaka-yfg): the expensive part is "for each day D, the sum over
// files of each file's most-recent file_lines with time_sent <= end-of-D".
// Doing that as N per-day as-of scans would table-scan the whole corpus per
// day. Instead this is ONE window-function pass:
//
//  1. Collapse to one row per (entity, day) = that day's latest line count.
//  2. Per file, LAG() the previous KNOWN day's count → a per-day DELTA
//     (file_lines - prev), so a file that grew 100→150 contributes +50 that day
//     and a brand-new file contributes its full count.
//  3. Sum deltas per day, then a running SUM() OVER (ORDER BY day) yields the
//     cumulative corpus total at each day.
//
// The scan intentionally includes ALL history up to t1 (not just >= t0) so the
// cumulative total already carries each file's pre-range baseline; only the
// in-range days are emitted. The single pass runs under the elevated work_mem
// (aggQuery), and Go then carry-forward downsamples the daily curve to
// <= locMaxOverTimePoints so the payload never balloons on all-time ranges.
func (d *DB) GetLocOverTime(ctx context.Context, owner string, t0, t1 time.Time, hs HiddenSets, ms MemberSets, spaceRequested bool) ([]model.LocPoint, error) {
	// $1 owner, $2 end (t1), $3 ignore patterns. Scopes start at $4. t0 is NOT a
	// SQL param — it only gates the Go-side windowing in bucketLocDaily; the scan
	// deliberately reads ALL history up to t1 so pre-range baselines survive.
	args := []any{owner, t1, locIgnorePatterns}
	query := `
WITH per_file_day AS (
    SELECT DISTINCT ON (entity, (time_sent)::date) entity,
           (time_sent)::date AS day,
           file_lines
    FROM heartbeats
    WHERE sender = $1 AND ty = 'file'
      AND file_lines IS NOT NULL
      AND entity IS NOT NULL AND entity <> ''
      AND time_sent <= $2
      AND entity NOT ILIKE ALL($3::text[])
    ORDER BY entity, (time_sent)::date, time_sent DESC
),
deltas AS (
    SELECT day,
           file_lines - COALESCE(
               LAG(file_lines) OVER (PARTITION BY entity ORDER BY day), 0
           ) AS delta
    FROM per_file_day
),
daily AS (
    SELECT day, sum(delta) AS day_delta
    FROM deltas
    GROUP BY day
),
cum AS (
    SELECT day, CAST(sum(day_delta) OVER (ORDER BY day) AS int8) AS loc
    FROM daily
)
-- Return the cumulative value for EVERY active day up to t1 (including
-- pre-range days). bucketLocDaily windows to [t0,t1] and carry-forwards, so a
-- file established before t0 and never touched in-range still primes the curve
-- at its baseline value rather than vanishing (a day-in-range filter here would
-- drop that baseline entirely).
SELECT day, loc
FROM cum
ORDER BY day`

	query, args, _ = applyScopes(query, "AND time_sent <= $2",
		hs, ms, spaceRequested, rawHeartbeatCols, args, 4)

	var daily []locDay
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var dl locDay
			if err := rows.Scan(&dl.day, &dl.loc); err != nil {
				return err
			}
			daily = append(daily, dl)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return bucketLocDaily(daily, t0, t1), nil
}

// bucketLocDaily downsamples the daily cumulative curve to at most
// locMaxOverTimePoints evenly-spaced points across [t0,t1], carrying the last
// known cumulative LOC forward across gaps (days with no activity). This
// mirrors the FE's ~weekly bucketing for long ranges: bucketDays scales with
// the span so short ranges stay daily and multi-year ranges collapse to weekly
// / monthly steps. Exported-for-test via loc_test.go in-package.
func bucketLocDaily(daily []locDay, t0, t1 time.Time) []model.LocPoint {
	if len(daily) == 0 {
		return []model.LocPoint{}
	}
	d0 := time.Date(t0.Year(), t0.Month(), t0.Day(), 0, 0, 0, 0, time.UTC)
	d1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 0, 0, 0, 0, time.UTC)
	if d1.Before(d0) {
		d0, d1 = d1, d0
	}
	spanDays := int(d1.Sub(d0).Hours()/24) + 1
	bucketDays := int(math.Ceil(float64(spanDays) / float64(locMaxOverTimePoints)))
	if bucketDays < 1 {
		bucketDays = 1
	}

	out := []model.LocPoint{}
	di := 0
	var lastLoc int64
	haveLast := false
	for cursor := d0; !cursor.After(d1); cursor = cursor.AddDate(0, 0, bucketDays) {
		bucketEnd := cursor.AddDate(0, 0, bucketDays-1)
		if bucketEnd.After(d1) {
			bucketEnd = d1
		}
		// Advance through every daily point at/through this bucket's end,
		// keeping the most recent LOC (the snapshot at bucketEnd).
		for di < len(daily) && !daily[di].day.After(bucketEnd) {
			lastLoc = daily[di].loc
			haveLast = true
			di++
		}
		if !haveLast {
			// No data at/before this bucket yet — skip (curve hasn't started).
			continue
		}
		out = append(out, model.LocPoint{Date: bucketEnd.Format("2006-01-02"), Loc: lastLoc})
	}
	return out
}
