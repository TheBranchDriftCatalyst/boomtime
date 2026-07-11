# The Query Engine

> How boomtime turns ~440k raw heartbeats into the numbers on a dashboard.

This is a deep dive for a new contributor who needs to touch stats aggregation,
curation (hide/rename), Space scoping, or the response cache. It documents the
**query engine**: the code path from an HTTP request for a dashboard down to the
SQL that runs against Postgres, and back up through the shaping and caching
layers.

For the one-paragraph version see [ARCHITECTURE.md](ARCHITECTURE.md#the-duration--rollup-model-why-its-fast).
This doc is the long version.

Everything here lives in three packages:

| Package | Role |
|---|---|
| `internal/handler` | Thin HTTP handlers: parse request, load curation/scope, call `db.*`, cache the JSON. |
| `internal/db` | The engine proper: embedded `.sql`, the ~11 aggregation functions, the curation/scope splicing layer. |
| `internal/stats` | Shaping: turn flat DB rows into the hakatime-compatible JSON payload, bound the payload size. |

There is **no ORM and no query builder**. Queries are hand-written SQL files
embedded at build time; curation and scope are added by *string-splicing bound-
parameter predicates* into those files at well-known anchor points. Read that
sentence again — it is the whole trick, and the rest of this doc explains why it
is safe and how to extend it.

The remapping is achieved in **two layers** that stack on the embedded `.sql`: an
inner **predicate-splice** layer that filters raw rows (hide + Space scope), and
an outer **rename re-group** layer that relabels and merges groups. This diagram
is the map for the whole doc — everything below is a zoom-in on one of these
boxes:

```mermaid
flowchart LR
    handler["internal/handler<br/>(parse request, load curation/scope,<br/>cache JSON)"]
    db["internal/db<br/>(engine: embedded .sql +<br/>2-layer splice/remap + ingest)"]
    stats["internal/stats<br/>(shape rows into payload,<br/>cap + bucket)"]
    cache["TTL cache<br/>(per-owner JSON blobs)"]

    handler -->|"owner, range, limit, ?space"| db
    db -->|"flat db.StatRow slice"| stats
    stats -->|"payload"| handler
    handler <-->|"wrap: hit skips db+stats"| cache

    subgraph L["internal/db: the query engine"]
        sql["embedded .sql<br/>(windowless conditional SUM)"]
        l1["Layer 1: predicate splice<br/>applyScopes -> injectAfter<br/>(hide + Space, inner WHERE)"]
        l2["Layer 2: rename re-group<br/>regroupStatRows<br/>(outer WITH + CASE remap)"]
        sql --> l1 --> l2
    end
    db -.-> L
```

The ingest side (`RecomputeGaps`, `RefreshRollup`) maintains the precomputed
`gap_seconds` and the daily rollup those queries read; it is not part of the
per-request path. See Section 1.

---

## 1. The duration model: `gap_seconds`

Wakatime-style "time spent" is **not stored**. There is no `duration` column.
Time is derived from the gaps between consecutive heartbeats for a sender.

### 1.1 Computed once, at ingest

Migration `00008_heartbeat_gap_seconds.sql` adds the column and backfills it:

```sql
ALTER TABLE heartbeats ADD COLUMN IF NOT EXISTS gap_seconds INT;
-- Backfill existing rows (NULL for each sender's first heartbeat).
WITH seq AS (
    SELECT id,
        EXTRACT(EPOCH FROM (time_sent - lag(time_sent)
            OVER (PARTITION BY sender ORDER BY time_sent)))::int AS gap
    FROM heartbeats
)
UPDATE heartbeats h SET gap_seconds = seq.gap FROM seq WHERE h.id = seq.id;
```

`gap_seconds` is "seconds to the previous heartbeat for the same sender, in global
time order". It is `NULL` for each sender's very first heartbeat.

On every ingest batch, `SaveHeartbeats` (`internal/db/ingest.go`) recomputes it
incrementally for the affected senders via `RecomputeGaps`, which anchors on the
row **immediately before** the earliest inserted timestamp so out-of-order
inserts fix up the following row too:

```go
// RecomputeGaps ... anchors on the row immediately before `since` so the first
// affected row — and any existing beat that now follows a freshly inserted one —
// is correct.
func (d *DB) RecomputeGaps(ctx context.Context, sender string, since time.Time) error {
	_, err := d.Pool.Exec(ctx, `
WITH anchor AS (
    SELECT COALESCE(max(time_sent), '-infinity'::timestamptz) AS t
    FROM heartbeats WHERE sender = $1 AND time_sent < $2
),
seq AS (
    SELECT h.id, h.time_sent,
        lag(h.time_sent) OVER (ORDER BY h.time_sent) AS prev
    FROM heartbeats h, anchor
    WHERE h.sender = $1 AND h.time_sent >= anchor.t
)
UPDATE heartbeats h
SET gap_seconds = CASE
        WHEN seq.prev IS NULL THEN NULL
        ELSE EXTRACT(EPOCH FROM (seq.time_sent - seq.prev))::int
    END
FROM seq
WHERE h.id = seq.id AND h.time_sent >= $2`, sender, since)
	return err
}
```

### 1.2 "Time spent" = windowless conditional SUM

Because the gap is materialized, computing time for a range is a plain aggregate
— **no per-request `lag()` window, no sort, no session reconstruction**. Every
aggregation shares this shape (here `$4` is `timeLimit` in minutes; a gap larger
than the limit is treated as idle and contributes zero):

```sql
CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8)
    AS total_seconds
```

This is the core of `get_user_activity.sql`, `get_category_daily.sql`,
`get_punchcard.sql`, `get_momentum.sql`, `get_projects_stats.sql`, and (with a
hardcoded 15-minute limit) `get_leaderboards.sql` / `get_time_today.sql`.

`get_sessions.sql` uses the same signal differently: a **new session starts** when
`gap_seconds IS NULL OR gap_seconds > ($4 * 60)` (the "break"), and a session's
duration is the SUM of its non-break gaps.

Since `gap_seconds` is per-sender by construction, `get_leaderboards.sql` gets
cross-user correctness for free — an earlier cross-user `lag()` bug simply cannot
recur (see the comment in that file).

---

## 2. The rollup fast-path: `hb_rollup_daily`

Scanning ~440k raw rows for the Overview on every load is wasteful when the
answer rarely changes. Migration `00009_hb_rollup_daily.sql` adds a coarse daily
rollup:

```sql
CREATE TABLE IF NOT EXISTS hb_rollup_daily (
    sender text NOT NULL,
    day date NOT NULL,
    project text NOT NULL,
    language text NOT NULL,
    editor text NOT NULL,
    platform text NOT NULL,
    machine text NOT NULL,
    total_seconds bigint NOT NULL,
    PRIMARY KEY (sender, day, project, language, editor, platform, machine)
);
```

It stores **exactly five breakdown axes** — `project, language, editor, platform,
machine` — plus `day` and a pre-summed `total_seconds` at the default 15-minute
gap cutoff (`gap_seconds <= 900`). No `branch`, `entity`, `category`, `plugin`.
Low cardinality by design.

It is maintained incrementally: `RefreshRollup` (`internal/db/ingest.go`) is
called after every ingest batch and re-derives the touched days
(`DELETE ... WHERE day >= $2::date`, then re-`INSERT ... SELECT` from raw).

### 2.1 When the fast path is used (and when it falls back)

The decision lives in the `Stats` handler (`internal/handler/stats.go`); `l` is
the lazily loaded curation/scope set (see `dashboardScope.load` in
`internal/handler/scope.go`):

```go
switch {
case s.limit == 15 && !l.spaceRequested && !l.hidden.HasHiddenOutside(db.RollupAxes):
	// Fast path: pre-aggregated rollup (default 15-min limit, no space).
	rows, err = h.DB.GetUserActivityRollup(s.ctx, s.owner, s.t0, s.t1, l.hidden, l.renames, l.members, false)
default:
	// Raw gap_seconds scan (non-default limit, a hide the rollup can't apply,
	// or a space scope).
	rows, err = h.DB.GetUserActivity(s.ctx, s.owner, s.t0, s.t1, s.limit, l.hidden, l.renames, l.members, l.spaceRequested)
}
```

The gate is a three-way AND — **all** must hold to take the rollup. Any one
failing forces the raw scan, because each guards against something the
pre-aggregated table structurally cannot express:

```mermaid
flowchart TD
    A["GetUserActivity request<br/>(owner, range, limit, ?space)"] --> B{"limit == 15 ?"}
    B -- "no (custom timeLimit)" --> R["Raw path:<br/>GetUserActivity<br/>fresh conditional SUM over raw gaps"]
    B -- yes --> C{"!spaceRequested ?"}
    C -- "no (a Space was requested)" --> R
    C -- yes --> D{"!hidden.HasHiddenOutside(RollupAxes) ?"}
    D -- "no (hide on plugin/branch/category)" --> R
    D -- yes --> F["Fast path:<br/>GetUserActivityRollup<br/>reads hb_rollup_daily"]

    B -. "rollup is pre-summed at a fixed<br/>900s cutoff; another limit needs<br/>a different conditional SUM" .-> R
    C -. "a Space rule may target branch/entity/...<br/>axes the rollup lacks" .-> R
    D -. "rollup has no plugin/branch/category<br/>column to exclude on" .-> R
```

Renames never appear in the gate: a rename only relabels output columns (Section
3.3), and the rollup stores exactly the five remappable axes, so re-summing
pre-aggregated rows by the remapped value merges correctly — no fallback needed.

The three fall-back-to-raw conditions:

| Condition | Why the rollup can't serve it |
|---|---|
| `limit != 15` | The rollup is pre-summed at a 900s cutoff; a different `timeLimit` needs a fresh conditional SUM over raw gaps. |
| `spaceRequested` | A Space rule may target an axis the rollup lacks (`branch`/`entity`/…), so scoped requests always use raw. See `HasMemberOutside`. |
| `hidden.HasHiddenOutside(db.RollupAxes)` | A hide on `plugin`/`branch`/`category` can't be applied — the rollup has no such column. |

`RollupAxes` and the gate helpers. `RollupAxes` is not a hand-maintained literal
— it is **derived from the axis registry** in `internal/db/axes.go` (every axis
whose `inRollup` flag is true), so `hb_rollup_daily`, `RollupAxes`, and
`rollupCols` can never drift apart:

```go
// axes.go: the single source of truth. inRollup drives the fast-path gate.
var axes = []axisDef{
	{name: "project", rawCol: "project", inRollup: true},
	{name: "language", rawCol: "language", inRollup: true},
	{name: "editor", rawCol: "editor", inRollup: true},
	{name: "plugin", rawCol: "plugin", inRollup: false},
	{name: "machine", rawCol: "machine", inRollup: true},
	{name: "platform", rawCol: "platform", inRollup: true},
	{name: "branch", rawCol: "branch", inRollup: false},
	{name: "category", rawCol: "category", inRollup: false},
}

// RollupAxes = {axis: true for every axes[i] with inRollup == true}
// => project, language, editor, machine, platform
```

```go
// HasHiddenOutside reports whether any hidden axis is NOT in the provided available set.
func (h HiddenSets) HasHiddenOutside(available map[string]bool) bool {
	for axis, vals := range h.byAxis {
		if len(vals) > 0 && !available[axis] { return true }
	}
	return false
}
```

`MemberSets.HasMemberOutside` is the exact mirror for Space scopes. (It is defined
but note that the current handler gates the rollup on `!spaceRequested` outright —
any Space request already takes raw — so `HasMemberOutside` is the belt-and-braces
check available to future callers that might want a rollup-with-space path.)

**Renames need no rollup fallback.** A rename only *relabels* output columns; it
never removes rows. The rollup's output columns are exactly the five remappable
axes, so re-summing pre-aggregated rows by the remapped value merges correctly.

---

## 3. The curation + scoping layer

This is the heart of the engine. Three families of per-user rules are applied
**at query time only** — raw heartbeats, projects, and the rollup are never
mutated, so every rule is instantly reversible and audit surfaces (the Explorer)
keep showing raw values.

| Rule family | Table | Sets type | Effect on SQL |
|---|---|---|---|
| **Hide** | `curation_rules` (action=`hide`) | `HiddenSets` | `AND NOT (col = ANY($n))` — drop rows |
| **Rename / merge** | `curation_rules` (action=`rename`) | `RenameSets` | outer re-group, `GROUP BY CASE`-remapped display value |
| **Space scope** | `spaces` / `space_rules` | `MemberSets` | `AND ( arm OR arm … )` — keep only matching rows |

The three families are defined across `internal/db/predicates.go` (hide:
`HiddenSets`, `exclusionPredicate`), `internal/db/remap.go` (rename: `RenameSets`,
`remapExpr`, `regroupStatRows`), and `internal/db/spaces.go` (scope: `MemberSets`,
`inclusionPredicate`, `spaceScopePredicate`). Rule-CRUD and template validation
live in `internal/db/curation.go`.

Each family loads from a different table, is built by a different helper, and
lands in a different part of the SQL — but only Hide and Space are *predicates*
(inner WHERE); Rename is a *re-group* (outer wrap):

```mermaid
flowchart TD
    subgraph SRC["Rule sources (per-user)"]
        cr["curation_rules<br/>(action=hide / rename)"]
        sp["spaces + space_rules"]
    end

    cr -->|"LoadHiddenSets<br/>action=hide"| HS["HiddenSets"]
    cr -->|"LoadRenameSets<br/>action=rename"| RS["RenameSets"]
    sp -->|"LoadMemberSets"| MS["MemberSets"]

    HS -->|"exclusionPredicate<br/>(predicates.go)"| HP["AND NOT (col = ANY($n))"]
    MS -->|"inclusionPredicate /<br/>spaceScopePredicate (spaces.go)"| MP["AND (col = ANY($n) OR col ~ $m ...)<br/>or AND FALSE (empty space)"]
    RS -->|"remapExpr + regroupStatRows<br/>(remap.go)"| RP["outer WITH regrouped AS<br/>(SELECT CASE-remap, SUM ... GROUP BY CASE-remap)"]

    HP --> INNER["Layer 1: inner WHERE<br/>(filter RAW rows before aggregation)"]
    MP --> INNER
    RP --> OUTER["Layer 2: outer wrap<br/>(relabel + merge groups, recompute % windows)"]
```

The split is load-bearing: Hide/Space *drop or keep raw rows* and so must run
**inside** the aggregation's WHERE; Rename *relabels and merges the aggregated
groups* and so must **wrap** the whole thing. Section 4 shows the code that
enforces this ordering.

### 3.1 Match types

A rule's `match_type` (curation) or a Space rule's is one of:

- **`exact`** — `MatchValue` is a literal; SQL uses `col = ANY($arr)`.
- **`regex`** — `MatchValue` is a Postgres regex; SQL uses `col ~ $pattern`.
- **`template`** (rename only) — `MatchValue` is a regex **and** `NewValue` is a
  `regexp_replace` template with capture-group backrefs. This is a *transform*,
  not a fixed target: `col ~ $pat THEN regexp_replace(col, $pat, $tmpl)`.

Template inputs are normalized and validated:

- `NormalizeTemplate` rewrites shell-style `$1` → Postgres `\1` (and `$$` → literal `$`).
- `ValidateRegex` compiles the pattern via `SELECT ''::text ~ $1` (no row scan).
- `ValidateTemplate` additionally counts the pattern's capture groups (using a
  self-matching `(?:(?:PATTERN)|)()` probe, real count = reported − 1) and rejects
  any backref `\N` that exceeds the group count — Postgres itself only raises
  "invalid reference number" when the pattern *matches*, so a naive empty-string
  probe would silently miss a bad `\9`.

