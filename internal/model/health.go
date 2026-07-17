package model

// Wire format for the HealthKit companion app (extensions/boomtime-watch/).
// Keep these shapes stable — the Swift side hard-codes matching Codable structs
// and rolling breaking changes there is painful (personal-provisioning rebuild
// + re-pair). Additive changes (new optional fields) are fine.

// HRSeriesPoint is one heart-rate sample inside a workout's per-second series.
type HRSeriesPoint struct {
	T   float64 `json:"t"`   // unix seconds
	BPM int64   `json:"bpm"` // beats per minute
}

// RoutePoint is one GPS breadcrumb inside a workout's route.
type RoutePoint struct {
	T   float64  `json:"t"`             // unix seconds
	Lat float64  `json:"lat"`
	Lon float64  `json:"lon"`
	Alt *float64 `json:"alt,omitempty"` // meters, nullable
}

// WorkoutPayload is one workout as HealthKit hands it to us (HKWorkout +
// derived samples). Populated fully by the companion; server persists it as
// a heartbeat row (with workout_* columns) plus a workout_details row for
// HR series and route.
//
// Label is a user-configurable override for the project bucket (edited on the
// phone under Settings -> Workout Labels). When non-nil/non-empty it replaces
// Kind as the heartbeat's Project; Kind itself stays raw so downstream
// aggregations that key on the activity type still line up. Older clients
// omit the field entirely — the server falls back to Kind in that case.
type WorkoutPayload struct {
	Kind       string          `json:"kind"`         // lowercase HKWorkoutActivityType name
	Label      *string         `json:"label,omitempty"` // optional user-facing project bucket name
	Start      float64         `json:"start"`        // unix seconds, float64
	End        float64         `json:"end"`
	DurationS  int64           `json:"duration_s"`   // authoritative duration
	Kcal       *float64        `json:"kcal,omitempty"`
	DistanceM  *float64        `json:"distance_m,omitempty"`
	AvgHR      *int64          `json:"avg_hr,omitempty"`
	HRSeries   []HRSeriesPoint `json:"hr_series,omitempty"`
	Route      []RoutePoint    `json:"route,omitempty"`
	SourceUUID string          `json:"source_uuid"` // HKWorkout.uuid — used to dedupe
}

// WorkoutBulkRequest is the top-level envelope for POST /workouts.bulk.
// Wrapping in {"data": [...]} matches the shape HKAnchoredObjectQuery batches
// naturally produce on the Swift side; also leaves room for future top-level
// fields (batch metadata, source device, anchor cursor) without a schema break.
type WorkoutBulkRequest struct {
	Data []WorkoutPayload `json:"data"`
}

// HealthSamplePayload is one raw HealthKit sample — HR reading, step count,
// active-energy delta, HRV SDNN measurement, sleep stage, mindful minute.
// Persisted to health_samples, aggregated in health_rollup_daily.
type HealthSamplePayload struct {
	Kind        string          `json:"kind"`                   // heart_rate|resting_heart_rate|steps|active_energy|hrv|sleep_stage|mindful
	Unit        string          `json:"unit"`                   // bpm|count|kcal|ms|minutes|stage
	Qty         *float64        `json:"qty,omitempty"`          // single-value samples
	QMin        *float64        `json:"q_min,omitempty"`        // HR ranges
	QAvg        *float64        `json:"q_avg,omitempty"`
	QMax        *float64        `json:"q_max,omitempty"`
	TsStart     float64         `json:"ts_start"`               // unix seconds, float64
	TsEnd       *float64        `json:"ts_end,omitempty"`       // interval samples (sleep stages)
	Meta        map[string]any  `json:"meta,omitempty"`         // device, sleep-stage label, etc.
	WorkoutUUID *string         `json:"workout_uuid,omitempty"` // FK by source_uuid to workout_details
}

// HealthSampleBulkRequest is the top-level envelope for POST /health_samples.bulk.
type HealthSampleBulkRequest struct {
	Data []HealthSamplePayload `json:"data"`
}

// ---- Response shapes for GET /stats/health ----

// HealthActivityDay is one row of the Wellness card feed. All counters are
// zero (not nil) when the day is present but empty, so the client can render
// a flat baseline without null-checking each field.
type HealthActivityDay struct {
	Day             string  `json:"day"`             // YYYY-MM-DD
	Workouts        int64   `json:"workouts"`        // count of workouts that day
	WorkoutMinutes  float64 `json:"workoutMinutes"`  // sum of workout_duration_s / 60
	ActiveKcal      float64 `json:"activeKcal"`      // sum of active_energy samples
	Steps           int64   `json:"steps"`           // sum of step_count samples
	AvgHR           float64 `json:"avgHR"`           // avg heart_rate across the day
	RestingHR       float64 `json:"restingHR"`       // latest resting_heart_rate sample
	SleepMinutes    float64 `json:"sleepMinutes"`    // sum of asleep_* stage durations
	HRVMs           float64 `json:"hrvMs"`           // avg SDNN
	MindfulMinutes  float64 `json:"mindfulMinutes"`  // sum of mindful sample durations
}

// HealthActivityPayload is the response shape for GET /stats/health.
// Follows AIActivityPayload's convention (bigbets.go:55-63): a top-level
// HasData toggle so the FE Wellness card can silently skip render for empty
// ranges without a null-check on every field.
type HealthActivityPayload struct {
	HasData bool                `json:"hasData"`
	Days    []HealthActivityDay `json:"days"`
	Totals  HealthActivityDay   `json:"totals"` // day="range"
}

// WorkoutEvent is one row of the per-workout event list. Powers the Wellness
// page's event breakdown (group by label to see "all Morning Runs" etc.).
// Label is the user-chosen project bucket (from the companion's
// AnnotatedWorkout list); Kind is the raw HKWorkoutActivityType name so the
// UI can still show a type icon regardless of what the user named it.
type WorkoutEvent struct {
	ID         int64    `json:"id"`         // heartbeats.id
	Kind       string   `json:"kind"`       // raw activity type
	Label      string   `json:"label"`      // == project bucket; falls back to Kind when unset
	StartUnix  float64  `json:"start"`      // unix seconds
	DurationS  int64    `json:"durationS"`
	Kcal       *float64 `json:"kcal,omitempty"`
	AvgHR      *int64   `json:"avgHR,omitempty"`
	DistanceM  *float64 `json:"distanceM,omitempty"`
	SourceUUID string   `json:"sourceUUID"`
}

// WorkoutLabelSummary is a per-label aggregate over a range: how many workouts
// carry this label, total minutes/kcal/etc. Fuels the "breakdown by label"
// section on the Wellness page.
type WorkoutLabelSummary struct {
	Label     string  `json:"label"`
	Kind      string  `json:"kind"`    // representative raw kind (mode across the group)
	Count     int64   `json:"count"`
	TotalMin  float64 `json:"totalMin"`
	TotalKcal float64 `json:"totalKcal"`
	AvgHR     float64 `json:"avgHR"`   // avg-of-avgs — good enough for breakdown-scale UI
}

// WorkoutListPayload is the response shape for GET /users/current/workouts.
type WorkoutListPayload struct {
	Events    []WorkoutEvent        `json:"events"`
	ByLabel   []WorkoutLabelSummary `json:"byLabel"`
	HasData   bool                  `json:"hasData"`
}
