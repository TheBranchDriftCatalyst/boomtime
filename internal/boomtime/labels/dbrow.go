// dbrow.go — bridge from db.Label (persisted JSONB row) to labels.LabelSpec
// (evaluator input). Mirrors the FE's dbRowToSpec in useLabelsCatalog.ts:
//
//   - condition JSON → decoded via UnmarshalCondition
//   - tierKey → derived from id ("{axis}-{value}-{tier}" convention)
//
// Isolated so gaka-hc6.3 (server endpoints) and gaka-hc6.6 (coverage test)
// share the same conversion instead of each writing its own.

package labels

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DBRow is the minimum shape SpecFromDBRow needs. Kept as a plain struct so
// callers can adapt from any concrete row type (db.Label, a test fixture,
// etc.) without importing this package into internal/db and creating a cycle.
type DBRow struct {
	ID            string
	Kind          string
	Label         string
	Glyph         string
	Description   string
	Rank          int
	Tier          string
	PeriodDefault string
	Condition     json.RawMessage
}

// SpecFromDBRow decodes one persisted catalog row into an evaluator-ready
// LabelSpec. The condition JSONB is parsed via UnmarshalCondition; on a
// decode failure the label is REJECTED (returned error) rather than silently
// swallowed — a bad seed row should be loud during boot.
//
// For kind='tier' rows, tierKey is reconstructed from the id. Convention:
// "{axis}-{value}-{tier}" (e.g. "languages-python-master") → "languages:python".
// Non-tier rows leave tierKey empty.
func SpecFromDBRow(r DBRow) (LabelSpec, error) {
	cond, err := UnmarshalCondition(r.Condition)
	if err != nil {
		return LabelSpec{}, fmt.Errorf("label %q: %w", r.ID, err)
	}
	spec := LabelSpec{
		ID:            r.ID,
		Kind:          LabelKind(r.Kind),
		Label:         r.Label,
		Glyph:         r.Glyph,
		Description:   r.Description,
		Rank:          r.Rank,
		Tier:          LabelTier(r.Tier),
		PeriodDefault: r.PeriodDefault,
		Condition:     cond,
	}
	if spec.Kind == KindTier && spec.Tier != "" {
		spec.TierKey = deriveTierKey(r.ID, r.Tier)
	}
	return spec, nil
}

// deriveTierKey mirrors the TS logic in useLabelsCatalog.ts dbRowToSpec.
// Strips the trailing "-{tier}" from the id, then swaps the FIRST dash to
// a colon: "languages-python-master" + "master" → "languages:python".
//
// If the id doesn't match the convention (no trailing tier match, or no
// dash after stripping) returns empty — the dedupe pass will treat the row
// as if it had no tierKey (each becomes its own key).
func deriveTierKey(id, tier string) string {
	if tier == "" || !strings.HasSuffix(id, "-"+tier) {
		return ""
	}
	withoutTier := strings.TrimSuffix(id, "-"+tier)
	firstDash := strings.IndexByte(withoutTier, '-')
	if firstDash <= 0 {
		return ""
	}
	return withoutTier[:firstDash] + ":" + withoutTier[firstDash+1:]
}

// SpecsFromDBRows converts a batch. Rejects on the first decode error —
// callers should treat that as "catalog is corrupt, refuse to evaluate"
// (safer than partially evaluating and skewing awards).
func SpecsFromDBRows(rows []DBRow) ([]LabelSpec, error) {
	out := make([]LabelSpec, 0, len(rows))
	for _, r := range rows {
		spec, err := SpecFromDBRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}
