package model

// loc.go — payloads for the Lines-of-Code feature (gaka-yfg). LOC is derived
// entirely from heartbeats.file_lines (the file's total line count at edit
// time) — NO GitHub dependency. Per-project "current" LOC sums each file's
// latest-known line count; the over-time series snapshots the whole-repo total
// as it grew.

// LocProject is one project's current lines-of-code (sum over its files of each
// file's most-recent file_lines within the range).
type LocProject struct {
	Project string `json:"project"`
	Loc     int64  `json:"loc"`
}

// LocPoint is one point on the total-LOC-over-time series: the whole-corpus
// line count as of that date (a cumulative snapshot, not a per-day delta).
type LocPoint struct {
	Date string `json:"date"` // "YYYY-MM-DD"
	Loc  int64  `json:"loc"`
}

// LocPayload is GET /api/v1/users/current/stats/loc. TotalLoc is the sum of
// PerProject (the current snapshot); OverTime is the bounded, downsampled
// total-LOC growth curve across the range. Both are derived with the
// generated/vendored ignore filter applied (see internal/db/loc.go), so the
// numbers reflect hand-written code, not node_modules / build output.
type LocPayload struct {
	TotalLoc   int64        `json:"totalLoc"`
	PerProject []LocProject `json:"perProject"`
	OverTime   []LocPoint   `json:"overTime"`
}
