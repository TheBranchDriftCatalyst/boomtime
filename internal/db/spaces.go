package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A Space is a named, user-scoped filter. Its membership is defined by rules
// {axis, matchValue, matchType} where matchType is "exact" | "regex". A heartbeat
// is "in" a Space if it matches ANY rule (a union across axes). Each scoped
// dashboard applies an inclusion predicate — the mirror of exclusionPredicate.

// Space is a named scope with a rule count (list view).
type Space struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	RuleCount int    `json:"ruleCount"`
}

// SpaceRule is one membership rule on a label axis.
type SpaceRule struct {
	ID         int    `json:"id"`
	Axis       string `json:"axis"`
	MatchValue string `json:"matchValue"`
	MatchType  string `json:"matchType"`
}

// axisMembers holds one axis's membership rules split by match type. Exact values
// become a single `col = ANY($arr)` arm; each regex value is its own `col ~ $n` arm.
type axisMembers struct {
	exact []string
	regex []string
}

// MemberSets holds a Space's membership rules per axis — the inclusion mirror of
// HiddenSets. Empty (zero rules) means the scope matches nothing (see
// spaceScopePredicate), so a rule-less Space renders an empty dashboard.
type MemberSets struct {
	byAxis map[string]axisMembers
}

// AnyMember reports whether the Space has any membership rule.
func (ms MemberSets) AnyMember() bool {
	for _, a := range ms.byAxis {
		if len(a.exact) > 0 || len(a.regex) > 0 {
			return true
		}
	}
	return false
}

// HasMemberOutside reports whether any membership-rule axis is NOT in the provided
// available set. Mirrors HasHiddenOutside: used to force the raw path when a Space
// rule targets an axis a pre-aggregated table (the rollup) lacks.
func (ms MemberSets) HasMemberOutside(available map[string]bool) bool {
	for axis, a := range ms.byAxis {
		if (len(a.exact) > 0 || len(a.regex) > 0) && !available[axis] {
			return true
		}
	}
	return false
}

// LoadMemberSets reads one space's rules (owner is enforced by the caller passing
// a space id already resolved to the owner, or by GetSpace). It groups rules by
// axis + match type, validating each axis against the ExploreColumn whitelist
// (unknown axes are skipped defensively — creation already rejects them).
func (d *DB) LoadMemberSets(ctx context.Context, spaceID int) (MemberSets, error) {
	ms := MemberSets{byAxis: map[string]axisMembers{}}
	rows, err := d.Pool.Query(ctx,
		`SELECT axis, match_value, match_type FROM space_rules WHERE space_id = $1 ORDER BY id ASC`, spaceID)
	if err != nil {
		return ms, err
	}
	defer rows.Close()
	for rows.Next() {
		var axis, matchValue, matchType string
		if err := rows.Scan(&axis, &matchValue, &matchType); err != nil {
			return ms, err
		}
		if _, ok := ExploreColumn(axis); !ok {
			continue // defensive: creation validates the axis whitelist
		}
		a := ms.byAxis[axis]
		if matchType == MatchRegex {
			a.regex = append(a.regex, matchValue)
		} else {
			a.exact = append(a.exact, matchValue)
		}
		ms.byAxis[axis] = a
	}
	return ms, rows.Err()
}

