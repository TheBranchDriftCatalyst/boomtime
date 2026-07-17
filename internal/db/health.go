// health.go persists Apple Watch / HealthKit data:
//   - Workouts land in `heartbeats` with ty='workout' + workout_* columns
//     (so the existing rollup + time-spent aggregations pick them up for
//     free) and a companion row in `workout_details` for the deep HR series
//     and GPS route.
//   - Raw samples (HR / steps / energy / HRV / sleep / mindful) land in
//     `health_samples`, aggregated in `health_rollup_daily` on ingest.
package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/jackc/pgx/v5"
)

// SaveWorkouts persists a batch of workouts as heartbeats (+ workout_details
// rows). Runs atomically inside a single transaction: heartbeat insert, detail
// insert, gap/rollup refresh all commit or roll back together. Returns the
// assigned heartbeat ids in input order.
func (d *DB) SaveWorkouts(ctx context.Context, owner string, workouts []model.WorkoutPayload) ([]int64, error) {
	if len(workouts) == 0 {
		return []int64{}, nil
	}

	// Translate WorkoutPayload -> HeartbeatPayload so we can reuse the exact
	// same insert path (and pick up gap_seconds / rollup for free). We tag ty
	// with model.WorkoutType so downstream queries can filter workouts in or
	// out. `entity` uses the HKWorkout uuid to satisfy the unique_heartbeats
	// constraint (entity, sender, time_sent) — two workouts starting at the
	// same second with different uuids won't collide.
	hbs := make([]model.HeartbeatPayload, len(workouts))
	for i, w := range workouts {
		w := w
		kind := w.Kind
		dur := w.DurationS
		entity := "workout:" + w.SourceUUID
		ua := "boomtime-watch/1 (macOS) boomtime-watch/1"
		// project bucket = user-supplied Label when set, otherwise fall back to
		// raw kind so pre-label clients keep their existing project labels.
		project := kind
		if w.Label != nil {
			if trimmed := *w.Label; trimmed != "" {
				project = trimmed
			}
		}
		hbs[i] = model.HeartbeatPayload{
			Sender:           &owner,
			UserAgent:        ua,
			Entity:           entity,
			Type:             model.WorkoutType,
			TimeSent:         w.Start,
			Project:          &project,
			WorkoutKind:      &kind,
			WorkoutDurationS: &dur,
			WorkoutKcal:      w.Kcal,
			WorkoutAvgHR:     w.AvgHR,
			WorkoutDistanceM: w.DistanceM,
		}
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := insertProjectsBatch(ctx, tx, hbs); err != nil {
		return nil, err
	}
	ids, err := insertHeartbeatsBatch(ctx, tx, hbs)
	if err != nil {
		return nil, err
	}

	// Companion workout_details rows for HR series + route. Both are stored
	// as JSONB; empty series/route stays JSON null rather than "[]" to make
	// the "no route recorded" case cheap to detect at read time.
	var db pgx.Batch
	for i, w := range workouts {
		var hrJSON, routeJSON []byte
		if len(w.HRSeries) > 0 {
			hrJSON, _ = json.Marshal(w.HRSeries)
		}
		if len(w.Route) > 0 {
			routeJSON, _ = json.Marshal(w.Route)
		}
		db.Queue(`
INSERT INTO workout_details (heartbeat_id, source_uuid, hr_series, route)
VALUES ($1, $2, $3, $4)
ON CONFLICT (heartbeat_id) DO UPDATE SET
    source_uuid = EXCLUDED.source_uuid,
    hr_series   = EXCLUDED.hr_series,
    route       = EXCLUDED.route`,
			ids[i], w.SourceUUID, jsonbOrNil(hrJSON), jsonbOrNil(routeJSON))
	}
	br := tx.SendBatch(ctx, &db)
	for i := 0; i < db.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return nil, err
		}
	}
	if err := br.Close(); err != nil {
		return nil, err
	}

	// Rollup + gap refresh, mirroring SaveHeartbeats. Rollup now honors
	// workout_duration_s (see refreshRollup in ingest.go).
	earliest := unixToTime(workouts[0].Start)
	for _, w := range workouts[1:] {
		t := unixToTime(w.Start)
		if t.Before(earliest) {
			earliest = t
		}
	}
	if err := recomputeGaps(ctx, tx, owner, earliest); err != nil {
		return nil, err
	}
	if err := refreshRollup(ctx, tx, owner, earliest); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

