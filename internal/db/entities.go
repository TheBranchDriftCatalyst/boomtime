// entities.go: read + redact for the Entity Explorer (gaka-90x). Powers a
// per-type flat view of every entity value (heartbeats.entity) the user's
// heartbeats reference, plus a REDACT that blanks the entity column on the
// selected rows — the heartbeat rows themselves stay (project/language/time
// etc. still count toward totals), but the specific entity value is
// scrubbed. Typical use: purge caught websites (ty=url/domain) or noise
// files (ty=file) from audit views without dropping their contribution to
// coding-time.
//
// The list side is owner-scoped and hides already-redacted rows (`entity <>
// ''`) so a re-redact never re-lists the same blob. The redact side is
// owner-scoped AND caller-bounded on batch size. Because entity is NOT NULL
// with UNIQUE (entity, sender, time_sent), a rare collision — two rows at
// the same (sender, time_sent) but different entities both selected for
// redaction — will surface as a Postgres unique-violation and the caller
// should retry one entity at a time. Not observed in practice; users
// typically redact a handful of URLs / files.
package db

import (
	"context"
	"strings"
	"time"
)

// EntityType is one of the values heartbeats.ty stores. Kept as string
// constants for compile-time reuse from handlers.
const (
	EntityTypeFile   = "file"
	EntityTypeApp    = "app"
	EntityTypeDomain = "domain"
	EntityTypeURL    = "url"
)

// EntitySummary is one row of the Entity Explorer table.
type EntitySummary struct {
	Entity    string    `json:"entity"`
	Count     int64     `json:"count"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// ListEntitiesByType returns every distinct non-empty entity value the sender
// has under the given ty, with heartbeat count + first/last-seen. Ordered by
// count DESC so the noisiest values surface first. Truncated is true when
// the result hit the limit — the caller can prompt the user to filter or
// raise the cap. Redacted heartbeats (entity = '') are excluded so a
// re-redact operation never shows them as a phantom bucket.
//
// The list is case-folded: `src/Main.go` and `SRC/main.go` collapse into one
// row whose count/first/last-seen sum across all case variants and whose
// displayed value is picked via MODE() (most common raw casing). Consistent
// with dashboard aggregation.
func (d *DB) ListEntitiesByType(ctx context.Context, sender, ty string, limit int) ([]EntitySummary, bool, error) {
	// Fetch one extra to detect truncation.
	rows, err := d.Pool.Query(ctx, `
		SELECT MODE() WITHIN GROUP (ORDER BY entity) AS entity,
		       count(*) AS n, min(time_sent), max(time_sent)
		FROM heartbeats
		WHERE sender = $1 AND ty = $2 AND entity <> ''
		GROUP BY lower(entity)
		ORDER BY n DESC, entity
		LIMIT $3`, sender, ty, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]EntitySummary, 0)
	for rows.Next() {
		var e EntitySummary
		if err := rows.Scan(&e.Entity, &e.Count, &e.FirstSeen, &e.LastSeen); err != nil {
			return nil, false, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated, nil
}

// RedactEntities blanks the entity column ('') on every heartbeat matching
// (sender, ty, entity ∈ entities). Returns the number of rows changed.
// Owner-scoped by construction. Rows are NOT deleted — the heartbeat still
// counts toward project/language/machine totals — only the entity value is
// scrubbed. The rollup (which doesn't store entity) is unaffected, so no
// drift banner is needed after redact.
//
// Case-insensitive: passing "src/Main.go" also redacts rows stored as
// "src/main.go" or "SRC/MAIN.GO" — consistent with the case-folded list view.
// Values are lowercased Go-side so the SQL side is `lower(entity) = ANY($3)`
// which lets the caller pass any case and the correct raw rows are redacted.
func (d *DB) RedactEntities(ctx context.Context, sender, ty string, entities []string) (int64, error) {
	if len(entities) == 0 {
		return 0, nil
	}
	lowered := make([]string, len(entities))
	for i, e := range entities {
		lowered[i] = strings.ToLower(e)
	}
	tag, err := d.Pool.Exec(ctx, `
		UPDATE heartbeats SET entity = ''
		WHERE sender = $1 AND ty = $2 AND lower(entity) = ANY($3)`,
		sender, ty, lowered)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