// inclusionPredicate builds ` AND ( <arm> OR <arm> ... )` — the union of every
// membership rule across axes present in cols (axis -> SQL column expression).
// Per axis: exact values become one `col = ANY($n)` arm (bound as a text[] arg);
// each regex becomes a `col ~ $n` arm. Axes are iterated in the deterministic
// hiddenAxes order. All values are bound params (injection-safe); the axis -> column
// mapping comes only from cols. Callers must guard with AnyMember() — an empty
// MemberSets here returns no predicate (use spaceScopePredicate for the
// match-nothing semantics).
func inclusionPredicate(ms MemberSets, cols map[string]string, nextArg int, args []any) (string, []any, int) {
	var arms []string
	for _, axis := range hiddenAxes { // deterministic order
		a := ms.byAxis[axis]
		col := cols[axis]
		if col == "" || (len(a.exact) == 0 && len(a.regex) == 0) {
			continue
		}
		if len(a.exact) > 0 {
			arms = append(arms, fmt.Sprintf("%s = ANY($%d)", col, nextArg))
			args = append(args, a.exact)
			nextArg++
		}
		for _, pat := range a.regex {
			arms = append(arms, fmt.Sprintf("%s ~ $%d", col, nextArg))
			args = append(args, pat)
			nextArg++
		}
	}
	if len(arms) == 0 {
		return "", args, nextArg
	}
	sql := " AND ("
	for i, arm := range arms {
		if i > 0 {
			sql += " OR "
		}
		sql += arm
	}
	sql += ")"
	return sql, args, nextArg
}

// spaceScopePredicate returns the inclusion predicate to splice into an aggregation
// query for a scoped (?space=) request. When a Space is requested but has no rules
// (or none map onto cols), the scope must match NOTHING — it emits ` AND FALSE` so
// a rule-less Space renders an empty dashboard, not the full unscoped one. When no
// Space is requested it returns no predicate (the normal unscoped path).
func spaceScopePredicate(ms MemberSets, cols map[string]string, nextArg int, args []any, spaceRequested bool) (string, []any, int) {
	if !spaceRequested {
		return "", args, nextArg
	}
	if !ms.AnyMember() {
		return " AND FALSE", args, nextArg
	}
	pred, args, nextArg := inclusionPredicate(ms, cols, nextArg, args)
	if pred == "" {
		// Rules exist but none map onto this query's columns (e.g. an axis the
		// pre-aggregated table lacks). The scope still can't include anything here.
		return " AND FALSE", args, nextArg
	}
	return pred, args, nextArg
}

// ---- Space + rule CRUD (all owner-scoped) ----