### 3.2 The predicate builders

**Exclusion (hide)** — `exclusionPredicate` walks the hide axes in a deterministic
order and appends one `AND NOT (…)` per hidden axis that has a column in `cols`:

```go
func exclusionPredicate(hs HiddenSets, cols map[string]string, scopeCond string, nextArg int, args []any) (string, []any, int) {
	var sql string
	for _, axis := range hiddenAxes { // deterministic order
		vals := hs.byAxis[axis]
		col := cols[axis]
		if len(vals) == 0 || col == "" {
			continue // axis has no hide, or this query's table lacks the column
		}
		if scopeCond != "" { // leaderboards: `sender = $req` scopes the hide to the requester
			sql += fmt.Sprintf(" AND NOT (%s AND %s = ANY($%d))", scopeCond, col, nextArg)
		} else {
			sql += fmt.Sprintf(" AND NOT (%s = ANY($%d))", col, nextArg)
		}
		args = append(args, vals)
		nextArg++
	}
	return sql, args, nextArg
}
```

The `scopeCond` argument (empty for the normal single-user path) is what lets
`GetLeaderboards` scope a hide to the requester's own rows — see Section 4.

**Inclusion (Space)** — `inclusionPredicate` is the *union mirror*: one `AND ( …
OR … )` where exact values become one `col = ANY($n)` arm and each regex becomes
a `col ~ $n` arm:

```go
sql := " AND ("
for i, arm := range arms {
	if i > 0 { sql += " OR " }
	sql += arm
}
sql += ")"
```

Space scope has a crucial edge case handled by `spaceScopePredicate`: a *rule-less
or column-less* Space must match **nothing**, not everything, so it emits
` AND FALSE`:

```go
func spaceScopePredicate(ms MemberSets, cols map[string]string, passCond string, nextArg int, args []any, spaceRequested bool) (string, []any, int) {
	if !spaceRequested { return "", args, nextArg }        // unscoped: no predicate
	var pred string
	if ms.AnyMember() {
		pred, args, nextArg = inclusionPredicate(ms, cols, passCond, nextArg, args)
	}
	if pred == "" { // no rules, or none map onto this table's cols => scope includes nothing
		if passCond != "" { return " AND (" + passCond + " OR FALSE)", args, nextArg }
		return " AND FALSE", args, nextArg
	}
	return pred, args, nextArg
}
```

Like `exclusionPredicate`, it grew a bypass argument (`passCond`, empty for the
single-user path). `GetLeaderboards` passes `sender <> $req` so other users' rows
survive even when the requester's scope matches nothing.

### 3.3 The rename remap: `remapExpr` + `regroup…`

A rename can't just filter — it re-labels and merges, so the pct/daily_pct windows
must be recomputed over the *merged* groups. This is a two-step:

