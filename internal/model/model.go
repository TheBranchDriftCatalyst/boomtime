// Package model holds the domain structs and their JSON wire representations.
// JSON tags reproduce hakatime's aeson field-name rules exactly:
//   - noPrefixOptions: drop the leading lowercase prefix and lowercase the next
//     char (pName -> name, tGrand_total -> grand_total, leadProject -> project).
//   - convertReservedWords (heartbeat only): ty->type, time_sent->time,
//     file_lines->lines; all other fields keep their name.
package model

import "time"

// EntityType is the kind of entity a heartbeat refers to.
type EntityType string

const (
	FileType   EntityType = "file"
	AppType    EntityType = "app"
	DomainType EntityType = "domain"
	URLType    EntityType = "url"
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

// StoredApiToken is one row of GET /auth/tokens (Types.hs StoredApiToken).
// Its ToJSON instance is the DEFAULT (no noPrefixOptions), so the keys are the
// raw Haskell field names. Verified against hakatime's dashboard which reads
// t.tknId / t.tknName / t.lastUsage (TokenList.js).
type StoredApiToken struct {
	ID        string     `json:"tknId"`     // base64(uuid) token id
	LastUsage *time.Time `json:"lastUsage"` // last_usage timestamp
	Name      *string    `json:"tknName"`   // optional name
	Desc      *string    `json:"tknDesc"`   // optional description
}

// TokenMetadata is the body of POST /auth/token (rename).
type TokenMetadata struct {
	TokenName string `json:"tokenName"`
	TokenID   string `json:"tokenId"`
}

// ---- Stats payloads (Stats.hs) ----

// ResourceStats is the per-resource aggregate (ResourceStats, noPrefixOptions).
type ResourceStats struct {
	Name         string    `json:"name"`         // pName
	TotalSeconds int64     `json:"totalSeconds"` // pTotalSeconds
	TotalPct     float64   `json:"totalPct"`     // pTotalPct
	TotalDaily   []int64   `json:"totalDaily"`   // pTotalDaily
	PctDaily     []float64 `json:"pctDaily"`     // pPctDaily
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

// ---- Project payloads (Projects.hs) ----

// ProjectStatistics is GET /api/v1/users/current/projects/:project (default options).
type ProjectStatistics struct {
	StartDate    time.Time       `json:"startDate"`
	EndDate      time.Time       `json:"endDate"`
	TotalSeconds int64           `json:"totalSeconds"`
	DailyTotal   []int64         `json:"dailyTotal"`
	Languages    []ResourceStats `json:"languages"`
	Files        []ResourceStats `json:"files"`
	WeekDay      []ResourceStats `json:"weekDay"`
	Hour         []ResourceStats `json:"hour"`
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

// TagsPayload is {"tags": [...]}.
type TagsPayload struct {
	Tags []string `json:"tags"`
}

// ProjectListPayload is {"projects": [...]}.
type ProjectListPayload struct {
	Projects []string `json:"projects"`
}

// ---- Leaderboards (Leaderboards.hs) ----

// UserTime is {"name": ..., "value": ...} (UserTime, noPrefixOptions).
type UserTime struct {
	Name  string `json:"name"`  // utName
	Value int64  `json:"value"` // utValue
}

// LeaderboardsPayload is GET /api/v1/leaderboards (noPrefixOptions).
type LeaderboardsPayload struct {
	Global []UserTime            `json:"global"` // lGlobal
	Lang   map[string][]UserTime `json:"lang"`   // lLang
}

// ---- Auth (Authentication.hs) ----

// AuthRequest is the login/register body.
type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is returned by login/register/refresh (default options).
type LoginResponse struct {
	Token         string    `json:"token"`
	TokenExpiry   time.Time `json:"tokenExpiry"`
	TokenUsername string    `json:"tokenUsername"`
}

// TokenResponse is {"apiToken": "..."}.
type TokenResponse struct {
	APIToken string `json:"apiToken"`
}

// ---- Users (Users.hs) ----

// UserStatus is the inner user object (noPrefixOptions on rFull_name etc.).
type UserStatus struct {
	FullName string `json:"full_name"` // rFull_name -> full_name
	Email    string `json:"email"`     // rEmail -> email
	Photo    string `json:"photo"`     // rPhoto -> photo
}

// UserStatusResponse is GET /auth/users/current.
type UserStatusResponse struct {
	Data UserStatus `json:"data"` // rData -> data
}

// ---- Badges (Badges.hs) ----

// BadgeResponse is {"badgeUrl": "..."}.
type BadgeResponse struct {
	BadgeURL string `json:"badgeUrl"`
}

// ---- Import (Import.hs) ----

// ImportRequestPayload is the body of POST /import and /import/status.
type ImportRequestPayload struct {
	APIToken  string    `json:"apiToken"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
}

// ImportRequestResponse is {"jobStatus": "Submitted|Pending|Failed|Finished"}.
type ImportRequestResponse struct {
	JobStatus string `json:"jobStatus"`
}

// ---- Commits (Commits.hs) ----

// CommitReport is {"commits": [...]}.
type CommitReport struct {
	Commits []CommitPayload `json:"commits"`
}

// AuthorData / CommitterData / CommitData mirror the GitHub commit shape with
// noPrefixOptions applied to the wrapper structs.
type AuthorData struct {
	Name  string    `json:"name"`  // authorName
	Email string    `json:"email"` // authorEmail
	Date  time.Time `json:"date"`  // authorDate
}

type CommitterData struct {
	Name  string    `json:"name"`  // committerName
	Email string    `json:"email"` // committerEmail
	Date  time.Time `json:"date"`  // committerDate
}

type CommitData struct {
	URL       string        `json:"url"`       // dataUrl
	Author    AuthorData    `json:"author"`    // dataAuthor
	Committer CommitterData `json:"committer"` // dataCommitter
	Message   string        `json:"message"`   // dataMessage
}

type AuthorPayload struct {
	Login string `json:"login"` // authorLogin
}

type CommitParent struct {
	URL string `json:"url"` // cmUrl
	Sha string `json:"sha"` // cmSha
}

type CommitPayload struct {
	URL          string         `json:"url"`           // pUrl
	Sha          string         `json:"sha"`           // pSha
	HTMLURL      string         `json:"html_url"`      // pHtml_url -> html_url
	Commit       CommitData     `json:"commit"`        // pCommit
	Author       AuthorPayload  `json:"author"`        // pAuthor
	Parents      []CommitParent `json:"parents"`       // pParents
	TotalSeconds *int64         `json:"total_seconds"` // pTotal_seconds -> total_seconds
}

// ---- Errors (Errors.hs ApiErrorData, omitNothingFields) ----

// APIErrorData is the error JSON envelope: {"error": "...", "message": "..."}.
type APIErrorData struct {
	Error   string  `json:"error"`             // apiError
	Message *string `json:"message,omitempty"` // apiMessage, omitted when nil
}
