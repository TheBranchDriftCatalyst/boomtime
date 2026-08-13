package amazon

// insights.go — the Kindle Reading-Insights wire surface, a sibling of the Cloud
// Reader library in readamazon.go. It is the ONLY per-book finish-DATE source
// Kindle exposes: the Cloud Reader library (readamazon.go) carries no timestamps,
// so every kindle reading_items.finished_at is otherwise null/un-windowable. The
// insights feed returns the user's read HISTORY (asin + date_read) back to ~2020,
// plus reading streaks / goals / achievements.
//
// Auth is IDENTICAL to the Cloud Reader library: website cookies, NOT ADP device
// signing. Reuse ExchangeWebsiteCookies (refresh_token → .amazon.com cookies),
// then GET the insights endpoint with those cookies as a plain Cookie header. No
// CSRF token is required for the GET (verified live 2026-08-13).
//
// Endpoint (reverse-engineered live 2026-08-13):
//
//	host www.amazon.com (CookieExchangeHost)
//	  GET /kindle/reading/insights/data
//	  -> ~40KB JSON:
//	     goal_info.titles_read[]{asin,date_read,content_type,read_event_id,
//	                             source_origin}  <- the core payload
//	     goal_info.goals, goal_info.is_backfill_completed
//	     current_daily_streak{duration}, longest_daily_streak{...},
//	     current_weekly_streak{...,ttl}, longest_weekly_streak{...},
//	     current_weekly_streak_state
//	     achievements_data{...}, preferences, urcGatingWeblabTreatment
//
// The whole response is a moving target, so decoding is deliberately tolerant:
// unknown fields are ignored, missing fields decode to zero values, and the
// secondary streak/achievement sub-objects are parsed best-effort (isolated so a
// type surprise there can never break titles_read, the payload the ingest needs).
// parseKindleInsights is pure (body -> typed) so it unit-tests against a captured
// fixture with no network.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// KindleInsightsPath is the reading-insights GET path on CookieExchangeHost
// (www.amazon.com). It is website-cookie authenticated, like the Cloud Reader.
const KindleInsightsPath = "/kindle/reading/insights/data"

// KindleInsights is the decoded reading-insights payload. TitlesRead is the core
// finish-date history the ingest consumes; the streaks / goals / achievements are
// retained (and the full Raw body is kept) for a future insights surface without
// forcing a schema up front.
type KindleInsights struct {
	// TitlesRead is the per-book read history: one entry per (asin, date_read).
	// This is the finish-DATE source that backfills reading_items.finished_at.
	TitlesRead []TitleRead

	// Goals is goal_info.goals, retained opaquely (shape not modelled yet).
	Goals               json.RawMessage
	IsBackfillCompleted bool

	// Reading streaks (best-effort parse; full detail also lives in Raw).
	CurrentDailyStreak       Streak
	LongestDailyStreak       Streak
	CurrentWeeklyStreak      Streak
	LongestWeeklyStreak      Streak
	CurrentWeeklyStreakState string

	// Achievements summary (best-effort parse).
	Achievements Achievements

	// URCGatingWeblabTreatment is an opaque A/B gating string Amazon returns.
	URCGatingWeblabTreatment string

	// Raw is the full response body, stored verbatim as the insights snapshot so
	// streaks/goals/achievements are recoverable later without a re-fetch.
	Raw []byte
}

// TitleRead is one read-history entry from goal_info.titles_read. DateRead is the
// parsed per-book read/finish DATE; DateReadRaw preserves the exact source value
// (whatever format Amazon sent) for debugging. ReadEventID is the seam the future
// per-session reading-TIME probe keys off (see the ingest's kindle-minutes TODO).
type TitleRead struct {
	ASIN         string
	DateRead     time.Time // parsed date_read (zero when unparseable/absent)
	DateReadRaw  string    // the raw date_read value as received
	ContentType  string
	ReadEventID  string
	SourceOrigin string
}

// Streak is a best-effort decode of a streak sub-object. Fields Amazon may send
// as either a string or a number use flexString so a type surprise never fails
// the decode. Absent fields stay at their zero value.
type Streak struct {
	Duration           int
	Start              string
	End                string
	UTCEndTime         string
	ReadingMarketplace string
	TTL                int64
}