1. `remapExpr(axis, col, extraCond, nextArg, args)` builds a `CASE` expression
   mapping a raw column to its display value. WHEN order is **exact → regex →
   template** (first match wins), all values bound as params:

   ```
   CASE WHEN col = ANY($arr) THEN $t
        [WHEN col ~ $pat THEN $t2 ...]
        [WHEN col ~ $pat THEN regexp_replace(col, $pat, $tmpl) ...]
   ELSE col END
   ```

   The optional `extraCond` is ANDed into every WHEN — leaderboards use it
   (`sender = $req`) so a user's rename only relabels *their own* rows.

2. `regroupStatRows` / `regroupProjectStatRows` wrap the inner query in an outer
   `WITH regrouped AS ( SELECT <remapped cols>, SUM(total_seconds) … GROUP BY
   <remapped cols> )` and recompute the two percentage windows. `regroupStatRows`
   remaps six axes (`project, language, editor, branch, platform, machine`);
   `regroupProjectStatRows` remaps only `language` (the project-detail query is
   already project-scoped). Both are **no-ops when no rename applies**.

### 3.4 The `cols` maps: axis → SQL column

The predicate builders are injection-safe precisely because the axis→column
mapping never comes from user input — it comes from a whitelist map **derived from
the `axes.go` registry**, and every value is a bound `$n` parameter. Which map you
pass depends on the table the query scans:

| Map | Used by | Notes |
|---|---|---|
| `rawHeartbeatCols` | every query whose innermost scan is `heartbeats` | all 8 hide axes available, unqualified columns |
| `rollupCols` | `GetUserActivityRollup` | only the 5 rollup axes |
| `projectListCols` | `GetAllProjects` | qualified `heartbeats.project`, … (a JOIN) |

If an axis isn't in the map you pass, its predicate is silently skipped — that is
how the rollup path drops a `branch` hide it can't express, and why the handler
must gate the fast path with `HasHiddenOutside` so it never *silently* ignores a
hide it was asked to apply.

### 3.5 The splice mechanism: `injectAfter` + range anchors

Predicates are inserted into the embedded SQL immediately after a **range-anchor**
constant — the query's range-end clause — using a literal string search:

```go
// injectAfter splices addition into query immediately after the first occurrence
// of anchor. If anchor is absent it returns query unchanged (so a drifted .sql is
// caught by the exclusion tests rather than producing broken SQL).
func injectAfter(query, anchor, addition string) string {
	if addition == "" { return query }
	idx := strings.Index(query, anchor)
	if idx < 0 { return query }
	pos := idx + len(anchor)
	return query[:pos] + addition + query[pos:]
}
```

Each aggregation defines its anchor as a Go constant that must appear verbatim in
its `.sql`:

| Anchor constant | `.sql` clause |
|---|---|
| `activityRangeAnchor` | `AND time_sent <= $3` |
| `rollupRangeAnchor` | `AND day <= $3::date` |
| `projectStatsRangeAnchor` | `AND time_sent <= $4` |
| `timelineRangeAnchor` | `AND time_sent < $3` |
| `leaderboardsRangeAnchor` | `AND time_sent <= $2` |
| `bigBetRangeAnchor` (category/punchcard/sessions/momentum) | `AND time_sent <= $3` |
| `timeTodayRangeAnchor` | `time_sent < (current_date + interval '1' day)` |

Keeping the anchor as a constant is a **safety valve**: if someone edits the
`.sql` and removes the anchor line, `injectAfter` returns the query unchanged, the
hide/scope predicate silently vanishes — and the curation integration tests catch
it (they assert hidden rows disappear).

---

## 4. The shared threading pattern

Almost every aggregation function follows the same recipe. Read it once and you
can read all eleven. Using `GetUserActivity` (`internal/db/activity.go`) as the
reference:

```go
func (d *DB) GetUserActivity(ctx, user, start, end, limit, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]StatRow, error) {
	// 1+2+3. Load the embedded .sql and splice BOTH row filters (hide exclusion +
	// Space inclusion) after the range anchor, in one call.
	query, args, next := applyScopes(qGetUserActivity, activityRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{user, start, end, limit}, 5)
	// 4. Wrap in the rename re-group (merges A,B -> M, recomputes % windows).
	query, args = rs.regroupStatRows(query, next, args)

	var out []StatRow                    // 5. run under elevated work_mem, scan rows
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanStatRows(rows); return
	})
	return out, err
}
```

The hide + space splice is now consolidated into a single `applyScopes` helper
(`internal/db/splice.go`) — beats 2 and 3 no longer appear as two separate
`injectAfter` calls at the call site. `applyScopes` performs both splices after
the same anchor internally:

```go
func applyScopes(query, anchor string, hs HiddenSets, ms MemberSets, spaceRequested bool,
	cols map[string]string, args []any, next int) (string, []any, int) {
	if hs.AnyHidden() {              // splice the hide exclusion after the anchor
		var pred string
		pred, args, next = exclusionPredicate(hs, cols, "", next, args)
		query = injectAfter(query, anchor, pred)
	}
	if spaceRequested {             // splice the space inclusion after the SAME anchor
		var pred string
		pred, args, next = spaceScopePredicate(ms, cols, "", next, args, spaceRequested)
		query = injectAfter(query, anchor, pred)
	}
	return query, args, next
}
```

The five beats, in order every time:

