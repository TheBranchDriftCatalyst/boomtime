package db

import "time"

// StatRow mirrors Types.hs StatRow (columns from get_user_activity).
//
// The *Missing flags are TRUE when every heartbeat contributing to this
// row had NULL on that axis (browser sessions with no file open, AI
// console tabs, plugin-less clients). Callers building per-axis pies
// (languages, projects, editors, ...) filter WHERE NOT <axis>Missing so
// null-axis time doesn't get collapsed into a synthetic 'Other' bucket
// that reads like the capWithOther aggregation cap. See boom-6ci.
type StatRow struct {
	Day             time.Time
	Project         string
	Language        string
	Editor          string
	Branch          string
	Platform        string
	Machine         string
	Entity          string
	TotalSeconds    int64
	Pct             float64
	DailyPct        float64
	ProjectMissing  bool
	LanguageMissing bool
	EditorMissing   bool
	BranchMissing   bool
	PlatformMissing bool
	MachineMissing  bool
}

// ProjectStatRow mirrors Types.hs ProjectStatRow (get_projects_stats).
type ProjectStatRow struct {
	Day             time.Time
	Weekday         string
	Hour            string
	Language        string
	Entity          string
	Ty              string // entity type (file/app/domain/url); the "files" list filters to 'file'
	TotalSeconds    int64
	Pct             float64
	DailyPct        float64
	LanguageMissing bool // boom-6ci: TRUE when the source heartbeat had NULL language
	EntityMissing   bool
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
	Project         string
	Language        string
	Sender          string
	TotalSeconds    int64
	ProjectMissing  bool // boom-6ci
	LanguageMissing bool
}

// StoredUser is a validated username with password material (users table).
//
// boom-awh.6: ArgonVersion tags the row with the Argon2id parameter generation
// its hashed_password was produced under. 1 = legacy (pre-Bravo params),
// 2 = current (OWASP ASVS L1 2025 floor). Verify with
// auth.VerifyPasswordWithVersion so a v1 hash is checked against v1 params.
// New rows land at 2; a successful login against a v1 row triggers a
// transparent rehash to 2 (see UpgradeArgonVersion).
type StoredUser struct {
	Username       string
	HashedPassword []byte
	SaltUsed       []byte
	ArgonVersion   int
	// Timezone (boom-dg7) is the user's explicit IANA name choice or the
	// empty-string sentinel meaning "no explicit pick — use BOOM_DEFAULT_TIMEZONE
	// (else UTC)". Callers that need the effective zone for a query MUST use
	// the 3-level resolver (handler.resolveUserTZ / db.ResolveTimezone) rather
	// than reading this field raw, so the env default has a chance to fire.
	Timezone string
}

// StoredUserFull is StoredUser plus the user-model substrate columns added in
// migration 00046 (boom-0oe.1): role, the capabilities override blob (raw
// JSONB bytes — parsed by auth.BuildIdentity), and disabled_at. Read via
// GetUserFullByName ONLY on the Identity path when BOOM_FEATURE_USER_MODEL is
// on; the today StoredUser + GetUserByName are left untouched so unmigrated
// callers compile and behave identically.
type StoredUserFull struct {
	StoredUser
	Role         string
	Capabilities []byte     // raw users.capabilities JSONB ('{}' when unset)
	DisabledAt   *time.Time // nil = active; non-nil = disabled (fail closed)
}

// TokenData is the access/refresh token pair created on login (Types.hs TokenData).
type TokenData struct {
	Owner        string
	Token        string
	RefreshToken string
}
