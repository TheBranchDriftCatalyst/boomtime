package db

import (
	"context"
	"fmt"
	"time"
)

// SourceHealth is one ingestion source (an editor / plugin / machine value) with
// its most recent check-in and heartbeat count. It is the raw data behind the
// "is my tracking still working" panel: status (active/idle/stale/silent) is
// derived CLIENT-side from LastSeen, so this stays a thin MAX(time_sent) query.
type SourceHealth struct {
	Source   string    `json:"source"`   // the editor/plugin/machine value
	Kind     string    `json:"kind"`     // "editor" | "plugin" | "machine"
	LastSeen time.Time `json:"lastSeen"` // MAX(time_sent) (UTC) for this source
	Count    int64     `json:"count"`    // total heartbeats from this source
}

// sourceHealthKinds maps the FE-facing kind name to its trusted heartbeats
// column. These are a subset of the explore whitelist — the injection guard is
// that only these fixed, code-defined columns are ever interpolated.
var sourceHealthKinds = []struct{ kind, col string }{
	{"editor", "editor"},
	{"plugin", "plugin"},
	{"machine", "machine"},
}

// SourceHealth returns, per ingestion source (across editor/plugin/machine
// values), MAX(time_sent) and the heartbeat count for the owner. NULL/empty
// values are excluded (they are not real sources). Ordered stalest-first
// (oldest lastSeen), so a silent plugin surfaces at the top. Owner-scoped.
func (d *DB) SourceHealth(ctx context.Context, sender string) ([]SourceHealth, error) {
	// Build one UNION ALL branch per kind: SELECT '<kind>' AS kind, <col> AS
	// source, max(time_sent), count(*). The kind literal and column are both
	// code-defined (never user input), so the query has no injection surface.
	branch := `SELECT '%s' AS kind, %s AS source, max(time_sent) AS last_seen, count(*) AS cnt
		FROM heartbeats
		WHERE sender = $1 AND %s IS NOT NULL AND %s <> ''
		GROUP BY %s`

	branches := make([]string, 0, len(sourceHealthKinds))
	for _, k := range sourceHealthKinds {
		branches = append(branches, fmt.Sprintf(branch, k.kind, k.col, k.col, k.col, k.col))
	}
	query := ""
	for i, b := range branches {
		if i > 0 {
			query += "\nUNION ALL\n"
		}
		query += b
	}
	query += "\nORDER BY last_seen ASC"

	rows, err := d.Pool.Query(ctx, query, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SourceHealth{}
	for rows.Next() {
		var s SourceHealth
		if err := rows.Scan(&s.Kind, &s.Source, &s.LastSeen, &s.Count); err != nil {
			return nil, err
		}
		s.LastSeen = s.LastSeen.UTC()
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