1. **Load the embedded `.sql`** (`qGet…`, embedded in `db.go` via `//go:embed`).
2. **Splice the hide predicate** after the range anchor (drop rows by raw value
   *before* aggregation) — inside `applyScopes`.
3. **Splice the space predicate** after the same anchor (keep only in-scope rows)
   — also inside `applyScopes`.
4. **Wrap in the rename re-group** (merge + relabel + recompute % windows).
5. **Execute via `aggQuery`** — a read-only transaction that first runs
   `SET LOCAL work_mem = '256MB'` (tunable via `BOOM_STATS_WORK_MEM`) so the big
   sorts stay in RAM; the `SET LOCAL` is discarded by the read-only rollback, so
   it never leaks to another pooled connection.

Beats 1-3 are **Layer 1** (predicate splice into the inner WHERE); beat 4 is
**Layer 2** (the rename re-group wrap). This is the two-layer construction that
achieves all the remapping:

```mermaid
flowchart TD
    S["qGetUserActivity<br/>(embedded .sql: inner scan +<br/>windowless conditional SUM)"]
    A["args = [user, start, end, limit]<br/>next = 5"]

    subgraph L1["Layer 1 — applyScopes: predicate splice (mutates INNER WHERE)"]
        direction TB
        H["exclusionPredicate(hs, cols, ...)<br/>=> AND NOT (col = ANY($n))"]
        SP["spaceScopePredicate(ms, cols, ...)<br/>=> AND (col = ANY($n) OR col ~ $m)<br/>or AND FALSE"]
        IA["injectAfter(query, activityRangeAnchor, pred)<br/>both land right after AND time_sent <= $3"]
        H --> IA
        SP --> IA
    end

    subgraph L2["Layer 2 — regroupStatRows: rename re-group (WRAPS the whole query)"]
        direction TB
        RG["WITH regrouped AS (<br/>SELECT CASE-remapped cols, SUM(total_seconds)<br/>FROM ( &lt;spliced inner query&gt; ) base<br/>GROUP BY CASE-remapped cols )<br/>+ recompute pct / daily_pct windows"]
    end

    S --> L1
    A --> L1
    L1 -->|"spliced inner SQL + grown args"| L2
    L2 -->|"final SQL string + args"| Q["aggQuery<br/>(SET LOCAL work_mem, read-only tx, scan)"]

    L1 -. "ordering is mandatory:<br/>filter RAW values FIRST" .-> L2
```

**Ordering matters.** Hide and scope operate on *raw* values in the inner WHERE,
so they must be spliced before the rename re-group wraps the query — otherwise you
would be filtering on already-relabeled values. The code enforces this by
construction: `applyScopes` (splice) runs first, `regroupStatRows` (wrap) runs
last.

Seen as a call-order story, the five beats are:

```mermaid
sequenceDiagram
    participant Fn as GetUserActivity
    participant AS as applyScopes
    participant IA as injectAfter
    participant RG as regroupStatRows
    participant AQ as aggQuery
    participant PG as Postgres

    Note over Fn: query = qGetUserActivity (embedded .sql)<br/>args = [user,start,end,limit], next=5
    Fn->>AS: (query, anchor, hs, ms, spaceRequested, cols, args, next)
    AS->>IA: exclusionPredicate -> AND NOT (col = ANY($n))
    IA-->>AS: inner WHERE mutated (hide)
    AS->>IA: spaceScopePredicate -> AND (...) / AND FALSE
    IA-->>AS: inner WHERE mutated (scope)
    AS-->>Fn: spliced query, grown args, next
    Fn->>RG: regroupStatRows(query, next, args)
    RG-->>Fn: outer WITH-wrapped query (relabel + merge + % windows)
    Fn->>AQ: aggQuery(final SQL, args, scan)
    AQ->>PG: SET LOCAL work_mem, run in read-only tx
    PG-->>AQ: rows
    AQ-->>Fn: []StatRow
```

Two functions deviate:

- **`GetLeaderboards`** is multi-user. Hide/rename/scope must apply to the
  **requester's own rows only** (one user's curation must not alter another user's
  leaderboard contribution). It reuses a single `$req` param, guards hide with
  `AND NOT (sender = $req AND col = ANY(...))`, scopes with
  `AND (sender <> $req OR <requester's inclusion>)`, and passes `extraCond =
  "sender = $req"` into `remapExpr`.
- **`GetActiveFiles`** (`internal/db/active_files.go`) builds its SQL inline (not
  from a `.sql` file) because it needs the rename remap applied *per raw row* (via
  `remapExpr` inside a `WITH per_row` CTE) before a `COUNT(DISTINCT
  remapped-project)`. It still uses the shared `applyScopes` for the hide + space
  splice — passing the inline query and its own `"AND time_sent <= $3"` anchor —
  so only the rename half is special-cased.

### 4.1 The aggregation functions

| Function | `.sql` file | `cols` map | Output row type |
|---|---|---|---|
| `GetUserActivity` | `get_user_activity.sql` | `rawHeartbeatCols` | `[]StatRow` |
| `GetUserActivityRollup` | `get_user_activity_rollup.sql` | `rollupCols` | `[]StatRow` |
| `GetProjectStats` | `get_projects_stats.sql` | `rawHeartbeatCols` | `[]ProjectStatRow` |
| `GetAllProjects` | *(inline SQL)* | `projectListCols` | `[]string` |
| `GetLeaderboards` | `get_leaderboards.sql` | `rawHeartbeatCols` (requester-scoped) | `[]LeaderboardRow` |
| `GetTimeline` | `get_timeline.sql` | `rawHeartbeatCols` (scope only) | `[]TimelineRow` |
| `GetCategoryDaily` | `get_category_daily.sql` | `rawHeartbeatCols` | `[]CategoryDailyRow` |
| `GetPunchcard` | `get_punchcard.sql` | `rawHeartbeatCols` (hide+scope; no rename) | `[]PunchcardCell` |
| `GetSessions` | `get_sessions.sql` | `rawHeartbeatCols` (hide+scope; no rename) | `[]SessionRow` |
| `GetMomentum` | `get_momentum.sql` | `rawHeartbeatCols` | `[]MomentumRow` |
| `GetActiveFiles` | *(inline SQL)* | `rawHeartbeatCols` | `[]ActiveFile` |

