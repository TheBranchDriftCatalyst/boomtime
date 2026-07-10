package db

import "time"

// StatRow mirrors Types.hs StatRow (columns from get_user_activity).
type StatRow struct {
	Day          time.Time
	Project      string
	Language     string
	Editor       string
	Branch       string
	Platform     string
	Machine      string
	Entity       string
	TotalSeconds int64
	Pct          float64
	DailyPct     float64
}

// ProjectStatRow mirrors Types.hs ProjectStatRow (get_projects_stats).
type ProjectStatRow struct {
	Day          time.Time
	Weekday      string
	Hour         string
	Language     string
	Entity       string
	Ty           string // entity type (file/app/domain/url); the "files" list filters to 'file'
	TotalSeconds int64
	Pct          float64
	DailyPct     float64
}

// TimelineRow mirrors Types.hs TimelineRow (get_timeline).
type TimelineRow struct {
	Lang       string
	Project    string
	RangeStart time.Time
	RangeEnd   time.Time
}

// LeaderboardRow mirrors Types.hs LeaderboardRow (get_leaderboards).
type LeaderboardRow struct {
	Project      string
	Language     string
	Sender       string
	TotalSeconds int64
}

// StoredUser is a validated username with password material (users table).
type StoredUser struct {
	Username       string
	HashedPassword []byte
	SaltUsed       []byte
}

// TokenData is the access/refresh token pair created on login (Types.hs TokenData).
type TokenData struct {
	Owner        string
	Token        string
	RefreshToken string
}
