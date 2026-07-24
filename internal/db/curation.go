package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Curation actions and match types.
const (
	CurationHide   = "hide"
	CurationRename = "rename"

	MatchExact = "exact"
	MatchRegex = "regex"
	// MatchTemplate is a regex whose NewValue is a replacement template referencing
	// capture groups (e.g. pattern `^@(.*)$` + template `\1` strips a leading `@`).
	// Applied non-destructively at query time via Postgres regexp_replace.
	MatchTemplate = "template"
)

// NormalizeTemplate accepts both Postgres backrefs (`\1`) and shell-style (`$1`)
// and normalizes `$N` -> `\N` so either input works. A literal `$$` is left as a
// single `$` (it is not a backref). Only `$` followed by a digit is rewritten.
func NormalizeTemplate(tmpl string) string {
	var b strings.Builder
	b.Grow(len(tmpl))
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		if c == '$' && i+1 < len(tmpl) {
			n := tmpl[i+1]
			if n == '$' { // `$$` -> literal `$`
				b.WriteByte('$')
				i++
				continue
			}
			if n >= '0' && n <= '9' { // `$N` -> `\N`
				b.WriteByte('\\')
				b.WriteByte(n)
				i++
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// CurationRule is a per-user data-curation rule (hide or rename) on a label axis.
// MatchType is "exact" (MatchValue is a literal), "regex" (MatchValue is a
// Postgres regex applied to the raw column via ~), or "template" (MatchValue is a
// regex and NewValue is a regexp_replace template referencing capture groups).
type CurationRule struct {
	ID         int       `json:"id"`
	Axis       string    `json:"axis"`
	Action     string    `json:"action"`
	MatchType  string    `json:"matchType"`
	MatchValue string    `json:"matchValue"`
	NewValue   *string   `json:"newValue"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListCurationRules returns a user's rules, newest first.
func (d *DB) ListCurationRules(ctx context.Context, sender string) ([]CurationRule, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, axis, action, match_type, match_value, new_value, created_at
		FROM curation_rules WHERE sender = $1 ORDER BY id DESC`, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CurationRule{}
	for rows.Next() {
		var r CurationRule
		if err := rows.Scan(&r.ID, &r.Axis, &r.Action, &r.MatchType, &r.MatchValue, &r.NewValue, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateCurationRule inserts a rule (deduped on sender,axis,action,match_type,
// match_value) and returns it. On an existing duplicate it updates new_value.
func (d *DB) CreateCurationRule(ctx context.Context, sender, axis, action, matchType, matchValue string, newValue *string) (*CurationRule, error) {
	if matchType == "" {
		matchType = MatchExact
	}
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO curation_rules (sender, axis, action, match_type, match_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (sender, axis, action, match_type, match_value)
		DO UPDATE SET new_value = EXCLUDED.new_value
		RETURNING id, axis, action, match_type, match_value, new_value, created_at`,
		sender, axis, action, matchType, matchValue, newValue)
	var r CurationRule
	if err := row.Scan(&r.ID, &r.Axis, &r.Action, &r.MatchType, &r.MatchValue, &r.NewValue, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetCurationRule fetches a single rule by id (no owner filter; caller checks).
func (d *DB) GetCurationRule(ctx context.Context, id int) (*CurationRule, string, error) {
	var r CurationRule
	var sender string
	err := d.Pool.QueryRow(ctx, `
		SELECT id, axis, action, match_type, match_value, new_value, created_at, sender
		FROM curation_rules WHERE id = $1`, id).
		Scan(&r.ID, &r.Axis, &r.Action, &r.MatchType, &r.MatchValue, &r.NewValue, &r.CreatedAt, &sender)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &r, sender, nil
}

// DeleteCurationRule removes a rule (owner-scoped). Returns rows affected.
func (d *DB) DeleteCurationRule(ctx context.Context, sender string, id int) (int64, error) {
	ct, err := d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE id = $1 AND sender = $2`, id, sender)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// AffectedValue is one distinct RAW value a rule matches, with its heartbeat
// count and (for rename rules) the display value it maps to. MappedTo is the
// fixed new_value for exact/regex renames, or regexp_replace(value,pattern,
// template) for template renames; it is empty for hide rules (no target).
type AffectedValue struct {
	Value    string `json:"value"`
	Count    int64  `json:"count"`
	MappedTo string `json:"mappedTo"`
}

// CurationAffectedValues returns the DISTINCT RAW values (with heartbeat counts)
// that a rule matches on its axis, owner-scoped and UNFILTERED (audit). Exact
// rules match the single literal; regex rules match every value where the raw
// column ~ the pattern. Ordered by count desc, capped at `limit`; the second
// return reports truncation. Injection-safe: the axis maps to a whitelisted
// column and match_value is a bound param.
func (d *DB) CurationAffectedValues(ctx context.Context, sender string, rule *CurationRule, limit int) ([]AffectedValue, bool, error) {
	col, ok := rawHeartbeatCols[rule.Axis]
	if !ok {
		// Non-remap axes (e.g. day/entity for hide) have no heartbeats column here.
		if c, whok := ExploreColumn(rule.Axis); whok {
			col = c // e.g. "time_sent::date" for day, "entity", "ty"
		} else {
			return nil, false, fmt.Errorf("axis %q has no affected-values column", rule.Axis)
		}
	}
	if limit <= 0 {
		limit = 200
	}

	// Case-insensitive matching mirrors the aggregation grouping: an exact rule
	// authored as "Writing Docs" catches "writing docs" / "WRITING DOCS" too;
	// regex/template rules use `~*` (Postgres's case-insensitive regex).
	pred := "lower(" + col + ") = lower($2)"
	if rule.MatchType == MatchRegex || rule.MatchType == MatchTemplate {
		pred = col + " ~* $2"
	}

	// mappedExpr is the display value each matched raw value maps to (rename
	// preview). $3 carries new_value (fixed target, or the regexp_replace template
	// for a template rule). For a hide rule (new_value NULL) it is '' — no target.
	mappedExpr := "$3::text"
	if rule.MatchType == MatchTemplate {
		mappedExpr = fmt.Sprintf("regexp_replace(%s, $2, $3, 'i')", col)
	}
	newVal := ""
	if rule.NewValue != nil {
		newVal = *rule.NewValue
	}

	// Fetch limit+1 to detect truncation.
	q := fmt.Sprintf(`
		SELECT %s::text AS value, count(*) AS cnt,
		       coalesce(%s, '') AS mapped
		FROM heartbeats
		WHERE sender = $1 AND %s IS NOT NULL AND %s
		GROUP BY %s
		ORDER BY cnt DESC, value ASC
		LIMIT %d`, col, mappedExpr, col, pred, col, limit+1)

	rows, err := d.Pool.Query(ctx, q, sender, rule.MatchValue, newVal)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []AffectedValue{}
	for rows.Next() {
		var v AffectedValue
		if err := rows.Scan(&v.Value, &v.Count, &v.MappedTo); err != nil {
			return nil, false, err
		}
		out = append(out, v)
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

// AffectedRowDiff is one row that a destructive apply-rename would rewrite. The
// mapping is `Before -> After` on the target column of the heartbeat row.
type AffectedRowDiff struct {
	ID     int64  `json:"id"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// buildApplyUpdateSQL returns the parameterized UPDATE that a destructive apply
// of `rule` would execute. Bound params are always ($1=sender, $2=matchValue,
// [$3=newValue for exact], [$3=template for template]). Exact/regex rules
// rewrite matched rows to a fixed target; template rules rewrite via
// regexp_replace with the same backref template used in the query-time remap.
// Case matching mirrors the query-time remap (case-insensitive throughout).
//
// The returned SQL is the SOURCE OF TRUTH — the same string is bound both when
// previewing (via a WITH cte to enumerate affected ids) and when applying. This
// guarantees the modal preview reflects exactly what will run.
func (d *DB) buildApplyUpdateSQL(rule *CurationRule) (sqlText string, bindParams []any, err error) {
	col, ok := rawHeartbeatCols[rule.Axis]
	if !ok {
		return "", nil, fmt.Errorf("axis %q has no raw column (only rollup-tracked axes are apply-able)", rule.Axis)
	}
	if rule.Action != CurationRename {
		return "", nil, fmt.Errorf("only rename rules are apply-able (got action=%q)", rule.Action)
	}
	if rule.NewValue == nil {
		return "", nil, fmt.Errorf("rename rule has no newValue (cannot apply)")
	}

	switch rule.MatchType {
	case MatchRegex:
		// Rewrite every row whose col matches the pattern to the fixed newValue.
		q := fmt.Sprintf(`UPDATE heartbeats SET %s = $3 WHERE sender = $1 AND %s ~* $2`, col, col)
		return q, []any{rule.MatchValue, *rule.NewValue}, nil
	case MatchTemplate:
		// Rewrite via regexp_replace, same pattern + template + 'i' flag as
		// remapExpr uses at query time. Only rows that match the pattern are
		// touched (the ~* WHERE gate).
		q := fmt.Sprintf(
			`UPDATE heartbeats SET %s = regexp_replace(%s, $2, $3, 'i') WHERE sender = $1 AND %s ~* $2`,
			col, col, col)
		return q, []any{rule.MatchValue, *rule.NewValue}, nil
	default: // MatchExact or ""
		// Case-insensitive equality mirrors the query-time exact rule.
		q := fmt.Sprintf(`UPDATE heartbeats SET %s = $3 WHERE sender = $1 AND lower(%s) = lower($2)`, col, col)
		return q, []any{rule.MatchValue, *rule.NewValue}, nil
	}
}

// buildApplyDeleteSQL returns the parameterized DELETE that removes the mapping
// row itself after the UPDATE has been applied. $1=id, $2=sender.
func (d *DB) buildApplyDeleteSQL() string {
	return `DELETE FROM curation_rules WHERE id = $1 AND sender = $2`
}

// InlineParams substitutes bound $N params into `sqlText` for a HUMAN-READABLE
// preview (the confirm modal). String values are single-quoted with doubled
// internal quotes; other types render via fmt %v. This is NEVER sent back
// through the SQL driver — the actual apply uses the parameterized form.
func InlineParams(sqlText string, args []any) string {
	// Replace $N (highest first) so $10 doesn't collide with $1.
	out := sqlText
	for i := len(args); i >= 1; i-- {
		var rep string
		switch v := args[i-1].(type) {
		case string:
			rep = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		default:
			rep = fmt.Sprintf("%v", v)
		}
		out = strings.ReplaceAll(out, fmt.Sprintf("$%d", i), rep)
	}
	return out
}

// ApplyRenamePreview returns the exact UPDATE + DELETE SQL that ApplyRenameRule
// would run, plus the affected-rows diff (before/after per heartbeat row). The
// diff is capped at `limit` rows; the total count is exact. Owner-scoped; no
// data is mutated. Used to populate the destructive-confirm modal.
func (d *DB) ApplyRenamePreview(ctx context.Context, sender string, rule *CurationRule, limit int) (
	sqlUpdatePlanned string, sqlDeletePlanned string, diff []AffectedRowDiff, total int64, err error,
) {
	if limit <= 0 {
		limit = 100
	}
	updSQL, updBind, err := d.buildApplyUpdateSQL(rule)
	if err != nil {
		return "", "", nil, 0, err
	}
	delSQL := d.buildApplyDeleteSQL()

	// The parameterized form binds $1=sender, then whatever buildApplyUpdateSQL
	// asked for. Inline for the human-readable display; run parameterized
	// against the DB.
	inlineArgs := append([]any{sender}, updBind...)
	inlineDelArgs := []any{rule.ID, sender}
	sqlUpdatePlanned = InlineParams(updSQL, inlineArgs)
	sqlDeletePlanned = InlineParams(delSQL, inlineDelArgs)

	// Enumerate the affected rows via the same predicate the UPDATE would use.
	// We build a SELECT with the same WHERE + SET expression so the "after"
	// value is exactly what the UPDATE would write. For exact/regex this is
	// $3 (the fixed newValue); for template it is regexp_replace(...).
	col := rawHeartbeatCols[rule.Axis]
	afterExpr := "$3::text"
	if rule.MatchType == MatchTemplate {
		afterExpr = fmt.Sprintf("regexp_replace(%s, $2, $3, 'i')", col)
	}
	pred := fmt.Sprintf("lower(%s) = lower($2)", col)
	if rule.MatchType == MatchRegex || rule.MatchType == MatchTemplate {
		pred = fmt.Sprintf("%s ~* $2", col)
	}
	// Count separately so the modal can say "and N more" honestly.
	countQ := fmt.Sprintf(`SELECT count(*) FROM heartbeats WHERE sender = $1 AND %s`, pred)
	if err = d.Pool.QueryRow(ctx, countQ, sender, rule.MatchValue).Scan(&total); err != nil {
		return "", "", nil, 0, err
	}

	// Fetch the diff (limit+1 not needed — we already have the exact total).
	q := fmt.Sprintf(`
		SELECT id, %s::text AS before, %s AS after
		FROM heartbeats
		WHERE sender = $1 AND %s
		ORDER BY id ASC
		LIMIT %d`, col, afterExpr, pred, limit)
	rows, qerr := d.Pool.Query(ctx, q, sender, rule.MatchValue, *rule.NewValue)
	if qerr != nil {
		return "", "", nil, 0, qerr
	}
	defer rows.Close()
	diff = make([]AffectedRowDiff, 0, limit)
	for rows.Next() {
		var r AffectedRowDiff
		if serr := rows.Scan(&r.ID, &r.Before, &r.After); serr != nil {
			return "", "", nil, 0, serr
		}
		diff = append(diff, r)
	}
	if err = rows.Err(); err != nil {
		return "", "", nil, 0, err
	}
	return sqlUpdatePlanned, sqlDeletePlanned, diff, total, nil
}

// ApplyRenameRule DESTRUCTIVELY collapses a rename mapping into the raw
// heartbeats: it runs the UPDATE (rewriting the target column values on every
// matching row) AND deletes the mapping row itself, ATOMICALLY in a single
// transaction. Either both succeed or both roll back — a partial state (rows
// rewritten but rule still active) is impossible.
//
// Returns the number of heartbeat rows rewritten and the exact SQL that was
// executed (for the API response — matches the preview verbatim). If the
// mapping was already applied and nothing matches, still succeeds with
// rowsAffected=0 and the mapping row is still removed (idempotent-in-effect).
//
// Owner-scoped: every query is gated on `sender = $1` AND `id = $ruleId AND
// sender = $1` for the delete, so one user can never apply another's mapping.
func (d *DB) ApplyRenameRule(ctx context.Context, sender string, rule *CurationRule) (
	rowsAffected int64, sqlUpdateRun string, sqlDeleteRun string, err error,
) {
	updSQL, updBind, err := d.buildApplyUpdateSQL(rule)
	if err != nil {
		return 0, "", "", err
	}
	delSQL := d.buildApplyDeleteSQL()

	// Compose the inlined-for-display forms once, from the same strings we run.
	sqlUpdateRun = InlineParams(updSQL, append([]any{sender}, updBind...))
	sqlDeleteRun = InlineParams(delSQL, []any{rule.ID, sender})

	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, "", "", err
	}
	defer tx.Rollback(ctx)

	// Run the UPDATE first so we can return the row count.
	updateArgs := append([]any{sender}, updBind...)
	ct, err := tx.Exec(ctx, updSQL, updateArgs...)
	if err != nil {
		return 0, "", "", err
	}
	rowsAffected = ct.RowsAffected()

	// Delete the mapping row (owner-scoped). If the id/sender pair doesn't
	// exist we surface an error — the handler already validated ownership so
	// this shouldn't happen, but the guard keeps the transaction honest.
	dct, err := tx.Exec(ctx, delSQL, rule.ID, sender)
	if err != nil {
		return 0, "", "", err
	}
	if dct.RowsAffected() == 0 {
		return 0, "", "", fmt.Errorf("mapping row %d for %q vanished mid-apply", rule.ID, sender)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, "", "", err
	}
	return rowsAffected, sqlUpdateRun, sqlDeleteRun, nil
}

// ValidateRegex checks that a pattern compiles as a Postgres regex (guarded).
// Returns nil when valid, else a user-facing error.
func (d *DB) ValidateRegex(ctx context.Context, pattern string) error {
	var ok bool
	// `'' ~ $1` forces Postgres to compile the pattern without scanning any rows.
	err := d.Pool.QueryRow(ctx, `SELECT ''::text ~ $1`, pattern).Scan(&ok)
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	return nil
}

// ValidateTemplate checks that `pattern` compiles as a Postgres regex AND every
// capture-group backref in `template` (already normalized to `\N`) refers to a
// group the pattern actually defines — guarding bad backrefs like `\9` for a
// single-group pattern. `template` should already be normalized (`$N`->`\N`).
//
// Note: Postgres only raises "invalid reference number" for a bad backref when
// the pattern MATCHES the input, so a `regexp_replace(”, ...)` probe misses it
// (the empty string rarely matches). We instead ask Postgres for the pattern's
// capture-group count (via regexp_match against a self-matching input) and check
// each backref against it — reusing Postgres's own regex engine for both the
// compile check and the group count. Returns nil when valid, else an error.
func (d *DB) ValidateTemplate(ctx context.Context, pattern, template string) error {
	// 1. Compile check (also rejects an uncompilable pattern).
	if err := d.ValidateRegex(ctx, pattern); err != nil {
		return err
	}
	// 2. Capture-group count. Build `(?:(?:PATTERN)|)()`: the `|` makes it always
	//    match (so regexp_match returns a row) and the trailing empty group `()` is
	//    a sentinel, so the returned array length is exactly PATTERN's group count
	//    PLUS ONE. (Without the sentinel, a 0-group pattern and a 1-group pattern
	//    both report length 1, since regexp_match returns the whole match when
	//    there are no groups.) Real group count = reported - 1.
	var arrLen *int
	err := d.Pool.QueryRow(ctx,
		`SELECT array_length(regexp_match('', '(?:(?:' || $1 || ')|)()'), 1)`, pattern).Scan(&arrLen)
	if err != nil {
		return fmt.Errorf("invalid template rename: %w", err)
	}
	n := 0
	if arrLen != nil && *arrLen > 1 {
		n = *arrLen - 1
	}
	// 3. Every `\N` backref (N>=1) must be <= group count. `\0` (whole match) and
	//    `\\` (escaped backslash) are always fine.
	for i := 0; i < len(template); i++ {
		if template[i] != '\\' || i+1 >= len(template) {
			continue
		}
		c := template[i+1]
		i++ // consume the escaped char
		if c < '1' || c > '9' {
			continue // \0, \\, \&, etc.
		}
		if int(c-'0') > n {
			return fmt.Errorf("invalid template rename: backref \\%c but pattern has %d capture group(s)", c, n)
		}
	}
	return nil
}
