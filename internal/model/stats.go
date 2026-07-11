package model

import "time"

// ---- Stats payloads (Stats.hs) ----

// ResourceStats is the per-resource aggregate (ResourceStats, noPrefixOptions).
type ResourceStats struct {
	Name         string    `json:"name"`         // pName
	TotalSeconds int64     `json:"totalSeconds"` // pTotalSeconds
	TotalPct     float64   `json:"totalPct"`     // pTotalPct
	TotalDaily   []int64   `json:"totalDaily"`   // pTotalDaily
	PctDaily     []float64 `json:"pctDaily"`     // pPctDaily
}

// LanguageDaily is one language's per-day coding-time series (seconds), aligned
// index-for-index to a ProjectStatistics.DailyTotal day axis. Name mirrors the
// matching Languages entry (incl. the "Other (N more)" bucket).
type LanguageDaily struct {
	Name  string  `json:"name"`
	Daily []int64 `json:"daily"`
}

// StatsPayload is GET /api/v1/users/current/stats (StatsPayload, default options).
type StatsPayload struct {
	StartDate    time.Time       `json:"startDate"`
	EndDate      time.Time       `json:"endDate"`
	TotalSeconds int64           `json:"totalSeconds"`
	DailyAvg     float64         `json:"dailyAvg"`
	DailyTotal   []int64         `json:"dailyTotal"`
	Projects     []ResourceStats `json:"projects"`
	Languages    []ResourceStats `json:"languages"`
	Platforms    []ResourceStats `json:"platforms"`
	Machines     []ResourceStats `json:"machines"`
	Editors      []ResourceStats `json:"editors"`
	Categories   []ResourceStats `json:"categories"`
	// True distinct counts before top-N capping (the *lists above are capped to
	// the top resources + one aggregated "Other" bucket to stay small).
	ProjectsCount   int `json:"projectsCount"`
	LanguagesCount  int `json:"languagesCount"`
	PlatformsCount  int `json:"platformsCount"`
	MachinesCount   int `json:"machinesCount"`
	EditorsCount    int `json:"editorsCount"`
	CategoriesCount int `json:"categoriesCount"`
}

// ---- Big-bet aggregation payloads (council visualizations) ----

// PunchcardPayload is GET /api/v1/users/current/stats/punchcard.
// Times are UTC: dow 0=Sunday..6=Saturday, hour 0..23 (FE documents the tz caveat).
type PunchcardPayload struct {
	Cells        []PunchcardCell `json:"cells"`
	MaxSeconds   int64           `json:"maxSeconds"`
	TotalSeconds int64           `json:"totalSeconds"`
}

// PunchcardCell is one dow x hour intensity bucket.
type PunchcardCell struct {
	Dow     int   `json:"dow"`
	Hour    int   `json:"hour"`
	Seconds int64 `json:"seconds"`
}

// SessionsPayload is GET /api/v1/users/current/stats/sessions (aggregates only).
type SessionsPayload struct {
	Summary   SessionSummary   `json:"summary"`
	Daily     []SessionDaily   `json:"daily"`
	Histogram []SessionHistBin `json:"histogram"`
}

// SessionSummary holds count + duration stats across all sessions in range.
type SessionSummary struct {
	Count         int64 `json:"count"`
	TotalSeconds  int64 `json:"totalSeconds"`
	AvgSeconds    int64 `json:"avgSeconds"`
	MaxSeconds    int64 `json:"maxSeconds"`
	MedianSeconds int64 `json:"medianSeconds"`
}

// SessionDaily is per-day session activity, gap-filled across the range.
type SessionDaily struct {
	Date           string `json:"date"` // "YYYY-MM-DD"
	Sessions       int64  `json:"sessions"`
	TotalSeconds   int64  `json:"totalSeconds"`
	LongestSeconds int64  `json:"longestSeconds"`
}

// SessionHistBin is one duration bucket of the session histogram.
type SessionHistBin struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// MomentumPayload is GET /api/v1/users/current/stats/momentum.
type MomentumPayload struct {
	Weeks    []string          `json:"weeks"` // ISO Monday week-starts, ascending
	Projects []MomentumProject `json:"projects"`
}

// MomentumProject is one project's weekly series (aligned to Weeks).
type MomentumProject struct {
	Name         string  `json:"name"`
	Weekly       []int64 `json:"weekly"`
	TotalSeconds int64   `json:"totalSeconds"`
}

// ActiveFile is one cross-project file: attributed time and the number of
// DISTINCT projects that touch it. Files with projects>1 are shared lynchpins.
type ActiveFile struct {
	Entity   string `json:"entity"`
	Seconds  int64  `json:"seconds"`
	Projects int64  `json:"projects"`
}

// ActiveFilesPayload is GET /api/v1/users/current/files — top files across all
// of the owner's projects, ordered lynchpins-first (projects desc, seconds desc).
type ActiveFilesPayload struct {
	Files     []ActiveFile `json:"files"`
	Truncated bool         `json:"truncated"`
}

// DayTextValue is {"text": "..."}.
type DayTextValue struct {
	Text string `json:"text"` // tText
}

// DayGrandTotal is the statusbar grand_total block.
type DayGrandTotal struct {
	GrandTotal DayTextValue `json:"grand_total"` // tGrand_total -> grand_total
	Categories []string     `json:"categories"`  // tCategories -> categories
}

// StatusBarPayload is GET /api/v1/users/current/statusbar/today.
type StatusBarPayload struct {
	Data DayGrandTotal `json:"data"` // tData -> data
}

// TimelineItem is one span in the timeline. Its ToJSON is the DEFAULT (Stats.hs),
// so keys are the raw Haskell field names. Verified against hakatime's dashboard
// which reads v.tName / v.tRangeStart / v.tRangeEnd (overview.js).
type TimelineItem struct {
	Name       string    `json:"tName"`
	RangeStart time.Time `json:"tRangeStart"`
	RangeEnd   time.Time `json:"tRangeEnd"`
}

// TimelinePayload maps language -> spans (default options: key stays timelineLangs).
type TimelinePayload struct {
	TimelineLangs map[string][]TimelineItem `json:"timelineLangs"`
}
