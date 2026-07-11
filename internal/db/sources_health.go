package db

import (
	"context"
	"time"
)

// SourceHealth is one ingestion source — a (plugin, machine) pair — with its most
// recent check-in and heartbeat count. The plugin is what actually sends
// heartbeats (the wakatime editor plugin); scoping it per machine makes each
// physical setup a distinct source, so a single laptop going quiet is visible
// even when the same plugin still reports elsewhere. It is the raw data behind
// the "is my tracking still working" panel: status (active/idle/stale/silent) is
// derived CLIENT-side from LastSeen, so this stays a thin MAX(time_sent) query.
type SourceHealth struct {
	Plugin   string    `json:"plugin"`   // the wakatime plugin (heartbeat source)
	Machine  string    `json:"machine"`  // machine name ('unknown' when unset)
	LastSeen time.Time `json:"lastSeen"` // MAX(time_sent) (UTC) for this pair
	Count    int64     `json:"count"`    // total heartbeats from this pair
}

// ListSourceHealth returns, per (plugin, machine) pair, MAX(time_sent) and the
// heartbeat count for the owner. Heartbeats with no plugin are excluded (they
// have no real source); a missing machine collapses to 'unknown'. Ordered
// stalest-first (oldest lastSeen) so a silent source surfaces at the top.
// Owner-scoped.
func (d *DB) ListSourceHealth(ctx context.Context, sender string) ([]SourceHealth, error) {
	const query = `
		SELECT plugin,
		       COALESCE(NULLIF(machine, ''), 'unknown') AS machine,
		       max(time_sent) AS last_seen,
		       count(*)       AS cnt
		FROM heartbeats
		WHERE sender = $1 AND plugin IS NOT NULL AND plugin <> ''
		GROUP BY plugin, COALESCE(NULLIF(machine, ''), 'unknown')
		ORDER BY last_seen ASC`

	rows, err := d.Pool.Query(ctx, query, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SourceHealth{}
	for rows.Next() {
		var s SourceHealth
		if err := rows.Scan(&s.Plugin, &s.Machine, &s.LastSeen, &s.Count); err != nil {
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
