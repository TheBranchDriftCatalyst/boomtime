// leaderboards.go holds the multi-user leaderboard aggregation (requester-scoped
// hide/rename/space handling).
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetLeaderboards runs get_leaderboards.sql ($1 start,$2 end).
// leaderboardsRangeAnchor is the inner range-end clause of get_leaderboards.sql.
const leaderboardsRangeAnchor = "AND time_sent <= $2"

// GetLeaderboards aggregates coding time across ALL users. Both hide and rename
// apply to the REQUESTER's own rows only (multi-user safe: one user's curation
// must not alter other users' leaderboard contributions). Hide excludes with
// `AND NOT (sender = $req AND <col> = ANY($n))`; rename re-groups the requester's
// project/language via `CASE WHEN sender = $req AND col = ANY(..) THEN ..`.
func (d *DB) GetLeaderboards(ctx context.Context, start, end time.Time, requester string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]LeaderboardRow, error) {
	query := qGetLeaderboards
	args := []any{start, end}

	// A single $req param is reused by hide, rename, and space scope when active.
	requesterArg := 0
	next := 3
	ensureRequester := func() {
		if requesterArg == 0 {
			args = append(args, requester)
			requesterArg = len(args)
			next = len(args) + 1
		}
	}

	if hs.AnyHidden() {
		ensureRequester()
		var pred string
		pred, args, next = exclusionPredicate(hs, rawHeartbeatCols,
			fmt.Sprintf("sender = $%d", requesterArg), next, args)
		query = injectAfter(query, leaderboardsRangeAnchor, pred)
	}

	// Space scope (?space=): the requester's OWN rows are restricted to those
	// matching the Space's membership; other users' rows pass through unchanged
	// (the `sender <> $req` bypass arm). An empty or column-less scope matches
	// nothing for the requester (`sender <> $req OR FALSE`).
	if spaceRequested {
		ensureRequester()
		var pred string
		pred, args, next = spaceScopePredicate(ms, rawHeartbeatCols,
			fmt.Sprintf("sender <> $%d", requesterArg), next, args, spaceRequested)
		query = injectAfter(query, leaderboardsRangeAnchor, pred)
	}

	// Requester-scoped rename: re-group by remapped project/language (only the
	// requester's rows relabel; every other sender's project/language pass through).
	if rs.HasAxis("project") || rs.HasAxis("language") {
		ensureRequester()
		reqCond := fmt.Sprintf("sender = $%d", requesterArg)
		projExpr, langExpr := "project", "language"
		projExpr, args, next = rs.remapExpr("project", "project", reqCond, next, args)
		langExpr, args, next = rs.remapExpr("language", "language", reqCond, next, args)
		query = fmt.Sprintf(`SELECT %s AS project, %s AS language, sender, CAST(SUM(total_seconds) AS int8) AS total_seconds
FROM ( %s ) base
GROUP BY %s, %s, sender`, projExpr, langExpr, trimSQL(query), projExpr, langExpr)
	}

	out := []LeaderboardRow{}
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r LeaderboardRow
			if err := rows.Scan(&r.Project, &r.Language, &r.Sender, &r.TotalSeconds); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}
