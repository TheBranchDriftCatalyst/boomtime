// projects.go holds project ownership checks, the projects list, and badge links.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---- Projects & badges ----

// checkProjectOwner reports whether the user owns the given project (raw name).
// Case-insensitive: "MyProject" and "myproject" both resolve to the same owner.
func (d *DB) checkProjectOwner(ctx context.Context, user, project string) (bool, error) {
	var name string
	err := d.Pool.QueryRow(ctx, `SELECT name FROM projects WHERE lower(name) = lower($1) AND owner = $2`, project, user).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// CheckProjectDisplayOwner reports whether the user owns a project whose DISPLAY
// name (after applying the project rename remap) equals `display`. This lets a
// merged/regex display name pass the project-detail ownership check even though no
// raw `projects` row carries that exact name. Falls back to the raw check when no
// project rename is active.
func (d *DB) CheckProjectDisplayOwner(ctx context.Context, user, display string, rs RenameSets) (bool, error) {
	if !rs.HasAxis("project") {
		return d.checkProjectOwner(ctx, user, display)
	}
	// $1 = user, $2 = display; the remap's pattern/target params start at $3.
	// Case-insensitive match on the DISPLAY value so a stored "MyProject" resolves
	// when the caller supplies "myproject" or a merged casing variant.
	args := []any{user, display}
	expr, args, _ := rs.remapExpr("project", "name", "", 3, args)
	q := fmt.Sprintf(`SELECT 1 FROM projects WHERE owner = $1 AND lower(%s) = lower($2) LIMIT 1`, expr)
	var one int
	err := d.Pool.QueryRow(ctx, q, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// GetAllProjects returns a user's projects with (non-hidden) heartbeats in
// [t0,t1], most active first. All hide axes are excluded on the heartbeats join.
// A project rename relabels names at read time (raw rows untouched): merged names
// collapse to one entry, most active first.
func (d *DB) GetAllProjects(ctx context.Context, user string, t0, t1 time.Time, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]string, error) {
	query := `
		SELECT name, count(*) AS cnt FROM projects
		INNER JOIN heartbeats ON heartbeats.project = projects.name AND heartbeats.sender = projects.owner
		WHERE heartbeats.sender = $1 AND heartbeats.time_sent >= $2 AND heartbeats.time_sent <= $3`
	// Hide exclusion + space scope splice in right after the range-end clause
	// (the current end of the WHERE), on the heartbeats-qualified columns.
	query, args, next := applyScopes(query, "AND heartbeats.time_sent <= $3",
		hs, ms, spaceRequested, projectListCols, []any{user, t0, t1}, 4)
	query += ` GROUP BY projects.name`

	// Always re-project through the rename remap (identity when no rule) AND
	// case-fold: merged/case-variant names collapse to one entry ordered by
	// summed activity, with a MODE-picked display casing.
	nameExpr := "name"
	nameExpr, args, next = rs.remapExpr("project", "name", "", next, args)
	query = fmt.Sprintf(`SELECT %s AS name FROM ( %s ) base GROUP BY lower(%s) ORDER BY SUM(cnt) DESC`,
		caseFoldPick(nameExpr), trimSQL(query), nameExpr)
	_ = next

	rows, err := d.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectStrings(rows)
}

// CreateBadgeLink upserts a badge link and returns its id.
func (d *DB) CreateBadgeLink(ctx context.Context, user, project string) (uuid.UUID, error) {
	var id uuid.UUID
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO badges(username, project) VALUES ($1, $2)
		ON CONFLICT (username, project) DO UPDATE SET username=EXCLUDED.username
		RETURNING link_id`, user, project).Scan(&id)
	return id, err
}

// GetBadgeLinkInfo returns the (username, project) for a badge id. ok is false
// (with a nil error) when no badge link exists for the id, following the
// missing-row convention used elsewhere in this package instead of leaking
// pgx.ErrNoRows to callers.
func (d *DB) GetBadgeLinkInfo(ctx context.Context, id uuid.UUID) (user, project string, ok bool, err error) {
	err = d.Pool.QueryRow(ctx, `SELECT username, project FROM badges WHERE link_id = $1`, id).Scan(&user, &project)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return user, project, true, nil
}

func collectStrings(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Import-job storage lives in importjobs.go (durable, resumable job records).
