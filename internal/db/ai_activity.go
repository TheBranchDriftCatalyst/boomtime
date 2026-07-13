// ai_activity.go: AI-assistance aggregation on the AI heartbeat columns
// captured in migration 00021 (gaka-1l9). Powers the "AI Assistance" card on
// Overview — per-day AI/human line-change split, prompt token totals,
// distinct AI-session count, and the latest subscription plan we've seen.
//
// Rollup is deliberately NOT used here: none of these columns live on
// hb_rollup_daily and the raw path is fast even on wide ranges (the
// per-owner (sender, time_sent) btree bounds the scan, and every AI
// aggregate uses SUM/COUNT which streams once). Space scoping is not
// applied — AI usage is a per-user cross-cutting metric.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// AIActivityDay is one row of the per-day time series.
type AIActivityDay struct {
	Day              time.Time `json:"day"`
	AIInputTokens    int64     `json:"aiInputTokens"`
	AIOutputTokens   int64     `json:"aiOutputTokens"`
	AILineChanges    int64     `json:"aiLineChanges"`
	HumanLineChanges int64     `json:"humanLineChanges"`
	AISessions       int64     `json:"aiSessions"`
}

// AIActivitySummary is the payload consumed by the Overview card.
type AIActivitySummary struct {
	Days                  []AIActivityDay `json:"days"`
	TotalInputTokens      int64           `json:"totalInputTokens"`
	TotalOutputTokens     int64           `json:"totalOutputTokens"`
	TotalAILineChanges    int64           `json:"totalAILineChanges"`
	TotalHumanLineChanges int64           `json:"totalHumanLineChanges"`
	TotalSessions         int64           `json:"totalSessions"`
	HeartbeatsWithAI      int64           `json:"heartbeatsWithAI"`
	LatestPlan            string          `json:"latestPlan,omitempty"`
	// HasData short-circuits the FE: renders nothing when false (user has no
	// AI-tagged heartbeats in the range, so no card to show).
	HasData bool `json:"hasData"`
}

// aiActivityFilter is the WHERE clause segment that keeps only rows the
// wakatime plugin actually populated with an AI signal — a heartbeat from a
// non-AI editor plugin has all-null AI columns and shouldn't drag the
// distinct-session count or the "how many AI-tagged hbs" summary down.
const aiActivityFilter = `
	(ai_input_tokens IS NOT NULL
	  OR ai_output_tokens IS NOT NULL
	  OR ai_line_changes IS NOT NULL
	  OR human_line_changes IS NOT NULL
	  OR ai_session IS NOT NULL)`

// GetAIActivity aggregates the AI heartbeat columns for a sender + range.
// Returns HasData=false with an empty summary when the range holds no
// AI-tagged heartbeats (so the FE can early-return without a card).
func (d *DB) GetAIActivity(ctx context.Context, sender string, start, end time.Time) (AIActivitySummary, error) {
	var s AIActivitySummary

	rows, err := d.Pool.Query(ctx, `
		SELECT
		    time_sent::date AS day,
		    COALESCE(SUM(ai_input_tokens), 0)::bigint    AS ai_in,
		    COALESCE(SUM(ai_output_tokens), 0)::bigint   AS ai_out,
		    COALESCE(SUM(ai_line_changes), 0)::bigint    AS ai_lines,
		    COALESCE(SUM(human_line_changes), 0)::bigint AS human_lines,
		    COUNT(DISTINCT ai_session)::bigint           AS sessions
		FROM heartbeats
		WHERE sender = $1
		  AND time_sent >= $2 AND time_sent <= $3
		  AND `+aiActivityFilter+`
		GROUP BY time_sent::date
		ORDER BY day`,
		sender, start, end)
	if err != nil {
		return s, err
	}
	defer rows.Close()

	s.Days = []AIActivityDay{}
	for rows.Next() {
		var d AIActivityDay
		if err := rows.Scan(&d.Day, &d.AIInputTokens, &d.AIOutputTokens,
			&d.AILineChanges, &d.HumanLineChanges, &d.AISessions); err != nil {
			return s, err
		}
		s.Days = append(s.Days, d)
		s.TotalInputTokens += d.AIInputTokens
		s.TotalOutputTokens += d.AIOutputTokens
		s.TotalAILineChanges += d.AILineChanges
		s.TotalHumanLineChanges += d.HumanLineChanges
	}
	if err := rows.Err(); err != nil {
		return s, err
	}

	// Summary row: heartbeat count + distinct sessions across the whole range
	// (NOT sum of per-day sessions — a session that spans days would count
	// twice) + the latest plan we've seen. Done in one round trip.
	err = d.Pool.QueryRow(ctx, `
		SELECT
		    COUNT(*)::bigint                              AS hb_with_ai,
		    COUNT(DISTINCT ai_session)::bigint            AS sessions,
		    COALESCE(
		        (SELECT ai_subscription_plan
		         FROM heartbeats
		         WHERE sender = $1
		           AND time_sent >= $2 AND time_sent <= $3
		           AND ai_subscription_plan IS NOT NULL
		         ORDER BY time_sent DESC
		         LIMIT 1),
		        ''
		    ) AS latest_plan
		FROM heartbeats
		WHERE sender = $1
		  AND time_sent >= $2 AND time_sent <= $3
		  AND `+aiActivityFilter,
		sender, start, end).
		Scan(&s.HeartbeatsWithAI, &s.TotalSessions, &s.LatestPlan)
	if err != nil && err != pgx.ErrNoRows {
		return s, err
	}
	s.HasData = s.HeartbeatsWithAI > 0
	return s, nil
}