`GetTimeline`, `GetPunchcard`, `GetSessions` take no `RenameSets` — their output
columns (lang/project spans, dow×hour, session day) carry no renamable *breakdown*
axis, so a rename would have no output column to relabel. (The category/punchcard/
sessions/momentum functions in `bigbets.go` all splice hide + scope through the
shared `applyScopes` helper, using the common `bigBetRangeAnchor`.)

Row types live in `internal/db/rows.go` (`StatRow`, `ProjectStatRow`,
`TimelineRow`, `LeaderboardRow`) and next to their functions
(`CategoryDailyRow`, `PunchcardCell`, `SessionRow`, `MomentumRow` in `bigbets.go`;
`ActiveFile` in `active_files.go`).

---

## 5. Request → SQL flow

```mermaid
flowchart TD
    A[GET /api/v1/users/current/stats?start&end&timeLimit&space] --> B[Stats handler]
    B --> C{cacheKey hit?}
    C -- yes --> Z[return cached JSON blob]
    C -- no --> D["s.load: LoadHiddenSets + LoadRenameSets + loadSpace"]
    D --> E{"limit==15 AND no space AND no hide-outside-rollup?"}
    E -- yes --> F[GetUserActivityRollup<br/>reads hb_rollup_daily]
    E -- no --> G[GetUserActivity<br/>raw gap_seconds scan]
    F --> H[splice exclusion + scope after range anchor<br/>wrap in rename re-group]
    G --> H
    H --> I[aggQuery: SET LOCAL work_mem, run in read-only tx]
    I --> J[scanStatRows -> flat db.StatRow slice]
    J --> K[GetCategoryDaily same threading]
    K --> L[stats.ToStatsPayload: segment, capWithOther, bucket]
    L --> M[json.Marshal -> Cache.Set key -> return blob]
```

Concretely for `GET /stats` (`internal/handler/stats.go`). The per-request
plumbing (owner, range, limit, `?space`) is bundled into a `dashboardScope`
(`internal/handler/scope.go`); the expensive curation/space lookups run lazily via
`s.load(...)` **inside** the `cachedJSON` closure so a cache hit skips them:

```go
func (h *Handler) Stats(c *echo.Context) error {
	s, aerr := h.dashboardScope(c, 7)               // owner + range(7d) + limit + ?space
	if aerr != nil { return respondErr(c, aerr) }
	return h.cachedJSON(c, s.cacheKey("stats", s.t0, s.t1, s.limit), func() (any, error) {
		l, err := s.load(loadHidden | loadRenames)  // HiddenSets + RenameSets + space scope
		if err != nil { return nil, err }
		// ... rollup-vs-raw switch (section 2.1) sets `rows` ...
		categories, err := h.DB.GetCategoryDaily(s.ctx, s.owner, s.t0, s.t1, s.limit,
			l.hidden, l.renames, l.members, l.spaceRequested)
		if err != nil { return nil, err }
		return stats.ToStatsPayload(s.t0, s.t1, rows, categories), nil
	})
}
```

`s.cacheKey` always appends the `"space:<param>"` component, so the range,
`timeLimit`, and `?space` all enter the key (Section 8).

---

## 6. Worked example: a Space `project ~ ^catalyst` inclusion

Suppose a user has a Space with one rule `{axis: project, matchType: regex,
matchValue: ^catalyst}` and requests `GET /stats?space=7`.

`get_user_activity.sql`, inner CTE, **before** splicing:

```sql
    FROM
        heartbeats
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3        -- <-- activityRangeAnchor
    GROUP BY
        ...
```

`applyScopes` runs with `next = 5`. There's no hide, so it skips
`exclusionPredicate`; `spaceRequested` is true, so
`spaceScopePredicate(ms, rawHeartbeatCols, "", 5, args, true)` returns
` AND (project ~ $5)` and appends `^catalyst` as `args[4]`. `injectAfter` splices
it right after the anchor. **After**:

```sql
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3 AND (project ~ $5)   -- spliced inclusion
    GROUP BY
        ...
```

`args` is now `[user, start, end, 15, "^catalyst"]`. Only heartbeats whose raw
project matches `^catalyst` survive to the aggregate.

Now suppose the same user *also* hid project `catalyst-legacy`. `applyScopes`
builds the hide predicate first (so it claims the lower param `$5`) and splices it,
then builds the space predicate (`$6`) and splices it. Because both use
`injectAfter` on the **same anchor**, each new fragment is inserted immediately
after the anchor literal — so the second splice (space) lands *ahead of* the first
(hide) in the final text, even though its `$n` is higher:

```mermaid
flowchart TB
    A0["anchor line:<br/>AND time_sent &lt;= $3"]

    subgraph B1["applyScopes step 1 — hide (builds $5, splices first)"]
        H["exclusionPredicate(hs) -> AND NOT (project = ANY($5))<br/>injectAfter -> AND time_sent &lt;= $3 AND NOT (project = ANY($5))"]
    end

    subgraph B2["applyScopes step 2 — space (builds $6, splices after SAME anchor)"]
        S["spaceScopePredicate(ms) -> AND (project ~ $6)<br/>injectAfter inserts right after the anchor,<br/>ahead of the hide fragment"]
    end

    R["final inner WHERE:<br/>AND time_sent &lt;= $3 AND (project ~ $6) AND NOT (project = ANY($5))"]

    A0 --> B1 --> B2 --> R
```

The param numbers follow *build* order (hide `$5`, space `$6`); the textual order
follows *splice* order (space ahead of hide). Either way, hide and scope compose
as a plain AND of two independent predicates, so the reordering is harmless.

Contrast with a **rule-less** Space (`space=7` but zero rules):
`spaceScopePredicate` returns ` AND FALSE`, so the dashboard renders empty — the
deliberate "empty scope shows nothing, not everything" semantic.

