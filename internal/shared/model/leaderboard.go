package model

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