// ListSpaces returns a user's spaces (ordered by position, then id) with rule counts.
func (d *DB) ListSpaces(ctx context.Context, owner string) ([]Space, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT s.id, s.name, s.position, count(r.id) AS rule_count
		FROM spaces s
		LEFT JOIN space_rules r ON r.space_id = s.id
		WHERE s.owner = $1
		GROUP BY s.id
		ORDER BY s.position ASC, s.id ASC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Space{}
	for rows.Next() {
		var s Space
		if err := rows.Scan(&s.ID, &s.Name, &s.Position, &s.RuleCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSpace returns one owner-scoped space and its rules, or (nil,nil,nil) when the
// space is missing or owned by someone else.
func (d *DB) GetSpace(ctx context.Context, owner string, id int) (*Space, []SpaceRule, error) {
	var s Space
	err := d.Pool.QueryRow(ctx,
		`SELECT id, name, position FROM spaces WHERE id = $1 AND owner = $2`, id, owner).
		Scan(&s.ID, &s.Name, &s.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT id, axis, match_value, match_type FROM space_rules WHERE space_id = $1 ORDER BY id ASC`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	rules := []SpaceRule{}
	for rows.Next() {
		var r SpaceRule
		if err := rows.Scan(&r.ID, &r.Axis, &r.MatchValue, &r.MatchType); err != nil {
			return nil, nil, err
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	s.RuleCount = len(rules)
	return &s, rules, nil
}

// CreateSpace inserts a new space at the end (max position + 1) and returns it.
func (d *DB) CreateSpace(ctx context.Context, owner, name string) (*Space, error) {
	var s Space
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO spaces (owner, name, position)
		VALUES ($1, $2, COALESCE((SELECT max(position) + 1 FROM spaces WHERE owner = $1), 0))
		RETURNING id, name, position`, owner, name).
		Scan(&s.ID, &s.Name, &s.Position)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// RenameSpace updates a space's name and/or position (owner-scoped). name/position
// are applied when non-nil. Returns rows affected.
func (d *DB) RenameSpace(ctx context.Context, owner string, id int, name *string, position *int) (int64, error) {
	ct, err := d.Pool.Exec(ctx, `
		UPDATE spaces
		SET name = COALESCE($3, name), position = COALESCE($4, position)
		WHERE id = $1 AND owner = $2`, id, owner, name, position)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// DeleteSpace removes a space (owner-scoped); its rules cascade. Returns rows affected.
func (d *DB) DeleteSpace(ctx context.Context, owner string, id int) (int64, error) {
	ct, err := d.Pool.Exec(ctx, `DELETE FROM spaces WHERE id = $1 AND owner = $2`, id, owner)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// spaceOwned reports whether owner owns the given space id.
func (d *DB) spaceOwned(ctx context.Context, owner string, spaceID int) (bool, error) {
	var one int
	err := d.Pool.QueryRow(ctx, `SELECT 1 FROM spaces WHERE id = $1 AND owner = $2`, spaceID, owner).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// AddSpaceRule validates + inserts a membership rule on an owner-scoped space and
// returns it. The axis must be in the ExploreColumn whitelist; matchType must be
// "exact" or "regex" (a regex pattern is compile-checked via ValidateRegex).
// Returns (nil,nil) when the space is missing / not owned.
func (d *DB) AddSpaceRule(ctx context.Context, owner string, spaceID int, axis, matchValue, matchType string) (*SpaceRule, error) {
	if matchType == "" {
		matchType = MatchExact
	}
	if matchType != MatchExact && matchType != MatchRegex {
		return nil, fmt.Errorf("matchType must be 'exact' or 'regex'")
	}
	if _, ok := ExploreColumn(axis); !ok {
		return nil, fmt.Errorf("unknown axis: %s", axis)
	}
	if matchValue == "" {
		return nil, fmt.Errorf("matchValue is required")
	}
	if matchType == MatchRegex {
		if err := d.ValidateRegex(ctx, matchValue); err != nil {
			return nil, err
		}
	}
	owned, err := d.spaceOwned(ctx, owner, spaceID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, nil
	}
	var r SpaceRule
	err = d.Pool.QueryRow(ctx, `
		INSERT INTO space_rules (space_id, axis, match_value, match_type)
		VALUES ($1, $2, $3, $4)
		RETURNING id, axis, match_value, match_type`, spaceID, axis, matchValue, matchType).
		Scan(&r.ID, &r.Axis, &r.MatchValue, &r.MatchType)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteSpaceRule removes a rule from an owner-scoped space. Returns rows affected
// (0 when the space is not owned or the rule does not belong to it).
func (d *DB) DeleteSpaceRule(ctx context.Context, owner string, spaceID, ruleID int) (int64, error) {
	ct, err := d.Pool.Exec(ctx, `
		DELETE FROM space_rules r
		USING spaces s
		WHERE r.id = $1 AND r.space_id = $2 AND s.id = r.space_id AND s.owner = $3`,
		ruleID, spaceID, owner)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// SpacePreviewValues returns the DISTINCT RAW values (with heartbeat counts) that
// an UNSAVED membership rule would match on its axis, owner-scoped and unfiltered.
// Reuses the exact logic/shape of CurationAffectedValues; powers the form's live
// preview. Ordered by count desc, capped at `limit`; the second return reports
// truncation. Injection-safe (axis -> whitelisted column, matchValue bound).
func (d *DB) SpacePreviewValues(ctx context.Context, owner, axis, matchValue, matchType string, limit int) ([]AffectedValue, bool, error) {
	if matchType == "" {
		matchType = MatchExact
	}
	// Preview reuses the rule-affected-values machinery: a hide-style CurationRule
	// carries no target (MappedTo comes back empty, which the preview ignores).
	rule := &CurationRule{Axis: axis, MatchType: matchType, MatchValue: matchValue, Action: CurationHide}
	return d.CurationAffectedValues(ctx, owner, rule, limit)
}