// SaveHealthSamples persists raw HealthKit samples and refreshes the daily
// rollup for the affected day range. Idempotent: the unique index on
// (owner, kind, ts_start, coalesce(ts_end, ts_start)) makes re-syncs safe
// after a lost anchor.
func (d *DB) SaveHealthSamples(ctx context.Context, owner string, samples []model.HealthSamplePayload) (int, error) {
	if len(samples) == 0 {
		return 0, nil
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Resolve workout_uuid -> workout_id (heartbeats.id) for samples that
	// declare a parent workout. Missing/stale uuids simply store NULL so a
	// late-arriving sample doesn't fail the whole batch — the sample is still
	// useful without the FK.
	uuidSet := map[string]struct{}{}
	for _, s := range samples {
		if s.WorkoutUUID != nil && *s.WorkoutUUID != "" {
			uuidSet[*s.WorkoutUUID] = struct{}{}
		}
	}
	uuidToID := map[string]int64{}
	if len(uuidSet) > 0 {
		uuids := make([]string, 0, len(uuidSet))
		for u := range uuidSet {
			uuids = append(uuids, u)
		}
		rows, err := tx.Query(ctx,
			`SELECT source_uuid, heartbeat_id FROM workout_details WHERE source_uuid = ANY($1)`,
			uuids)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var u string
			var id int64
			if err := rows.Scan(&u, &id); err != nil {
				rows.Close()
				return 0, err
			}
			uuidToID[u] = id
		}
		rows.Close()
	}

	var batch pgx.Batch
	earliestDay := unixToTime(samples[0].TsStart)
	for _, s := range samples {
		var tsEnd *time.Time
		if s.TsEnd != nil {
			t := unixToTime(*s.TsEnd)
			tsEnd = &t
		}
		var workoutID *int64
		if s.WorkoutUUID != nil {
			if id, ok := uuidToID[*s.WorkoutUUID]; ok {
				workoutID = &id
			}
		}
		var metaJSON []byte
		if len(s.Meta) > 0 {
			metaJSON, _ = json.Marshal(s.Meta)
		}
		ts := unixToTime(s.TsStart)
		if ts.Before(earliestDay) {
			earliestDay = ts
		}
		batch.Queue(`
INSERT INTO health_samples (owner, kind, unit, qty, q_min, q_avg, q_max, ts_start, ts_end, meta, workout_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT ON CONSTRAINT idx_health_samples_dedupe DO NOTHING`,
			owner, s.Kind, s.Unit, s.Qty, s.QMin, s.QAvg, s.QMax,
			ts, tsEnd, jsonbOrNil(metaJSON), workoutID)
	}
	br := tx.SendBatch(ctx, &batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return 0, err
		}
	}
	if err := br.Close(); err != nil {
		return 0, err
	}

	if err := refreshHealthRollup(ctx, tx, owner, earliestDay); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(samples), nil
}

// refreshHealthRollup rebuilds health_rollup_daily for `owner`'s affected days
// (>= the date of `since`). Delete+insert atomic within the caller's tx.
func refreshHealthRollup(ctx context.Context, q execer, owner string, since time.Time) error {
	if _, err := q.Exec(ctx,
		`DELETE FROM health_rollup_daily WHERE owner = $1 AND day >= $2::date`, owner, since); err != nil {
		return err
	}
	// Sleep/mindful have ts_end and we want the duration in seconds; other
	// kinds use qty directly. total_qty / avg_qty are populated for every
	// kind so downstream aggregations don't have to switch by kind.
	_, err := q.Exec(ctx, `
INSERT INTO health_rollup_daily (owner, day, kind, total_qty, avg_qty, min_qty, max_qty, sample_count)
SELECT owner,
       ts_start::date AS day,
       kind,
       CASE
         WHEN kind IN ('sleep_stage', 'mindful')
           THEN COALESCE(SUM(EXTRACT(EPOCH FROM (ts_end - ts_start))), 0)
         ELSE COALESCE(SUM(COALESCE(qty, q_avg)), 0)
       END AS total_qty,
       AVG(COALESCE(qty, q_avg))         AS avg_qty,
       MIN(COALESCE(qty, q_min))         AS min_qty,
       MAX(COALESCE(qty, q_max))         AS max_qty,
       COUNT(*)                          AS sample_count
FROM health_samples
WHERE owner = $1 AND ts_start >= $2::date
GROUP BY owner, ts_start::date, kind`, owner, since)
	return err
}

