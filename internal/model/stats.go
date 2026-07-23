package model

import (
	"strings"
	"time"
)

// HiddenSets is a minimal, package-neutral view of the per-axis hide rules for
// a user. It exists in the model package so payload types can implement
// axis-aware scrubbing (ScrubTail) without importing internal/db (which would
// create an import cycle, since db imports model).
//
// The interface intentionally exposes ONLY read accessors. Concrete
// implementations live elsewhere: db.HiddenSets (production, DB-backed) and
// HiddenSetsMap (below, in-memory; used by tests and the widget scrubber).
//
// axis names track internal/db/axes.go: "project", "language", "editor",
// "plugin", "machine", "platform", "branch", "category". Values MUST already be
// lowercased by the loader — comparisons in this package are case-insensitive
// via lowercasing on the RHS.
type HiddenSets interface {
	// Values returns the hidden values for one axis (empty/nil if none).
	Values(axis string) []string
	// Projects is a convenience for the project axis.
	Projects() []string
}

// HiddenSetsMap is an in-memory HiddenSets implementation. Callers construct it
// directly from an axis->values map. Values are stored as-provided; callers
// should pass lowercase strings to match the DB-backed loader's contract.
type HiddenSetsMap map[string][]string

// Values returns the hidden values for one axis.
func (h HiddenSetsMap) Values(axis string) []string { return h[axis] }

// Projects returns the hidden values on the project axis.
func (h HiddenSetsMap) Projects() []string { return h["project"] }

// hiddenNameSet builds a lowercase lookup set of the hidden values on axis.
// Returns nil when no values are hidden — callers can range over a nil map or
// check for a nil return.
func hiddenNameSet(hidden HiddenSets, axis string) map[string]struct{} {
	if hidden == nil {
		return nil
	}
	vs := hidden.Values(axis)
	if len(vs) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(vs))
	for _, v := range vs {
		m[strings.ToLower(v)] = struct{}{}
	}
	return m
}

// ScrubTail returns a shallow copy of the payload with every OtherMembers tail
// filtered so no entry whose Name matches a hidden value on the corresponding
// axis appears. The top-N rows on each segment are NOT touched here — they
// should already have been excluded at query time by the DB predicates; this
// method is the LAST-LINE guard for the long-tail bucket, which capWithOther
// collapses in application code AFTER the DB has returned rows and is
// therefore not covered by SQL-level hide predicates.
//
// Axes and their corresponding StatsPayload segments:
//
//	project  -> Projects
//	language -> Languages
//	editor   -> Editors
//	platform -> Platforms
//	machine  -> Machines
//	category -> Categories
//
// The returned payload is a shallow-copied *StatsPayload — segment slices and
// their ResourceStats elements are copied only where a filter actually
// changes something, so payloads with no matches share memory with the input.
// The input payload is never mutated.
//
// If hidden is nil or has no relevant values, the input pointer is returned
// unchanged.
func (p *StatsPayload) ScrubTail(hidden HiddenSets) *StatsPayload {
	if p == nil || hidden == nil {
		return p
	}
	axisSegments := []struct {
		axis string
		seg  *[]ResourceStats
	}{
		{"project", nil}, {"language", nil}, {"editor", nil},
		{"platform", nil}, {"machine", nil}, {"category", nil},
	}
	// Build the destination lazily so we don't allocate when nothing changes.
	out := p
	copyOnWrite := func() {
		if out == p {
			cp := *p
			out = &cp
		}
	}
	// Bind the segment pointers on the copy-on-write target — but only after
	// out points at the destination. To keep the code straight, do it inline.
	for i := range axisSegments {
		names := hiddenNameSet(hidden, axisSegments[i].axis)
		if names == nil {
			continue
		}
		// Resolve the segment slice on the CURRENT out (input or copy).
		var srcSeg []ResourceStats
		switch axisSegments[i].axis {
		case "project":
			srcSeg = out.Projects
		case "language":
			srcSeg = out.Languages
		case "editor":
			srcSeg = out.Editors
		case "platform":
			srcSeg = out.Platforms
		case "machine":
			srcSeg = out.Machines
		case "category":
			srcSeg = out.Categories
		}
		newSeg, changed := scrubSegmentTail(srcSeg, names)
		if !changed {
			continue
		}
		copyOnWrite()
		switch axisSegments[i].axis {
		case "project":
			out.Projects = newSeg
		case "language":
			out.Languages = newSeg
		case "editor":
			out.Editors = newSeg
		case "platform":
			out.Platforms = newSeg
		case "machine":
			out.Machines = newSeg
		case "category":
			out.Categories = newSeg
		}
	}
	return out
}

// scrubSegmentTail walks a segment and, for every ResourceStats whose
// OtherMembers contains a hidden name, returns a copy of the segment with the
// offending entries removed. Non-Other rows and non-matching Other rows share
// memory with the input.
func scrubSegmentTail(seg []ResourceStats, hidden map[string]struct{}) ([]ResourceStats, bool) {
	if len(seg) == 0 || len(hidden) == 0 {
		return seg, false
	}
	changed := false
	out := seg
	for i, r := range seg {
		if len(r.OtherMembers) == 0 {
			continue
		}
		// Scan to see if any member matches; if none, leave the row alone.
		matched := false
		for _, m := range r.OtherMembers {
			if _, hit := hidden[strings.ToLower(m.Name)]; hit {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if !changed {
			out = make([]ResourceStats, len(seg))
			copy(out, seg)
			changed = true
		}
		filtered := make([]OtherMember, 0, len(r.OtherMembers))
		for _, m := range r.OtherMembers {
			if _, hit := hidden[strings.ToLower(m.Name)]; hit {
				continue
			}
			filtered = append(filtered, m)
		}
		rowCopy := r
		rowCopy.OtherMembers = filtered
		out[i] = rowCopy
	}
	return out, changed
}

// ---- Stats payloads (Stats.hs) ----

// ResourceStats is the per-resource aggregate (ResourceStats, noPrefixOptions).
type ResourceStats struct {
	Name         string    `json:"name"`         // pName
	TotalSeconds int64     `json:"totalSeconds"` // pTotalSeconds
	TotalPct     float64   `json:"totalPct"`     // pTotalPct
	TotalDaily   []int64   `json:"totalDaily"`   // pTotalDaily
	PctDaily     []float64 `json:"pctDaily"`     // pPctDaily

	// OtherMembers is populated only on the synthesized "Other (N more)" entry
	// produced by capWithOther. It carries the top otherMembersCap tail members
	// (by TotalSeconds desc) so tooltips can break down what "Other" contains.
	// omitempty keeps every non-Other payload byte-identical.
	OtherMembers []OtherMember `json:"otherMembers,omitempty"`
	// OtherCount is the total number of tail members Other collapsed (i.e.
	// len(tail), which is >= len(OtherMembers) when the cap kicks in). Also
	// omitempty so non-Other rows stay unchanged.
	OtherCount int `json:"otherCount,omitempty"`
}

// OtherMember is one tail entry carried on a synthesized "Other (N more)"
// ResourceStats. Name / TotalSeconds / TotalPct only — no per-day arrays, since
// carrying a per-day matrix for the tail would defeat capWithOther's purpose
// (bound the payload size).
type OtherMember struct {
	Name         string  `json:"name"`
	TotalSeconds int64   `json:"totalSeconds"`
	TotalPct     float64 `json:"totalPct"`
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
