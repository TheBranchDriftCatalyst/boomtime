// rename_apply.go — the INGEST-TIME applier for apply_at_ingest rename rules
// (boom-scrub). A rename rule flagged apply_at_ingest rewrites the matching
// heartbeat field AS THE ROW IS STORED (internal/ingest.storeAndRespond calls
// LoadIngestRenameRules once per batch, then IngestRenameSet.Apply per row).
//
// This Go applier MUST mirror the Postgres query-time/destructive semantics
// (remap.go remapExpr, curation.go buildApplyUpdateSQL) so a rule behaves the
// same whichever way it's applied:
//   - exact:    lower(col) = match  → whole value replaced by new_value  (EqualFold)
//   - regex:    col ~* pattern      → whole value replaced by new_value   ((?i) + MatchString)
//   - template: col ~* pattern      → regexp_replace(col, pattern, tmpl, 'i')
//
// Two parity hazards handled here (see rename_apply_test.go for the PG-vs-Go proof):
//  1. regexp_replace uses flag 'i' only (NO 'g') → FIRST match only. Go's
//     ReplaceAllString replaces ALL, so template uses FindStringSubmatchIndex +
//     ExpandString on the first match and splices.
//  2. Postgres backrefs are `\N`; Go's Expand wants `${N}`. new_value is stored
//     in `\N` form (NormalizeTemplate). convertTemplateToGo bridges it.
package db

