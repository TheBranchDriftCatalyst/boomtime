package db

import (
	"context"
	"fmt"
	"time"
)

// ActiveFile is one cross-project "active file": how much attributed time was
// spent in an entity and how many DISTINCT projects touch it. Files that span
// several projects (Projects > 1) are shared interfaces / lynchpins.
type ActiveFile struct {
	Entity   string `json:"entity"`
	Seconds  int64  `json:"seconds"`
	Projects int64  `json:"projects"`
}

// GetActiveFiles returns the owner's top files across ALL projects (ty='file'
// only), ordered lynchpins-first (projects desc, then seconds desc) and capped
// at `limit`. Seconds is the attributed SUM(gap_seconds <= timeLimit*60); the
// project axis is curation-aware — hidden projects are excluded and rename rules
// are applied BEFORE the DISTINCT-project count, so a file's `projects` reflects
// the merged/hidden projects shown on the dashboards (this is a dashboard
// aggregate, not the raw audit). The second return reports truncation.
//
// The remap is applied per raw row in an inner select, then the outer query
// groups by entity: SUM(seconds) and COUNT(DISTINCT remapped-project). All
// match/new/hide values are bound params (injection-safe).
func (d *DB) GetActiveFiles(ctx context.Context, sender string, start, end time.Time, timeLimit int64, limit int, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]ActiveFile, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	// $1 sender, $2 start, $3 end, $4 timeLimit (gap cutoff minutes).
	args := []any{sender, start, end, timeLimit}

	// Project rename remap, applied per raw row before the DISTINCT count.
	projExpr, args, nextArg := rs.remapExpr("project", "project", "", 5, args)

	// Cap+1 to detect truncation.
	fetch := limit + 1
	query := fmt.Sprintf(`
WITH per_row AS (
    SELECT entity,
           %s AS project,
           CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END AS secs
    FROM heartbeats
    WHERE sender = $1 AND ty = 'file' AND entity IS NOT NULL AND entity <> ''
      AND time_sent >= $2 AND time_sent <= $3
)
SELECT entity,
       CAST(coalesce(sum(secs), 0) AS int8) AS seconds,
       CAST(count(DISTINCT project) AS int8) AS projects
FROM per_row
GROUP BY entity
ORDER BY projects DESC, seconds DESC, entity ASC
LIMIT %d`, projExpr, fetch)

	// Hide exclusion + space scope on the inner raw scan (spliced after the
	// range-end clause). Their args follow the remap's.
	query, args, _ = applyScopes(query, "AND time_sent <= $3",
		hs, ms, spaceRequested, rawHeartbeatCols, args, nextArg)

	rows, err := d.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := []ActiveFile{}
	for rows.Next() {
		var f ActiveFile
		if err := rows.Scan(&f.Entity, &f.Seconds, &f.Projects); err != nil {
			return nil, false, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	truncated := false
	if len(out) > limit {
		out = out[:limit]
		truncated = true
	}
	return out, truncated, nil
}