---

## 7. Payload bounding

Two mechanisms keep the JSON small and the browser responsive on "All time" over
~440k rows:

**Top-N + "Other (N more)"** — `capWithOther` (`internal/stats/stats.go`) keeps
the `resourceTopN = 12` biggest resources per dimension and collapses the rest
into one aggregated bucket whose totals and per-day arrays are the element-wise
sums of the tail:

```go
const resourceTopN = 12

func capWithOther(list []model.ResourceStats) []model.ResourceStats {
	if len(list) <= resourceTopN { return list }
	sort.SliceStable(list, func(a, b int) bool { return list[a].TotalSeconds > list[b].TotalSeconds })
	top, tail := list[:resourceTopN], list[resourceTopN:]
	other := model.ResourceStats{Name: fmt.Sprintf("Other (%d more)", len(tail))}
	// ... element-wise sum tail into other ...
	return append(top, other)
}
```

Applied to projects, editors, languages, platforms, machines, categories, files,
branches. `ToMomentumPayload` does its own top-N (default 8) project ranking.

**~Weekly bucketing** — long daily time-series are downsampled to
`MAX_CHART_POINTS = 62` on the frontend (`web/src/viz/bucket.ts`). `bucketGroups`
is identity when `dayCount <= 62`, else it groups days into `ceil(dayCount/62)`-
sized contiguous buckets; `bucketSum`/`bucketAvg`/`bucketMax` reduce each bucket
per the metric's semantics:

```ts
export const MAX_CHART_POINTS = 62;
export function bucketGroups(dayCount: number): number[][] {
  if (dayCount <= MAX_CHART_POINTS) return Array.from({ length: dayCount }, (_, i) => [i]);
  const size = Math.ceil(dayCount / MAX_CHART_POINTS);
  // ... contiguous groups of `size` days ...
}
```

The contribution calendar is the intentional exception — it needs raw daily cells
(a year is ~465 cells, cheap).

---

## 8. The response cache

Aggregations are cached as marshaled JSON blobs in a tiny per-process TTL cache
(`internal/cache/ttl.go`), wired in the handler.

**Key** — `cacheKey(owner, name, parts...)` builds `"owner|name|part|part…"`.
`time.Time` parts are truncated to a 30s bucket (`cacheKeyTimeBucket`) before
rendering as Unix seconds — without it, default-range requests (whose end is
`time.Now()`) would mint a fresh key every second and never hit the cache:

```go
func cacheKey(owner, name string, parts ...any) string {
	// "owner|name|<t0 unix, 30s-bucketed>|<t1 unix, 30s-bucketed>|<limit>|space:<id>"
	// time.Time parts: t.Truncate(cacheKeyTimeBucket).Unix()
}
```

The range, `timeLimit`, and `?space` **all enter the key**, so a scoped or
non-default-limit request never collides with the unscoped default. Handlers build
it via the scope helper `s.cacheKey`, which always appends the space component:

```go
// dashboardScope.cacheKey (internal/handler/scope.go):
s.cacheKey("stats", s.t0, s.t1, s.limit)
// => cacheKey(owner, "stats", t0, t1, limit, "space:"+spaceParam)
```

**TTL** — `statsCacheTTL()` defaults to 30s (tunable via `BOOM_STATS_CACHE_TTL`
seconds; `0` disables). `cachedJSON` serves the cached blob on a hit, else computes
+ marshals + `Cache.Set`s.

**Invalidation** — the key is prefixed with `owner + "|"`, so changing a user's
curation or spaces drops *all* their cached dashboards at once:

```go
func (h *Handler) invalidateOwnerCache(owner string) {
	if h.Cache != nil {
		h.Cache.InvalidatePrefix(owner + "|")
	}
}
```

`invalidateOwnerCache` is called from every curation and space mutation handler
(`internal/handler/curation.go`, `internal/handler/spaces.go`). A change takes
effect immediately for that owner; other owners' entries survive (`InvalidatePrefix`
only drops matching keys). Beyond explicit busts, the 30s TTL bounds staleness for
everything else (e.g. a fresh ingest).

---

## 9. Extension guide

### 9.1 Add a new scoped aggregation

Say you want a new "top dependencies" dashboard endpoint.

1. **Write the `.sql`** in `internal/db/queries/`. Follow the shape of an
   existing file: an inner scan of `heartbeats` (or the rollup) that produces the
   windowless conditional SUM, with a range-end clause that will be your anchor.
   Make the range-end clause a **stable, unique** literal.
2. **Embed it** in `internal/db/db.go`: add a `qGetTopDeps = mustQuery("get_top_deps.sql")`
   line to the preloaded `var (...)` block. (The `//go:embed queries/*.sql`
   directive already includes any new file.)
3. **Add the anchor constant** next to your function:
   `const topDepsRangeAnchor = "AND time_sent <= $3"` (must match your `.sql` byte
   for byte).
4. **Write the row type** (in `rows.go` or beside the function) and a
   `scan…` helper.
5. **Write the function** following the section-4 threading recipe: build `args`,
   splice the hide + space predicates after your anchor with one
   `applyScopes(query, yourAnchor, hs, ms, spaceRequested, rawHeartbeatCols, args, next)`
   call, then — if any output column carries a renamable axis — write a
   `regroup…`-style wrapper (or reuse `regroupStatRows` if your columns match).
   Run it under `d.aggQuery`.
6. **Add the handler** (`internal/handler/…`): call `h.dashboardScope(c, days)`,
   then inside a `cachedJSON` closure keyed with `s.cacheKey(...)`, call
   `s.load(loadHidden | loadRenames)` to get the `HiddenSets` / `RenameSets` /
   space scope. `s.cacheKey` already includes range, limit, and `"space:"+param`.
7. **Add a curation/scope integration test** that asserts a hidden value
   disappears and a Space scope filters — this is what catches an anchor typo.

### 9.2 Add a new curation / scope axis

Say heartbeats gain a `repo` column you want to hide/rename/scope on.