import (
	"context"
	"regexp"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// compiledRenameRule is one apply_at_ingest rename rule, regex pre-compiled.
type compiledRenameRule struct {
	matchType  string
	matchValue string         // exact: the literal to case-insensitively match
	re         *regexp.Regexp // regex/template: compiled "(?i)"+pattern (nil for exact)
	newValue   string         // exact/regex: whole-replacement value
	goTemplate string         // template: new_value converted from `\N` to `${N}`
}

// apply rewrites value per this rule, mirroring the Postgres semantics.
func (r compiledRenameRule) apply(value string) string {
	switch r.matchType {
	case MatchExact:
		if strings.EqualFold(value, r.matchValue) {
			return r.newValue
		}
	case MatchRegex:
		if r.re.MatchString(value) {
			return r.newValue
		}
	case MatchTemplate:
		// FIRST match only (regexp_replace without 'g'): expand the template
		// against the first match's submatches, splice into the original.
		loc := r.re.FindStringSubmatchIndex(value)
		if loc != nil {
			repl := r.re.ExpandString(nil, r.goTemplate, value, loc)
			return value[:loc[0]] + string(repl) + value[loc[1]:]
		}
	}
	return value
}

// IngestRenameSet is an owner's enabled apply_at_ingest rename rules, grouped by
// axis (rules within an axis stay id-ordered). Apply is pure in-memory.
type IngestRenameSet struct {
	byAxis map[string][]compiledRenameRule
}

// Empty reports whether the set has no rules (the ingest hot path skips Apply).
func (s IngestRenameSet) Empty() bool { return len(s.byAxis) == 0 }

// Apply rewrites hb's targeted fields in place. Runs LAST in storeAndRespond
// (after enrichment + placeholder substitution), so every axis is settable and
// no placeholder-skip is needed. *string fields are nil-guarded.
func (s IngestRenameSet) Apply(hb *model.HeartbeatPayload) {
	if hb == nil {
		return
	}
	for axis, rules := range s.byAxis {
		switch axis {
		case "entity":
			hb.Entity = applyRenameRules(rules, hb.Entity)
		case "project":
			applyRenamePtr(&hb.Project, rules)
		case "branch":
			applyRenamePtr(&hb.Branch, rules)
		case "language":
			applyRenamePtr(&hb.Language, rules)
		case "category":
			applyRenamePtr(&hb.Category, rules)
		case "machine":
			applyRenamePtr(&hb.Machine, rules)
		case "editor":
			applyRenamePtr(&hb.Editor, rules)
		case "plugin":
			applyRenamePtr(&hb.Plugin, rules)
		case "platform":
			applyRenamePtr(&hb.Platform, rules)
			// "sender" and any other axis are intentionally not scrubbable.
		}
	}
}

func applyRenameRules(rules []compiledRenameRule, value string) string {
	for _, r := range rules {
		value = r.apply(value)
	}
	return value
}

func applyRenamePtr(p **string, rules []compiledRenameRule) {
	if *p == nil {
		return
	}
	out := applyRenameRules(rules, **p)
	*p = &out
}

// ingestScrubbableAxis reports whether an axis maps to a settable heartbeat
// payload field (matches the switch in Apply). sender/others are excluded.
func ingestScrubbableAxis(axis string) bool {
	switch axis {
	case "entity", "project", "branch", "language", "category", "machine", "editor", "plugin", "platform":
		return true
	}
	return false
}

// LoadIngestRenameRules returns the sender's ENABLED, apply_at_ingest rename
// rules, compiled + grouped by axis (id-ordered). A rule whose pattern fails to
// compile under Go RE2, or targets a non-scrubbable axis, is skipped (never
// fatal — ingest must not fail because of one bad rule).
func (d *DB) LoadIngestRenameRules(ctx context.Context, sender string) (IngestRenameSet, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT axis, match_type, match_value, new_value FROM curation_rules
		WHERE sender = $1 AND action = 'rename' AND enabled = true
		  AND apply_at_ingest = true AND new_value IS NOT NULL
		ORDER BY id ASC`, sender)
	if err != nil {
		return IngestRenameSet{}, err
	}
	defer rows.Close()

	set := IngestRenameSet{byAxis: map[string][]compiledRenameRule{}}
	for rows.Next() {
		var axis, mtype, match, newv string
		if err := rows.Scan(&axis, &mtype, &match, &newv); err != nil {
			return IngestRenameSet{}, err
		}
		if !ingestScrubbableAxis(axis) {
			continue
		}
		cr := compiledRenameRule{matchType: mtype, matchValue: match, newValue: newv}
		if mtype == MatchRegex || mtype == MatchTemplate {
			re, cErr := regexp.Compile("(?i)" + match)
			if cErr != nil {
				continue // corrupt/RE2-incompatible pattern — skip, don't fail the batch
			}
			cr.re = re
			if mtype == MatchTemplate {
				cr.goTemplate = convertTemplateToGo(newv)
			}
		}
		set.byAxis[axis] = append(set.byAxis[axis], cr)
	}
	if len(set.byAxis) == 0 {
		set.byAxis = nil // so Empty() is cheap/true
	}
	return set, rows.Err()
}

// convertTemplateToGo turns a stored Postgres replacement template (`\N`
// backrefs, per NormalizeTemplate) into Go's Expand syntax (`${N}`). The brace
// form is required so `\12` (group 1 then literal '2') becomes `${1}2`, not Go
// group 12. A literal `$` is escaped to `$$` (Go treats `$` specially; PG does
// not). `\\` → `\`. Other `\x` escapes keep the escaped char literally.
func convertTemplateToGo(stored string) string {
	var b strings.Builder
	b.Grow(len(stored) + 8)
	for i := 0; i < len(stored); i++ {
		c := stored[i]
		switch {
		case c == '\\' && i+1 < len(stored):
			n := stored[i+1]
			switch {
			case n >= '0' && n <= '9':
				b.WriteString("${")
				b.WriteByte(n)
				b.WriteByte('}')
			case n == '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(n) // unknown escape → literal char
			}
			i++
		case c == '$':
			b.WriteString("$$")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ValidateIngestRenamePattern checks that an apply_at_ingest rename rule's
// pattern compiles under Go RE2 (the ingest apply engine). Curation's existing
// validation uses Postgres regex, which is more permissive (pattern backrefs,
// lookaround) — a PG-valid but RE2-invalid pattern would save yet silently
// no-op at ingest, so the CreateCuration handler runs this too. Exact needs no
// regex. Returns a user-safe error.
func ValidateIngestRenamePattern(matchType, pattern string) error {
	if matchType == MatchRegex || matchType == MatchTemplate {
		if _, err := regexp.Compile("(?i)" + pattern); err != nil {
			return err
		}
	}
	return nil
}