// GetHealthActivity aggregates one row per day between t0 and t1 for the
// Wellness card feed. Workouts come from heartbeats+workout_details; the
// other columns come from health_rollup_daily.
func (d *DB) GetHealthActivity(ctx context.Context, owner string, t0, t1 time.Time) (model.HealthActivityPayload, error) {
	var payload model.HealthActivityPayload

	// Workout counts + minutes per day from heartbeats.
	workoutRows, err := d.Pool.Query(ctx, `
SELECT time_sent::date AS day,
       COUNT(*) AS workouts,
       COALESCE(SUM(workout_duration_s), 0) / 60.0 AS minutes,
       COALESCE(SUM(workout_kcal), 0) AS kcal_from_workouts
FROM heartbeats
WHERE sender = $1 AND ty = $2 AND time_sent >= $3 AND time_sent < $4
GROUP BY time_sent::date`, owner, string(model.WorkoutType), t0, t1)
	if err != nil {
		return payload, err
	}
	byDay := map[string]*model.HealthActivityDay{}
	for workoutRows.Next() {
		var day time.Time
		var count int64
		var minutes, kcal float64
		if err := workoutRows.Scan(&day, &count, &minutes, &kcal); err != nil {
			workoutRows.Close()
			return payload, err
		}
		k := day.Format("2006-01-02")
		byDay[k] = &model.HealthActivityDay{
			Day:            k,
			Workouts:       count,
			WorkoutMinutes: minutes,
			ActiveKcal:     kcal,
		}
	}
	workoutRows.Close()

	// Everything else from the rollup. total_qty already carries the right
	// semantics per kind (see refreshHealthRollup).
	sampleRows, err := d.Pool.Query(ctx, `
SELECT day, kind, total_qty, avg_qty
FROM health_rollup_daily
WHERE owner = $1 AND day >= $2::date AND day < $3::date`, owner, t0, t1)
	if err != nil {
		return payload, err
	}
	for sampleRows.Next() {
		var day time.Time
		var kind string
		var totalQty, avgQty *float64
		if err := sampleRows.Scan(&day, &kind, &totalQty, &avgQty); err != nil {
			sampleRows.Close()
			return payload, err
		}
		k := day.Format("2006-01-02")
		d := byDay[k]
		if d == nil {
			d = &model.HealthActivityDay{Day: k}
			byDay[k] = d
		}
		switch kind {
		case "active_energy":
			if totalQty != nil {
				d.ActiveKcal += *totalQty // may add to workout-inline kcal
			}
		case "steps":
			if totalQty != nil {
				d.Steps = int64(*totalQty)
			}
		case "heart_rate":
			if avgQty != nil {
				d.AvgHR = *avgQty
			}
		case "resting_heart_rate":
			if avgQty != nil {
				d.RestingHR = *avgQty
			}
		case "sleep_stage":
			if totalQty != nil {
				d.SleepMinutes = *totalQty / 60.0
			}
		case "hrv":
			if avgQty != nil {
				d.HRVMs = *avgQty
			}
		case "mindful":
			if totalQty != nil {
				d.MindfulMinutes = *totalQty / 60.0
			}
		}
	}
	sampleRows.Close()

	// Sort by day ascending and compute totals.
	payload.Days = make([]model.HealthActivityDay, 0, len(byDay))
	keys := make([]string, 0, len(byDay))
	for k := range byDay {
		keys = append(keys, k)
	}
	sortStringsAsc(keys)
	for _, k := range keys {
		payload.Days = append(payload.Days, *byDay[k])
	}
	payload.HasData = len(payload.Days) > 0
	payload.Totals = summariseDays(payload.Days)
	return payload, nil
}

// WorkoutType is the ty value we stamp on workout heartbeats. Kept as a
// db-package constant (rather than a model.EntityType) so the raw string is
// the source of truth and query params can bind it directly.
const WorkoutType = "workout"

