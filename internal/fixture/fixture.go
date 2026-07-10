// Package fixture defines the on-disk shape of the anonymized heartbeat fixture
// shared by the generator (tools/fixturegen) and the test loader (internal/db).
package fixture

import "time"

// Heartbeat is one fixture row. Fields mirror the heartbeats table columns that
// matter for aggregation. Identifying fields are anonymized by the generator;
// non-identifying fields (language/editor/platform/category/is_write/type/timing)
// are kept as-is for realism. gap_seconds is intentionally omitted: the loader
// recomputes gaps from time order so the fixture stays timing-accurate.
type Heartbeat struct {
	Project      *string   `json:"project"`
	Language     *string   `json:"language"`
	Editor       *string   `json:"editor"`
	Plugin       *string   `json:"plugin"`
	Platform     *string   `json:"platform"`
	Machine      *string   `json:"machine"`
	Branch       *string   `json:"branch"`
	Category     *string   `json:"category"`
	Entity       string    `json:"entity"`
	Type         string    `json:"type"` // ty: file/app/domain/url
	IsWrite      *bool     `json:"isWrite"`
	Lineno       *int64    `json:"lineno"`
	Cursorpos    *string   `json:"cursorpos"`
	FileLines    *int64    `json:"fileLines"`
	Dependencies []string  `json:"dependencies"`
	UserAgent    string    `json:"userAgent"`
	TimeSent     time.Time `json:"timeSent"`
}

// File is the top-level fixture document.
type File struct {
	// Anonymized reports whether identifying fields were pseudonymized.
	Anonymized bool `json:"anonymized"`
	// GeneratedAt is informational (not load-bearing for tests).
	GeneratedAt time.Time `json:"generatedAt"`
	// Counts is a small summary for humans reading the committed file.
	Counts Counts `json:"counts"`
	// Heartbeats is the row set, in stable (deterministic) order.
	Heartbeats []Heartbeat `json:"heartbeats"`
}

// Counts summarizes the fixture's coverage (for review + a golden smoke test).
type Counts struct {
	Heartbeats int `json:"heartbeats"`
	Projects   int `json:"projects"`
	Languages  int `json:"languages"`
	Editors    int `json:"editors"`
	Machines   int `json:"machines"`
	Branches   int `json:"branches"`
	Categories int `json:"categories"`
	Days       int `json:"days"`
}