// Achievements is a best-effort decode of achievements_data. DisplayAttributes is
// retained opaquely (shape not modelled yet).
type Achievements struct {
	DaysLeftInCurrentChallenge int
	TotalAvailableAchievements int
	TotalEarnedAchievements    int
	DisplayAttributes          json.RawMessage
}

// FetchKindleInsights (method) delegates to the package-level function so the
// Cloud Reader client exposes the insights fetch alongside the library methods.
func (c *CloudReaderClient) FetchKindleInsights(ctx context.Context, cookies map[string]string) (*KindleInsights, error) {
	return FetchKindleInsights(ctx, cookies)
}

// FetchKindleInsights GETs the reading-insights endpoint with the exchanged
// website cookies and decodes the response. The caller supplies cookies from
// ExchangeWebsiteCookies (the same jar the Cloud Reader library uses).
func FetchKindleInsights(ctx context.Context, cookies map[string]string) (*KindleInsights, error) {
	if len(cookies) == 0 {
		return nil, fmt.Errorf("amazon kindle insights: no website cookies (call ExchangeWebsiteCookies first)")
	}
	endpoint := "https://" + CookieExchangeHost + KindleInsightsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookieHeader(cookies))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cloudReaderUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// ~40KB live; 8 MiB is a generous ceiling for a big multi-year history.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("amazon kindle insights: HTTP %d: %s", resp.StatusCode, kindleSnippet(body))
	}
	return parseKindleInsights(body)
}

// ---------------------------------------------------------------------------
// Pure parser (unit-tested against a captured fixture)
// ---------------------------------------------------------------------------

// insightsWire is the tolerant wire shape. Everything the ingest depends on
// (titles_read) is typed; the volatile streak / achievement / goal sub-objects
// are captured as json.RawMessage and decoded best-effort afterwards, so a shape
// change there can never break the core titles parse.
type insightsWire struct {
	GoalInfo struct {
		TitlesRead []struct {
			ASIN         string          `json:"asin"`
			DateRead     json.RawMessage `json:"date_read"`
			ContentType  string          `json:"content_type"`
			ReadEventID  string          `json:"read_event_id"`
			SourceOrigin string          `json:"source_origin"`
		} `json:"titles_read"`
		Goals               json.RawMessage `json:"goals"`
		IsBackfillCompleted bool            `json:"is_backfill_completed"`
	} `json:"goal_info"`
	CurrentDailyStreak       json.RawMessage `json:"current_daily_streak"`
	LongestDailyStreak       json.RawMessage `json:"longest_daily_streak"`
	CurrentWeeklyStreak      json.RawMessage `json:"current_weekly_streak"`
	LongestWeeklyStreak      json.RawMessage `json:"longest_weekly_streak"`
	CurrentWeeklyStreakState string          `json:"current_weekly_streak_state"`
	AchievementsData         json.RawMessage `json:"achievements_data"`
	URCGating                string          `json:"urcGatingWeblabTreatment"`
}

// parseKindleInsights maps the insights response into the typed struct. Items
// without an ASIN are skipped (they can't be keyed to a reading_item); a
// title whose date_read won't parse is still surfaced (with a zero DateRead) so
// the parser is faithful — the ingest decides to skip zero dates.
func parseKindleInsights(body []byte) (*KindleInsights, error) {
	var w insightsWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("amazon kindle insights: parse failed: %w (body: %s)", err, kindleSnippet(body))
	}
	ins := &KindleInsights{
		Goals:                    w.GoalInfo.Goals,
		IsBackfillCompleted:      w.GoalInfo.IsBackfillCompleted,
		CurrentDailyStreak:       parseStreak(w.CurrentDailyStreak),
		LongestDailyStreak:       parseStreak(w.LongestDailyStreak),
		CurrentWeeklyStreak:      parseStreak(w.CurrentWeeklyStreak),
		LongestWeeklyStreak:      parseStreak(w.LongestWeeklyStreak),
		CurrentWeeklyStreakState: strings.TrimSpace(w.CurrentWeeklyStreakState),
		Achievements:             parseAchievements(w.AchievementsData),
		URCGatingWeblabTreatment: strings.TrimSpace(w.URCGating),
		Raw:                      append([]byte(nil), body...),
	}
	ins.TitlesRead = make([]TitleRead, 0, len(w.GoalInfo.TitlesRead))
	for _, t := range w.GoalInfo.TitlesRead {
		asin := strings.TrimSpace(t.ASIN)
		if asin == "" {
			continue
		}
		dt, raw := parseInsightsDate(t.DateRead)
		ins.TitlesRead = append(ins.TitlesRead, TitleRead{
			ASIN:         asin,
			DateRead:     dt,
			DateReadRaw:  raw,
			ContentType:  strings.TrimSpace(t.ContentType),
			ReadEventID:  strings.TrimSpace(t.ReadEventID),
			SourceOrigin: strings.TrimSpace(t.SourceOrigin),
		})
	}
	return ins, nil
}

