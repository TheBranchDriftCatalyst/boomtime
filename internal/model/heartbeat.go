package model

// EntityType is the kind of entity a heartbeat refers to.
type EntityType string

const (
	FileType    EntityType = "file"
	AppType     EntityType = "app"
	DomainType  EntityType = "domain"
	URLType     EntityType = "url"
	WorkoutType EntityType = "workout" // Apple Watch / HealthKit workouts
)

// HeartbeatPayload is the incoming/outgoing heartbeat JSON (Types.hs HeartbeatPayload,
// encoded with convertReservedWords).
type HeartbeatPayload struct {
	Editor       *string    `json:"editor"`
	Plugin       *string    `json:"plugin"`
	Platform     *string    `json:"platform"`
	Machine      *string    `json:"machine"`
	Sender       *string    `json:"sender"`
	UserAgent    string     `json:"user_agent"`
	Branch       *string    `json:"branch"`
	Category     *string    `json:"category"`
	Cursorpos    *int64     `json:"cursorpos"`
	Dependencies []string   `json:"dependencies"`
	Entity       string     `json:"entity"`
	IsWrite      *bool      `json:"is_write"`
	Language     *string    `json:"language"`
	Lineno       *int64     `json:"lineno"`
	FileLines    *int64     `json:"lines"` // file_lines -> lines
	Project      *string    `json:"project"`
	Type         EntityType `json:"type"` // ty -> type
	TimeSent     float64    `json:"time"` // time_sent -> time
	// gaka-1l9: AI-assistance fields wakatime.com started emitting 2026-07-03.
	// Kept optional (`,omitempty` so heartbeats from non-AI plugins don't
	// re-encode a bunch of null keys and blow up on-wire size).
	AIInputTokens      *int64  `json:"ai_input_tokens,omitempty"`
	AIOutputTokens     *int64  `json:"ai_output_tokens,omitempty"`
	AILineChanges      *int64  `json:"ai_line_changes,omitempty"`
	HumanLineChanges   *int64  `json:"human_line_changes,omitempty"`
	AIPromptLength     *int64  `json:"ai_prompt_length,omitempty"`
	AISession          *string `json:"ai_session,omitempty"`
	AISubscriptionPlan *string `json:"ai_subscription_plan,omitempty"`
	// Health metrics: populated when Type == "workout" (see internal/model/health.go).
	// Set by the workouts ingest path (handler/workouts.go), not by editor plugins.
	// Existing WakaTime heartbeats leave all five nil, so the rollup query's
	// COALESCE(workout_duration_s, gap_seconds_bounded) preserves the old
	// gap-inferred semantics for coding time.
	WorkoutKind       *string  `json:"workout_kind,omitempty"`
	WorkoutDurationS  *int64   `json:"workout_duration_s,omitempty"`
	WorkoutKcal       *float64 `json:"workout_kcal,omitempty"`
	WorkoutAvgHR      *int64   `json:"workout_avg_hr,omitempty"`
	WorkoutDistanceM  *float64 `json:"workout_distance_m,omitempty"`
}

// HeartbeatID is the inner {"id": "..."} object.
type HeartbeatID struct {
	ID string `json:"id"` // heartbeatId -> id
}

// HeartbeatData wraps a HeartbeatID as {"data": {"id": "..."}}.
type HeartbeatData struct {
	Data HeartbeatID `json:"data"` // heartbeatData -> data
}

// BulkHeartbeatData is the top-level bulk response: {"responses": [[{data},code],...]}.
// Each inner element mixes a HeartbeatData object and an int status code (untagged
// sum ReturnBulkStruct), so we serialize as []any.
type BulkHeartbeatData struct {
	Responses [][]any `json:"responses"` // bResponses -> responses
}
