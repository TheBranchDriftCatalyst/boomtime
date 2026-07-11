package model

import "time"

// ---- Project payloads (Projects.hs) ----

// ProjectStatistics is GET /api/v1/users/current/projects/:project (default options).
type ProjectStatistics struct {
	StartDate    time.Time       `json:"startDate"`
	EndDate      time.Time       `json:"endDate"`
	TotalSeconds int64           `json:"totalSeconds"`
	DailyTotal   []int64         `json:"dailyTotal"`
	Languages    []ResourceStats `json:"languages"`
	// LanguagesDaily is the per-day-per-language matrix for the SAME top-N (+
	// "Other (N more)") set as Languages, each series aligned index-for-index to
	// DailyTotal's day axis. Invariant: summing daily across all series for a
	// given day equals DailyTotal[day]. Powers the language-stacked "Total
	// activity" column chart. Additive/backward-compatible.
	LanguagesDaily []LanguageDaily `json:"languagesDaily"`
	Files          []ResourceStats `json:"files"`
	WeekDay        []ResourceStats `json:"weekDay"`
	Hour           []ResourceStats `json:"hour"`
	// True distinct counts (languages/files lists are capped to top-N + "Other").
	LanguagesCount int `json:"languagesCount"`
	FilesCount     int `json:"filesCount"`

	// Authoring vs Reading (ty='file' only; is_write has no meaning otherwise).
	WriteSeconds int64 `json:"writeSeconds"`
	ReadSeconds  int64 `json:"readSeconds"`
	// DailyWriteRatio aligns to DailyTotal's days: write/(write+read) per day,
	// 0 when there was no file activity that day.
	DailyWriteRatio []float64 `json:"dailyWriteRatio"`

	// Branch activity (top-12 + "Other (N more)" via capWithOther).
	Branches      []ResourceStats `json:"branches"`
	BranchesCount int             `json:"branchesCount"`

	// Breadth vs depth: distinct files (entities) touched per day (ty='file'),
	// aligned to DailyTotal's days. entitiesCount == filesCount (same distinct set).
	DailyEntities []int64 `json:"dailyEntities"`
	EntitiesCount int     `json:"entitiesCount"`
}

// ProjectListPayload is {"projects": [...]}.
type ProjectListPayload struct {
	Projects []string `json:"projects"`
}