// parseInsightsDate decodes a date_read value that may arrive as a "YYYY-MM-DD"
// (or RFC3339) string OR as an epoch number (seconds or milliseconds). It returns
// the parsed UTC time (zero on failure) plus the raw string form for debugging.
func parseInsightsDate(raw json.RawMessage) (time.Time, string) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return time.Time{}, ""
	}
	if s[0] == '"' { // JSON string
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return time.Time{}, ""
		}
		str = strings.TrimSpace(str)
		return parseDateString(str), str
	}
	// JSON number → epoch.
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return insightsEpoch(n), s
	}
	return time.Time{}, s
}

// parseDateString tries the date/time layouts Amazon plausibly uses, then falls
// back to treating a bare numeric string as an epoch.
func parseDateString(str string) time.Time {
	for _, layout := range []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, str); err == nil {
			return t.UTC()
		}
	}
	if n, err := strconv.ParseInt(str, 10, 64); err == nil {
		return insightsEpoch(n)
	}
	return time.Time{}
}

// insightsEpoch interprets an epoch as milliseconds when it is far too large to be
// seconds (≈ years past 33658 in seconds), else as seconds.
func insightsEpoch(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	if n > 1_000_000_000_000 { // 10^12 ⇒ milliseconds
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}

// flexString unmarshals from either a JSON string or a JSON number/bool into a
// string, so a streak field that flips type upstream never fails the decode.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b2 := strings.TrimSpace(string(b))
	if b2 == "" || b2 == "null" {
		*f = ""
		return nil
	}
	if b2[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(strings.Trim(b2, `"`))
	return nil
}

// flexInt unmarshals from a JSON number OR a numeric string into an int64.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b2 := strings.TrimSpace(string(b))
	if b2 == "" || b2 == "null" {
		*f = 0
		return nil
	}
	b2 = strings.Trim(b2, `"`)
	if b2 == "" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(b2, 10, 64)
	if err != nil {
		// A non-integer (e.g. float) shouldn't fail the whole streak — treat as 0.
		if fv, ferr := strconv.ParseFloat(b2, 64); ferr == nil {
			*f = flexInt(int64(fv))
			return nil
		}
		return nil
	}
	*f = flexInt(n)
	return nil
}

// parseStreak best-effort decodes a streak sub-object. A decode error is
// swallowed (the zero Streak is returned) so a streak-shape surprise is isolated
// from the titles_read parse.
func parseStreak(raw json.RawMessage) Streak {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return Streak{}
	}
	var w struct {
		Duration           flexInt    `json:"duration"`
		Start              flexString `json:"start"`
		End                flexString `json:"end"`
		UTCEndTime         flexString `json:"utcEndTime"`
		ReadingMarketplace flexString `json:"readingMarketplace"`
		TTL                flexInt    `json:"ttl"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Streak{}
	}
	return Streak{
		Duration:           int(w.Duration),
		Start:              string(w.Start),
		End:                string(w.End),
		UTCEndTime:         string(w.UTCEndTime),
		ReadingMarketplace: string(w.ReadingMarketplace),
		TTL:                int64(w.TTL),
	}
}

// parseAchievements best-effort decodes achievements_data.
func parseAchievements(raw json.RawMessage) Achievements {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return Achievements{}
	}
	var w struct {
		DaysLeft   flexInt         `json:"daysLeftInCurrentChallenge"`
		TotalAvail flexInt         `json:"totalAvailableAchievements"`
		TotalEarn  flexInt         `json:"totalEarnedAchievements"`
		Display    json.RawMessage `json:"achievementsDisplayAttributes"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Achievements{}
	}
	return Achievements{
		DaysLeftInCurrentChallenge: int(w.DaysLeft),
		TotalAvailableAchievements: int(w.TotalAvail),
		TotalEarnedAchievements:    int(w.TotalEarn),
		DisplayAttributes:          w.Display,
	}
}
