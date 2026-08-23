package model

import "time"

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
//
// Wakatime-compat notes (boom-dg7): full_name / email / photo are consumed by
// unmodified Wakatime editor plugins that expect this exact shape. Adding
// fields is safe (JSON decoders ignore unknowns) and matches how is_admin was
// introduced. NEVER remove or rename an existing key here without confirming
// every wakatime-* plugin path — a rename silently 500s a whole cohort of
// editor sessions.
type UserStatus struct {
	FullName string `json:"full_name"` // rFull_name -> full_name
	Email    string `json:"email"`     // rEmail -> email
	Photo    string `json:"photo"`     // rPhoto -> photo
	// boom-myv: signal to the FE whether this user is on the admin allowlist
	// (BOOM_ADMIN_USERS). The FE conditionally shows the Admin tab based on
	// this — server also enforces via the admin endpoints, so the flag is a
	// UX aid, not a security boundary.
	IsAdmin bool `json:"is_admin"` // omit-defaults not used to keep the shape stable
	// boom-dg7: user's raw stored IANA name ('' = never picked). The FE
	// picker reads this to decide "your explicit choice" vs "auto-detect
	// from browser". Wakatime-compat: additive field, unknown to plugins.
	Timezone string `json:"timezone"`
	// boom-dg7: what the server ACTUALLY resolves to via the 3-level chain
	// (user > BOOM_DEFAULT_TIMEZONE > "UTC"). The FE's auto-detect on first
	// login only fires when this differs from the browser's zone AND
	// Timezone == '' (user hasn't opted in). NEVER "".
	EffectiveTimezone string `json:"effective_timezone"`
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