1. **Add one `axisDef` row** to the `axes` registry in `internal/db/axes.go`:
   `{name: "repo", rawCol: "repo", inRollup: false}`. That single edit derives
   `hiddenAxes`, `rawHeartbeatCols`, `projectListCols` (as `heartbeats.repo`),
   `RollupAxes`, and `rollupCols` — so `exclusionPredicate`/`inclusionPredicate`
   iterate the new axis automatically. The derivations are pinned by
   `TestAxisRegistryDerivations`.
2. **Make the axis pass `ExploreColumn`'s whitelist** so Space-rule and
   curation-rule creation accept it (`AddSpaceRule` and rule creation validate
   the axis against `ExploreColumn`).
3. **If it should be renamable/mergeable**, add `{"repo", "repo"}` to
   `statRowRemapAxes` (`remap.go`) and extend the relevant `regroup…` wrapper's
   SELECT/GROUP BY so the new axis is remapped and re-summed.
4. **Rollup?** Set `inRollup: true` on the `axisDef` **and** add the column to
   `hb_rollup_daily` (new migration) if you want `repo` hides to survive the fast
   path. Otherwise leave `inRollup: false` — `HasHiddenOutside` will correctly
   force the raw path whenever a `repo` hide is active.
6. **Tests**: extend the axis-coverage suites
   (`regex_all_aggregations_test.go`, `rename_merge_test.go`,
   `suppression_test.go`, `spaces_test.go`).

---

## 10. Gotchas

- **The rollup only stores 5 axes.** `hb_rollup_daily` has no `branch`, `entity`,
  `category`, or `plugin`. A hide/scope on those *must* force the raw path
  (`HasHiddenOutside` / `!spaceRequested`). If you add a rollup consumer, gate it
  the same way or you will silently ignore a hide the user asked for.

- **Template rename is transform, not match.** An `exact`/`regex` rename maps to a
  *fixed* `NewValue`; a `template` rename maps to `regexp_replace(col, $pat,
  $tmpl)` — the output is computed per row. `ValidateTemplate` guards bad
  backrefs, but remember `NormalizeTemplate` (`$1`→`\1`) runs first, so store the
  normalized form.

- **Anchor drift silently drops predicates.** `injectAfter` returns the query
  *unchanged* if the anchor literal isn't found. Edit a `.sql`'s range-end clause
  and you'll silently disable that query's hide/scope — only the integration tests
  catch it. Keep the anchor constant and the `.sql` clause byte-identical.

- **Cache invalidation only fires from the curation/space handlers.** Anything
  that mutates heartbeats or curation via *direct SQL* (a manual `UPDATE`, a
  migration, an admin script) bypasses `invalidateOwnerCache`, so cached
  dashboards can serve stale data for up to the TTL (30s default). The ingest path
  is not explicitly invalidated either — it relies on the TTL.

- **Never run these aggregations against a write connection with EXPLAIN
  ANALYZE.** `aggQuery` runs in a read-only transaction that it *rolls back*
  (that's how `SET LOCAL work_mem` is discarded). If you copy a query out to
  benchmark it, `EXPLAIN ANALYZE` **executes** it — harmless for these SELECTs,
  but the same care applies to the ingest-side `RecomputeGaps`/`RefreshRollup`
  `UPDATE`/`DELETE`+`INSERT`: `EXPLAIN ANALYZE` on those *performs the write*. Use
  plain `EXPLAIN` (no ANALYZE) on any write, or wrap in a `ROLLBACK`.

- **Percentages are recomputed on merge.** `pct`/`daily_pct` are `numeric(13,12)`
  window fractions. Any rename re-group *must* recompute them over the merged
  groups (the `regroup…` wrappers do). If you write a new merge path, don't carry
  the inner percentages up — re-derive them.

---

## Related code

- `internal/db/axes.go` — the axis registry (`axes`, `axisDef`); derives
  `hiddenAxes`, `rawHeartbeatCols`, `rollupCols`, `RollupAxes`, `projectListCols`.
- `internal/db/ingest.go` — ingest + derived data: `SaveHeartbeats`,
  `RecomputeGaps`, `RefreshRollup`, `ResyncDerived`, `GetDerivedStatus`.
- `internal/db/splice.go` — shared plumbing: `injectAfter`, `applyScopes`,
  `aggQuery`, `scanStatRows`, `trimSQL`.
- `internal/db/predicates.go` — hide exclusion: `HiddenSets`,
  `exclusionPredicate`, `LoadHiddenSets`, `HasHiddenOutside`.
- `internal/db/remap.go` — rename remap: `RenameSets`, `remapExpr`,
  `regroupStatRows`, `regroupProjectStatRows`, `LoadRenameSets`,
  `statRowRemapAxes`.
- `internal/db/curation.go` — curation-rule CRUD + template validation
  (`NormalizeTemplate`, `ValidateRegex`, `ValidateTemplate`,
  `CurationAffectedValues`).
- `internal/db/spaces.go` — `MemberSets`, `inclusionPredicate`,
  `spaceScopePredicate`, `HasMemberOutside`, space/rule CRUD.
- `internal/db/activity.go` — `GetUserActivity`, `GetUserActivityRollup`,
  `GetProjectStats`, `GetTimeline`, `GetTotalTimeToday` + their range anchors.
- `internal/db/leaderboards.go` — `GetLeaderboards` (requester-scoped
  hide/rename/space).
- `internal/db/projects.go` — `GetAllProjects` (inline SQL), project ownership,
  badges.
- `internal/db/bigbets.go`, `active_files.go` — category/punchcard/sessions/
  momentum + cross-project active files.
- `internal/db/queries/*.sql` — the embedded SQL.
- `internal/db/migrations/00008*`, `00009*` — the gap column + rollup table.
- `internal/handler/stats.go`, `scope.go`, `handler.go` — rollup-vs-raw decision,
  `dashboardScope`/`load`, `cacheKey`, `cachedJSON`.
- `internal/stats/stats.go`, `bigbets.go` — shaping + `capWithOther`.
- `web/src/viz/bucket.ts` — frontend ~weekly bucketing.
