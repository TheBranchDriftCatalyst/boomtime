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