// GetWorkouts returns every workout event between t0 and t1 for `owner`, plus
// a per-label aggregate summary. Powers the Wellness page's event breakdown
// (group by label — every annotated workout becomes its own bucket).
// project is the canonical Label field (SaveWorkouts routes WorkoutPayload.Label
// -> heartbeats.project; falls back to raw kind when no label was given).
func (d *DB) GetWorkouts(ctx context.Context, owner string, t0, t1 time.Time) (model.WorkoutListPayload, error) {
	var payload model.WorkoutListPayload

	rows, err := d.Pool.Query(ctx, `
SELECT h.id,
       COALESCE(h.workout_kind, '')            AS kind,
       COALESCE(h.project, h.workout_kind, '') AS label,
       EXTRACT(EPOCH FROM h.time_sent)         AS start_unix,
       COALESCE(h.workout_duration_s, 0)       AS duration_s,
       h.workout_kcal, h.workout_avg_hr, h.workout_distance_m,
       COALESCE(wd.source_uuid, '')            AS source_uuid
FROM heartbeats h
LEFT JOIN workout_details wd ON wd.heartbeat_id = h.id
WHERE h.sender = $1 AND h.ty = $2
  AND h.time_sent >= $3 AND h.time_sent < $4
ORDER BY h.time_sent DESC`,
		owner, string(model.WorkoutType), t0, t1)
	if err != nil {
		return payload, err
	}
	defer rows.Close()

	// byLabel accumulates one entry per Label seen. Kind is the most-recent
	// raw kind we saw for that label — good enough for a group-header icon.
	type labelAcc struct {
		count     int64
		totalMin  float64
		totalKcal float64
		hrSum     float64
		hrCount   float64
		lastKind  string
	}
	byLabel := map[string]*labelAcc{}

	for rows.Next() {
		var e model.WorkoutEvent
		if err := rows.Scan(&e.ID, &e.Kind, &e.Label, &e.StartUnix, &e.DurationS,
			&e.Kcal, &e.AvgHR, &e.DistanceM, &e.SourceUUID); err != nil {
			return payload, err
		}
		payload.Events = append(payload.Events, e)

		acc := byLabel[e.Label]
		if acc == nil {
			acc = &labelAcc{lastKind: e.Kind}
			byLabel[e.Label] = acc
		}
		acc.count++
		acc.totalMin += float64(e.DurationS) / 60.0
		if e.Kcal != nil {
			acc.totalKcal += *e.Kcal
		}
		if e.AvgHR != nil && *e.AvgHR > 0 {
			acc.hrSum += float64(*e.AvgHR)
			acc.hrCount++
		}
		if e.Kind != "" {
			acc.lastKind = e.Kind
		}
	}

	// Emit ByLabel sorted by total minutes desc — most-used labels first.
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sortStringsAsc(labels)
	summaries := make([]model.WorkoutLabelSummary, 0, len(labels))
	for _, l := range labels {
		acc := byLabel[l]
		var avgHR float64
		if acc.hrCount > 0 {
			avgHR = acc.hrSum / acc.hrCount
		}
		summaries = append(summaries, model.WorkoutLabelSummary{
			Label:     l,
			Kind:      acc.lastKind,
			Count:     acc.count,
			TotalMin:  acc.totalMin,
			TotalKcal: acc.totalKcal,
			AvgHR:     avgHR,
		})
	}
	// Reorder by totalMin desc — simple selection sort, list is small.
	for i := 0; i < len(summaries); i++ {
		best := i
		for j := i + 1; j < len(summaries); j++ {
			if summaries[j].TotalMin > summaries[best].TotalMin {
				best = j
			}
		}
		if best != i {
			summaries[i], summaries[best] = summaries[best], summaries[i]
		}
	}
	payload.ByLabel = summaries
	payload.HasData = len(payload.Events) > 0
	return payload, nil
}

// ---- helpers ----

func jsonbOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func sortStringsAsc(s []string) {
	// Small helper to avoid an import for a one-line sort in a package that
	// already avoids "sort" elsewhere. Simple insertion sort — days-per-
	// request is bounded to a few hundred at most.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func summariseDays(days []model.HealthActivityDay) model.HealthActivityDay {
	var total model.HealthActivityDay
	total.Day = "range"
	if len(days) == 0 {
		return total
	}
	var (
		hrSum, hrCount             float64
		restSum, restCount         float64
		hrvSum, hrvCount           float64
	)
	for _, d := range days {
		total.Workouts += d.Workouts
		total.WorkoutMinutes += d.WorkoutMinutes
		total.ActiveKcal += d.ActiveKcal
		total.Steps += d.Steps
		total.SleepMinutes += d.SleepMinutes
		total.MindfulMinutes += d.MindfulMinutes
		if d.AvgHR > 0 {
			hrSum += d.AvgHR
			hrCount++
		}
		if d.RestingHR > 0 {
			restSum += d.RestingHR
			restCount++
		}
		if d.HRVMs > 0 {
			hrvSum += d.HRVMs
			hrvCount++
		}
	}
	if hrCount > 0 {
		total.AvgHR = hrSum / hrCount
	}
	if restCount > 0 {
		total.RestingHR = restSum / restCount
	}
	if hrvCount > 0 {
		total.HRVMs = hrvSum / hrvCount
	}
	return total
}

