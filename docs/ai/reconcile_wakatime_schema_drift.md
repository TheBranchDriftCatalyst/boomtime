# Reconcile wakatime.com schema drift

**Purpose.** Take the JSON payload produced by the DriftBanner's *"Copy JSON (with instructions)"* button
(`web/src/features/import/DriftBanner.tsx`, gaka-rl6) and land a full backend +
frontend reconciliation: swallow the noise, persist the useful fields, unlock
the graphs those fields enable. This document is the runbook — paste the drift
JSON below it in a coding-agent session and follow the sections in order.

The canonical reference implementation is commit `6b4724c` (gaka-1l9), which
absorbed 15 fields (7 heartbeat AI metrics + 8 lookup metadata) and shipped an
Overview `AIAssistanceCard` in a single change. Everything below mirrors that
shape.

---

## Input contract

The DriftBanner emits:

```json
{
  "source": "boomtime import drift",
  "capturedAt": "<iso>",
  "instructions": "Extend the wakatime.com schema definitions in internal/importer/drift.go … ",
  "findings": [
    {
      "endpoint": "heartbeats" | "user_agents" | "machine_names" | "all_time_since_today",
      "field":    "<json_key>",
      "kind":     "unknown_field" | "missing_required" | "type_changed" | "envelope_changed",
      "detail":   "<optional>",
      "severity": "warning" | "error",
      "firstSeenDay": "YYYY-MM-DD" | "",
      "count":    <int>
    },
    …
  ]
}
```

Every finding has an endpoint + a field. Different endpoints hit different
schemaSpecs (`heartbeatSpec`, `lookupSpec`, `allTimeSpec` in
`internal/importer/drift.go`). Different kinds imply different fixes:

| kind | what happened | what to do |
|---|---|---|
| `unknown_field` | wakatime returned a key we neither `known` nor `baseline` | **triage: persist or baseline** (see next) |
| `missing_required` | a field we require was missing on some rows | check if it's truly load-bearing; consider marking optional or fixing the decoder |
| `type_changed` | a `known` field arrived as a different JSON type | update the `jsonType` in the spec + retype the decoder + migration if column type must change |
| `envelope_changed` | the outer `{data: …}` shape shifted | rare; investigate the specific endpoint before assuming a fix |

The rest of this doc is scoped to `unknown_field`, which is what 99% of drift
looks like in practice.

---

## Triage rubric

For every `unknown_field` finding, decide one of three tiers:

1. **Persist as a heartbeat column** (per-row analytic value that unlocks a chart).
   *Signals:* the field lives on `heartbeats`; the value varies per row; it's a
   count / measure / identifier you'd want to `SUM` / `COUNT DISTINCT` /
   filter on. Examples: `ai_input_tokens`, `human_line_changes`, `ai_session`.

2. **Baseline (swallow silently)** (per-lookup metadata, or per-row context we
   don't currently visualize).
   *Signals:* the field lives on `user_agents` / `machine_names` (metadata about
   the sender's environment, not per-row data); OR the field lives on
   `heartbeats` but is essentially display-only and we don't have a plan
   for it. Examples: `cli_version`, `go_version`, `ai_subscription_plan` (arguable —
   we persisted it in gaka-1l9 so the AI card can show the plan chip; had
   we no such use it would go here).

3. **`missing_required` action** — see the table above; not part of the
   `unknown_field` flow.

**Sizing decision:** if the field will become a chart on its own it goes to
tier 1. If it will only ever appear on a hover tooltip, tier 2 is fine.
If in doubt, prefer tier 2 (baseline) — you can always promote it later
without a migration; demoting later is a schema change.

---

## Backend changes (in order)

Every step below cites a specific file so an agent can jump straight in. All
line numbers are approximate — grep for the anchor string, don't trust the
number.

### 1. Silence baseline findings first (fast dopamine)

For every tier-2 finding, add a single line to the right `baseline` map in
`internal/importer/drift.go`.

- **`heartbeats` endpoint** → `heartbeatSpec.baseline` (anchor: `"line_additions"`).
- **`user_agents` and `machine_names` endpoints** → `lookupSpec.baseline` (anchor: `"is_desktop_app"`).
- **`all_time_since_today`** → `allTimeSpec.baseline` (anchor: `"writes_only"`).

Format each line with a trailing comment noting the source endpoint when it's
not obvious (gaka-1l9's `lookupSpec.baseline` uses `// user_agents` /
`// machine_names` to disambiguate — mirror that).

Rebuild + rerun the failed import — the tier-2 warnings should disappear.
This is a small commit on its own if you're time-boxed.

### 2. For every tier-1 heartbeat field, pick columns

Design the DB shape before touching the migration. For each field:

| wakatime json type | Postgres column type |
|---|---|
| integer (tokens / line counts / lengths) | `INTEGER` (nullable) — or `BIGINT` if it could plausibly overflow int32 (rare for per-heartbeat counters). |
| string identifier (session id, plan name) | `TEXT` (nullable). |
| float | `DOUBLE PRECISION`. |
| bool | `BOOLEAN`. |
| enum-ish string | `TEXT` with an app-level whitelist (don't add a CHECK constraint unless you're sure the enum is stable — wakatime adds values). |

Every column is nullable. Non-AI plugins simply store NULL; the aggregate
queries must `COALESCE(..., 0)` for sums and treat NULL as "not applicable"
for distinct-counts.

### 3. Write the migration

Path: `internal/db/migrations/000NN_heartbeats_<theme>_fields.sql` where NN is
one past the current highest migration (grep `ls internal/db/migrations/`).

Template (mirror `00021_heartbeats_ai_fields.sql`):

```sql
-- +goose Up
-- gaka-<id>: <one-liner about what was captured>. Persisting them here
-- unlocks <named surface>. All columns are nullable; heartbeats from a
-- plugin that doesn't emit these fields simply bind NULL, and every
-- aggregation COALESCEs to 0 / ignores nulls. Rollup unaffected — these
-- columns aren't rollup axes and hb_rollup_daily doesn't touch them.
ALTER TABLE heartbeats
    ADD COLUMN <col1> <type>,
    ADD COLUMN <col2> <type>;

-- +goose Down
ALTER TABLE heartbeats
    DROP COLUMN <col1>,
    DROP COLUMN <col2>;
```

### 4. Add to the schemaSpec `known` map

In `internal/importer/drift.go`, extend `heartbeatSpec.known` with the new
fields, using the correct `jsonType` (see the file for the enum:
`jtNumberOrNull`, `jtStringOrNull`, `jtBoolOrNull`, `jtArrayOrNull`, etc.).
Group under a comment tagged with the bead id (gaka-1l9 uses `// gaka-1l9:
wakatime.com's AI-assistance heartbeat fields (first seen 2026-07-03).
Persisted to matching heartbeats columns.`).

### 5. Thread through the decoder / model / write path

Five files touch this — do them in order or the build breaks midway:

1. **`internal/importer/importer.go`** — extend `type importHeartbeat` with
   the new fields (grep for `MachineNameID *string`). Use `*int64` for
   `INTEGER`, `*string` for `TEXT`, `*float64` for `DOUBLE PRECISION`,
   `*bool` for `BOOLEAN`. Every field is a pointer because wakatime may
   omit them per-heartbeat.

2. Same file, **`convertForDB`** (grep `out = append(out, model.HeartbeatPayload`)
   — pass every new field through. This is a mechanical mapping; use the
   `hb.NewField` naming so it's obvious what wakatime emitted.

3. **`internal/model/heartbeat.go`** — extend `HeartbeatPayload` (the public
   ingest shape) with the same fields. **Use `,omitempty`** on the JSON tags
   so heartbeats from non-AI clients don't waste bytes serializing null keys.

4. **`internal/db/queries/insert_heartbeat.sql`** — append the new columns to
   both the column list and the `VALUES` clause, in the same order. Update
   the `$N` numbers.

5. **`internal/db/ingest.go`, `insertHeartbeatsBatch`** — bind the values in
   `b.Queue(qInsertHeartbeat, …)` in the exact same order as the SQL.

Run `go build ./...` after each of steps 3-5; the compiler will tell you if
you missed a field.

### 6. Write the aggregation

New file: `internal/db/<theme>_activity.go` (mirror `internal/db/ai_activity.go`).

**Shape**: expose a single `Get<Theme>Activity(ctx, sender, start, end)` that
returns a summary struct with:
- a per-day time series (`Days []<Theme>Day`) so charts can plot trends,
- range totals (`Total…`) for headline stats,
- any range-level derived fields (e.g. latest plan, distinct session count —
  distinct across the WHOLE range, NOT the sum of per-day distincts, which
  would double-count sessions that span midnight),
- `HasData bool` so the FE can skip render when the range holds no rows.

**Filter**: the WHERE clause must exclude rows where *every* new column is
NULL — otherwise a distinct-session count / summary picks up all the non-AI
heartbeats too. Codify the filter as a `const` (`aiActivityFilter` in the
reference) so the two queries (per-day + summary) can't drift apart.

**No rollup**: none of these columns live on `hb_rollup_daily`. Skip the
rollup fast-path gate; the (sender, time_sent) btree keeps the raw scan
fast even on wide ranges.

**No space scoping**: unless the metric is inherently project-scoped, keep
these aggregates cross-cutting (AI usage / editor-source usage / etc. is a
per-user signal).

### 7. Add the HTTP handler + route

New handler in `internal/handler/bigbets.go` (or a new file if the theme
deserves it). Mirror the `AIActivity` handler:

```go
func (h *Handler) <Theme>Activity(c *echo.Context) error {
    s, aerr := h.dashboardScope(c, 30)   // 30-day default
    if aerr != nil { return respondErr(c, aerr) }
    return h.cachedJSON(c, s.cacheKey("<theme>-activity", s.t0, s.t1), func() (any, error) {
        return h.DB.Get<Theme>Activity(s.ctx, s.owner, s.t0, s.t1)
    })
}
```

Register in `internal/server/server.go` alongside the other stats routes
(anchor: `e.GET("/api/v1/users/current/stats/momentum"`).

Path convention: `/api/v1/users/current/stats/<theme>` (lowercase, no
hyphens if possible — `ai` not `ai-activity`).

---

## Frontend changes

### 8. Types

Add to `web/src/types/stats.ts` (anchor: `export interface StatsPayload`),
using camelCase keys that mirror the Go JSON tags. Include the `hasData`
field.

### 9. API client

In `web/src/lib/api.ts`, add a `get<Theme>Activity(params: RangeParams)`
alongside the other stats methods (anchor: `getMomentum`). Add the payload
type to the imports at the top of the file.

### 10. Query key

In `web/src/lib/queryKeys.ts`:
- Add `<theme>Activity: ["<theme>-activity"] as const` to the `prefix` block.
- Add `<theme>Activity: (start, end) => ["<theme>-activity", start, end] as const` to
  the `qk` block. Include a `space` param only if the endpoint honours it.

### 11. The chart(s)

Pick a chart shape per the rubric:

| Data shape | Chart | Existing example |
|---|---|---|
| 3–6 headline totals + one horizontal ratio (A vs B) | **Mini-stat strip + ratio bar** in a `ChartCard` | `web/src/features/overview/AIAssistanceCard.tsx` |
| Per-day time-series of one metric | **ColumnChart** or **CumulativeArea** | See usages in `OverviewDashboard.tsx` |
| Per-day time-series of N metrics | **HeatmapChart** (rows = metric) | `Activity per language` in `OverviewDashboard.tsx` |
| Weekly breakdown by project/category | **MomentumGrid** | `Project momentum` panel |
| Cross-axis rhythm (dow × hour) | **Punchcard** | `Coding punchcard` panel |

**Sizing:** first chart is always a headline strip (fits in a `ChartCard` in
the top ~2 rows of Overview). Deeper drill-downs go on their own dedicated
route later — don't over-stuff Overview.

**Self-hiding:** every new card MUST early-return `null` when
`data.hasData === false`. Non-AI users, first-time users, and users on
plans that don't include the metric will otherwise see an empty card.

Component template — copy `AIAssistanceCard.tsx` and rename. Keep:
- `useMemo` for derived percentages / ratios,
- the `MiniStat` sub-component (four-tile grid),
- the horizontal bar as two `<div style={{width: pct%}} />` with hover titles,
- the `fmt()` number formatter (k / M suffixes).

### 12. Wire into Overview

In `web/src/features/overview/OverviewDashboard.tsx`:
- Add a `useQuery({ queryKey: qk.<theme>Activity(...), queryFn: () => api.get<Theme>Activity(...) })`
  next to the other big-bet queries.
- Import the new card component.
- Insert `<<Theme>Card data={query.data} />` in the render tree. Position:
  right after the top `StatCard` grid, above `Category breakdown`, if the
  metric is a headline; otherwise inside the "Patterns" section further down.

---

## Testing

Reference implementation added no new tests for the AI capture (the
persist path was covered by the existing `TestSaveHeartbeats*` suite, and
the aggregation is a straight-line SQL). Add tests when:

- **The rewrite is non-trivial** (e.g. rename expansion in gaka-xuc got a
  dedicated `widgets_test.go`). Add a pure-Go unit test in
  `internal/db/<theme>_activity_test.go` locking the summary math on a
  fixture.
- **The FE has branching logic** (e.g. `AIAssistanceCard` renders `null`
  when `!hasData`, computes a ratio only when total > 0). Add a `*.test.tsx`
  with `renderWithProviders` + MSW handler mocking the new endpoint.

Gates before shipping (run from repo root):

```bash
go build ./...
go test ./...
cd web && yarn tsc --noEmit && yarn vitest run
```

All four must be green. The test DB will auto-apply the new migration on
first `go test ./internal/db/...` — watch the `goose: successfully migrated`
line in the output to confirm the migration is syntactically valid.

---

## Ship checklist

1. Commit the whole capture as one feat: `feat(<domain>): capture <fields> + <chart-name> (gaka-<id>)`.
   Reference implementation: `6b4724c`.
2. Push to `gakatime`.
3. Cut a patch release: `task release VERSION=v0.5.<next>` then
   `git push --follow-tags`.
4. Watch the pipeline (`gh run list --limit 4`) for the tag build to
   finish; Argo rolls the deployment on the new `:sha-<hash>` image.
5. Trigger a new import (Import page) and confirm:
   - The DriftBanner no longer reports the fields you just absorbed.
   - The new card appears on Overview with real data (assuming the range
     includes rows with the new fields).

---

## Anti-patterns to avoid

- **Adding a `CHECK` constraint on an enum-ish string column.** Wakatime
  adds values; you don't want a migration to accept a new plan name.
- **Making the new columns `NOT NULL`.** Non-AI heartbeats have no value
  to write; every existing row would fail the migration.
- **Storing the raw wakatime json blob on the row.** Denormalizing a
  handful of fields is fine; keeping the whole blob wastes disk and makes
  every query re-parse JSON.
- **Adding the new fields to the rollup.** The rollup (`hb_rollup_daily`)
  is deliberately a 5-axis aggregate for the Overview fast path. Growing
  it invalidates every existing rollup row and demands a full resync.
- **Space-scoping a cross-cutting metric.** AI usage isn't per-project;
  editor-source usage isn't per-space. Don't add `?space=` to the handler
  unless the data itself is inherently scoped.
- **Rendering the new card unconditionally.** Users on non-AI plugins,
  users importing pre-drift ranges, and fresh accounts will see an empty
  card. `data.hasData === false` MUST short-circuit render.
- **Skipping the baseline pass in step 1.** Silencing the tier-2 warnings
  first makes the DriftBanner match the tier-1 work you're about to do —
  otherwise it looks like the reconcile didn't work.

---

## Reference file map (quick lookup)

| Concern | File | Anchor |
|---|---|---|
| Schema whitelist | `internal/importer/drift.go` | `heartbeatSpec.known` |
| Baseline swallow | `internal/importer/drift.go` | `lookupSpec.baseline` |
| Wire decoder | `internal/importer/importer.go` | `type importHeartbeat` |
| Wire → domain | `internal/importer/importer.go` | `convertForDB` |
| Domain model | `internal/model/heartbeat.go` | `type HeartbeatPayload` |
| Insert SQL | `internal/db/queries/insert_heartbeat.sql` | `VALUES ( $1, $2` |
| Insert batch | `internal/db/ingest.go` | `insertHeartbeatsBatch` |
| Aggregation | `internal/db/<theme>_activity.go` | new file |
| Handler | `internal/handler/bigbets.go` | end of file |
| Route | `internal/server/server.go` | `stats/momentum` |
| FE types | `web/src/types/stats.ts` | `StatsPayload` |
| FE api | `web/src/lib/api.ts` | `getMomentum` |
| FE query key | `web/src/lib/queryKeys.ts` | `momentum` |
| FE card | `web/src/features/overview/AIAssistanceCard.tsx` | template |
| FE wiring | `web/src/features/overview/OverviewDashboard.tsx` | `aiActivityQuery` |
| Migration index | `internal/db/migrations/` | pick `000NN` |

---

## When *not* to run this

- The drift finding is `missing_required` or `type_changed` — go read the
  specific finding first; the "add columns + graph" flow doesn't apply.
- The finding is on `all_time_since_today` — that endpoint is a UX helper
  for range detection, not a data pipeline. Just add to
  `allTimeSpec.baseline` and move on.
- The finding count is < 10 and the field name looks like a wakatime
  experimental flag (`_beta_*`, `_debug_*`). Baseline it; wait to see if
  it stabilizes before persisting.
