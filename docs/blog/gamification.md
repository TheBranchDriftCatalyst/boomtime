# Turning wakatime stats into a game

Boomtime records what you type, where you type it, and when. That's the
raw data — hours in Python, an editor swap at 2 AM, a whole Saturday
where the only file touched was `configure`. The **gamification layer**
turns that stream into a legible profile: **labels** you earn, **tiers**
you climb, **patches** you pin, and **streaks** that reward showing up.

This post is the map: what the system does, how the pieces fit,
where the extension points are, and where a language model could add
value without breaking the shape.

> **Updated 2026-07-29 (gaka-hc6).** The evaluator moved server-side.
> The DSL is unchanged (all 13 primitives + 3 composers) — same tables,
> same JSON shapes on disk — but the runtime moved from a browser
> TypeScript switch to a Go switch behind two endpoints. Layer 2 and
> the request-flow diagram reflect the new shape; see also
> `docs/design/labels-server-eval.md` for the migration write-up.

## What you see

Open `/p/<slug>` on any public profile. Under the hero you'll see a row
of chips:

```
★ NIGHT WATCH   ⚔ TERMINAL PURIST   ⚡ PYTHON · MASTER   🌊 SPRINTER
    3x                                  ↑ tier badge          "heating up"
```

Each chip is a **label award** — a passing verdict from a small
declarative predicate against your public dashboard payload. The
number in the corner (`3x`) is a **streak** — three consecutive periods
where you kept the label. Hover a chip and you get:

- A one-line description ("Sprinter: your last 7 days average is 2×
  the prior 7 days")
- A **condition explainer** ("Fires when: last-7 vs prior-7 ratio ≥ 2")
- The streak length, if any

That's the whole user-facing surface — a row of chips with tooltips.
Everything below is the machinery that makes those chips honest.

## The four layers

```
┌────────────────────────────────────────────────────────────┐
│  Catalog (DB)     labels table + optimizedPrompt for       │
│                   ComfyUI-rendered emblem images           │
├────────────────────────────────────────────────────────────┤
│  Evaluator (FE)   pure TS function over the public         │
│                   dashboard payload — no server round-trip │
├────────────────────────────────────────────────────────────┤
│  Ledger (BE)      append-only award_ledger:                │
│                   (username, label_id, period_start)       │
├────────────────────────────────────────────────────────────┤
│  Streaks (BE)     backward walk over the ledger →          │
│                   { labelId: count } for FE chip badges    │
└────────────────────────────────────────────────────────────┘
```

Each layer has one job. That's the whole point — you can rewrite any
one of them without touching the others.

### Layer 1 — Catalog (DB)

`internal/db/migrations/00036_labels_catalog.sql` bootstraps a **labels**
table. One row per possible award. Columns that matter for the game:

| Column            | Purpose                                                    |
|-------------------|------------------------------------------------------------|
| `id`              | Stable slug, e.g. `python-master`                          |
| `kind`            | `tier` \| `tribe` \| `archetype` \| `meme` \| `patch`      |
| `label`           | Display text — uppercased at render                        |
| `glyph`           | 1-3 char emoji/symbol fallback if there's no image         |
| `description`     | Tooltip one-liner                                          |
| `rank`            | Sort key — higher = more prominent                         |
| `tier`            | For `tier` kind: `novice` → `legend`                       |
| `condition`       | JSONB — the predicate itself (see Layer 2)                 |
| `period_default`  | Ledger cadence override (`daily`/`weekly`/`monthly`/`""`)  |
| `optimized_prompt`| Text prompt for ComfyUI to render the emblem image         |

The catalog ships as SQL seeds. **114 labels** across 5 kinds:

- **Tiers** — a 5-band ladder per axis-value: Novice / Apprentice /
  Adept / Master / Legend of Python, of Vim, of Go, etc. Auto-generated
  from `tierLabels.ts` so you get a ladder per language without
  hand-authoring 5 rows.
- **Tribes** — community identity. Vim Tribe, VS Code Tribe, Linux
  Tribe. Lifetime; can't lose it.
- **Archetypes** — behavioral. Night Owl. Weekend Warrior. Polyglot.
  Deep Focuser. Held simultaneously; some overlap intentionally.
- **Memes** — the OP shiznit. Sigma Grindset. Kawaii Coder. Space
  Marine of the Terminal. Ranks intentionally OUTRANK archetypes so
  memes win the hero top-3 slot when they fire.
- **Patches** — event-driven military-op citations. Rapid Response Team.
  Fire Fighter. Terminal Purist. Render with a double-amber border and
  `★` prefix so they read as achievements-in-the-moment, not identity.

Adding a label is **one INSERT**. No code change. That's on purpose.

### Layer 2 — Evaluator (Go, server-side)

The catalog holds intent. The evaluator turns intent into awards.

`internal/labels/evaluator.go` — pure Go, no I/O, no clock, no random.
Takes a `Condition` node and a `*Payload` (a projection of the same
public-dashboard payload the FE renders from) and returns `true` or
`false`. `EvaluateAll(payload, catalog) []LabelAward` is the entry
point every handler uses.

Every condition is data (same JSON shape stored in `labels.condition`
JSONB — unchanged by the port):

```json
// PYTHON MASTER — 100 hours logged in Python
{ "kind": "axis-time", "axis": "languages", "value": "Python",
  "op": ">=", "hours": 100 }

// TERMINAL PURIST — vim + neovim + emacs ≥ 50h combined
{ "kind": "axis-time-sum", "axis": "editors",
  "values": ["vim","neovim","emacs"], "op": ">=", "hours": 50 }

// NIGHT WATCH — ≥ 40% of punchcard between 22:00-05:00
{ "kind": "punchcard-hour-pct", "hoursIn": [22,23,0,1,2,3,4,5],
  "op": ">=", "pct": 0.40 }

// POLYGLOT — 5+ languages each ≥ 20h
{ "kind": "distinct-count", "axis": "languages",
  "minHoursEach": 20, "op": ">=", "n": 5 }

// SPRINTER — last-7 avg is 2x the prior-7 avg
{ "kind": "trend", "window": "last7-vs-prior7", "op": ">=", "ratio": 2.0 }

// Composition
{ "kind": "all", "of": [ <condA>, <condB> ] }
{ "kind": "any", "of": [ <condA>, <condB> ] }
{ "kind": "not", "of": <condA> }
```

The full primitive set today:

| Primitive              | What it inspects                                    |
|------------------------|-----------------------------------------------------|
| `axis-time`            | hours on one axis-value                             |
| `axis-time-sum`        | sum of hours across N axis-values                   |
| `axis-pct`             | % of axis total for one value                       |
| `top-share`            | % held by the #1 entry on an axis                   |
| `distinct-count`       | N distinct entries each ≥ minHoursEach              |
| `punchcard-hour-pct`   | % of clock in a subset of hours-of-day              |
| `punchcard-dow-pct`    | % of clock in a subset of days-of-week              |
| `streak` / `daily-avg` | daily-heartbeat streaks + daily average hours       |
| `trend`                | last-7 vs prior-7 ratio                             |
| `all` / `any` / `not`  | boolean composition                                 |

Extension rule: adding a primitive is **one case** in the switch, **one
struct** in `types.go`, **one test** in `evaluator_test.go`. If the
existing set covers a new label, compose — don't add.

`EvaluateAll` walks the whole catalog, filters to passing specs, dedupes
tier collisions (highest tier per axis-value wins), sorts by rank desc
(id asc secondary), returns `[]LabelAward`. Pure function; the wrapping
handler is the one that touches DB and clocks.

**Client contract:** the FE never runs the evaluator anymore. Two hooks
in `web/src/features/publicprofile/labels/useAwards.ts` — `useOwnAwards()`
and `usePublicAwards(slug)` — pull awards from the endpoints in Layer
3. `useAwards()` picks between them by sniffing the `:slug` route param,
so widget authors don't have to plumb scope through props.

### Layer 3 — Ledger (BE, append-only)

The evaluator is stateless. That's great for correctness — every render
recomputes from scratch — but it means "you had this label yesterday
too" isn't a thing the evaluator can see. Enter the **award ledger**.

`internal/db/migrations/00044_award_ledger.sql`:

```sql
CREATE TABLE award_ledger (
  username     TEXT      NOT NULL,
  label_id     TEXT      NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  period_type  TEXT      NOT NULL,       -- daily / weekly / monthly
  period_start TIMESTAMPTZ NOT NULL,
  period_end   TIMESTAMPTZ NOT NULL,
  logged_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (username, label_id, period_start)
);
```

Every time an authenticated user hits `GET /api/v1/users/current/awards`,
the server evaluates the catalog and — before returning the awards —
writes ledger rows in the same handler. The server:

1. Resolves the caller's timezone via the 3-level chain
   (`users.timezone` → `BOOM_DEFAULT_TIMEZONE` → `UTC`)
2. Computes `[period_start, period_end)` for each firing label at
   `at=now()` in that timezone
3. Batches one upsert per item — `ON CONFLICT DO NOTHING`. Idempotent
   inside a period; repeat visits are cheap and don't skew the streak

Public-profile hits (`GET /api/public/profile/:slug/awards`) evaluate
and return, but **do NOT write to the ledger** — a public visitor
must not be able to advance someone else's streak. The one authoritative
"you had this label" moment is the owner's own read.

**Historical replay:** `POST /api/v1/users/current/awards/backfill
{days: N}` walks the last N days server-side, rebuilds each day's
payload snapshot, evaluates, and writes ledger rows with `at=D` for
that day. Powers the "Streak backfill" tool in Settings → Admin;
supersedes the earlier browser-side per-day loop.

The primary key `(username, label_id, period_start)` is the whole trick.
Two visits on the same day → one row. Fifty visits over a Monday → one
row. Zero visits on Tuesday → no row. That absence-of-a-row is what
makes the walker in Layer 4 possible.

**Kind → default period:**

| Kind        | Default period | Rationale                          |
|-------------|----------------|-------------------------------------|
| `tier`      | `lifetime`     | Once you're Python Master, you're it |
| `tribe`     | `lifetime`     | Vim Tribe is forever                |
| `archetype` | `weekly`       | Night Owl waxes and wanes            |
| `meme`      | `weekly`       | Meme energy is a mood                |
| `patch`     | `daily`        | Citations reward the specific day    |

Lifetime labels **don't get ledger rows** — no streak concept applies.
Overridable per-label via `labels.period_default`.

### Layer 4 — Streaks (BE, walker)

`GET /api/v1/users/current/awards/streaks` returns
`{ [labelId]: streakCount }` for every label with an *active* streak
(i.e. fired in the current period). The walker in
`db.GetLabelStreaks`:

1. For each `(username, label_id)`, load rows ordered `period_start DESC`
2. If the newest row's `period_start` ≠ current period bounds → streak = 0
   (label didn't fire this period; no badge)
3. Otherwise walk backward: for each expected preceding period, if a row
   exists → increment; on the first gap → stop

That's it. The evaluator says "fires now" (Layer 2); the ledger says
"fired then" (Layer 3); the walker joins them into "fires now AND
fired for N periods prior" (Layer 4). The `Nx` badge on the chip.

## The whole request flow

Visitor loads their own dashboard at `/app`:

```
Browser                    Boomtime API                    DB
   │                           │                            │
   ├─ GET /awards ────────────►│                            │
   │                           │ build public payload       │
   │                           │  ├── LoadHidden/RenameSets │
   │                           │  ├── GetUserActivity       │
   │                           │  ├── GetCategoryDaily      │
   │                           │  └── GetPunchcard          │
   │                           │ ListLabels → catalog       │
   │                           │ EvaluateAll(payload, cat.) │
   │                           │ LogAwards (own only)       │
   │                           │  ON CONFLICT DO NOTHING    │
   │◄────── LabelAward[] ──────┤                            │
   │                           │                            │
   ├─ GET /awards/streaks ────►│                            │
   │◄─── {night-watch: 3, ...} │ walker over ledger         │
   │                           │                            │
   └── useAwards() + useAwardStreaks() → render chips w/ Nx badges
```

Visitor loads a public profile at `/p/dj`:

```
Browser                    Boomtime API                    DB
   │                           │                            │
   ├─ GET /public/profile/dj/awards ─────────────────►────►│
   │                           │ resolve slug → owner       │
   │                           │ build owner's payload      │
   │                           │ ListLabels → catalog       │
   │                           │ EvaluateAll                │
   │                           │ *NO ledger write*          │
   │◄────── LabelAward[] ──────┤                            │
   │                           │                            │
   ├─ GET /public/profile/dj/awards/streaks ────────►      │
   │◄─── {night-watch: 3, ...} │ walker (visitor's view)    │
   │                           │                            │
   └── useAwards() picks public via slug param → render
```

One request per read, ledger write folded in on the own path,
everything idempotent.

## Why "server evaluate + server-write ledger"

Three shapes were on the table:

1. **Fully client-side** — evaluator only, no persistence. Streaks
   impossible; every reload starts over.
2. **Fully server-side batch** — a cron computes awards nightly.
   Streaks trivial; but no live "you just earned this" moment; every
   catalog tweak requires a rerun.
3. **Server evaluate on-read + write-through ledger** — what we shipped.
   Every own-dashboard hit re-evaluates against the current catalog and
   writes the ledger inline. Public visits evaluate but never write.

Shape #3 wins on the same three counts as any live-eval approach:
the **catalog is authorable** (edit a threshold, refresh, every
subsequent visitor sees the new answer immediately; no batch to
rerun), the **ledger stays honest** (server-authoritative, no client
lies possible), and the **request budget is small** (one own read =
one payload query + one catalog query + one batch upsert; public read
skips the upsert).

Shape #3 replaced an earlier "shape #3.5" — client-side JIT evaluate
plus a client POST to `/awards/log` after each evaluate — which put
too much of the ledger-integrity contract in the browser. See
`docs/design/labels-server-eval.md` for the migration write-up.

The one gotcha survives: rule changes are retroactively invisible.
A ledger row from 2026-07-15 says "fired on that date" but doesn't
remember the threshold that fired it. That's a deliberate call:
streaks are about **consistency**, not **rule archaeology**. Tighten
a rule and next period the evaluator gets a different answer; the
streak may break, and that's honest.

## What extensibility actually looks like

**Add a new label.** One SQL row:

```sql
INSERT INTO labels (id, kind, label, glyph, description, rank, condition)
VALUES (
  'ci-firefighter', 'patch', 'CI FIREFIGHTER', '🚒',
  'Debugging session > 3h logged in a single day',
  50,
  '{"kind":"axis-time","axis":"categories","value":"Debugging",
    "op":">=","hours":3}'::jsonb
);
```

Refresh → next visitor sees it if they qualify. No deploy.

**Add a new primitive.** Say we want "worked from ≥ 3 machines this
week" — no existing primitive covers `machines` (public payload
strips it, and the current axis set doesn't include it anyway). Add:

1. `MachineDistinctCond` interface in `types.ts`
2. One `case` in `conditions.ts` switch that reads `payload.machines`
3. Extend `PublicDashboardPayload` to expose `machines` publicly (or
   scope the primitive to authenticated own-view)
4. Test in `conditions.test.ts`

Four small edits, one commit. Every catalog row can now use `machines`.

**Add a new kind.** Rarer, but suppose we want `guild` — group-scoped
labels that require N members of the same tribe. Add `guild` to the
`kind` check constraint, teach `KindDefaultPeriod` what cadence a guild
label runs on, teach the FE `LabelChip` how to render its border
(guilds get a hex badge, maybe). The evaluator doesn't care —
`kind` is a display concept, not an evaluation one.

**Retire a label.** `DELETE FROM labels WHERE id = 'sigma-grindset';`
Foreign key cascades wipe the ledger rows — clean. Streaks for other
labels are unaffected.

## Where AI layers slot in

The system is intentionally boring in the middle — the evaluator is
pure TS over a fixed vocabulary. Every AI opportunity is at an **edge**:
authoring, presenting, or narrating. That keeps the runtime
deterministic and testable while opening a lot of surface for models.

### 1. Label authoring assistant (already partial)

Today, `labels.optimized_prompt` is a text field consumed by a ComfyUI
worker that renders the emblem image. That prompt is authored — usually
by hand — and then the pipeline runs.

The FE admin sheet could wrap a small model to:

- **Suggest a condition** from a natural-language description ("fire
  when someone codes past 2 AM three days in a row") → emit a
  candidate `Condition` JSON. Human reviews and saves. The DSL's tiny
  surface makes the model's job easy — it emits one of 13 known kinds
  with fixed fields.
- **Draft the description/tagline** consistent with the tone of
  neighboring labels. Feed the model 10 nearby specs, ask for one in
  the same voice.
- **Auto-optimize the image prompt** from a rough brief. Already
  partly done today (there's a `POST /avatar/synthesize-prompt`
  endpoint doing something similar for chibi avatars).

The classifier lives at authoring time. If it's wrong, the human catches
it in preview. Runtime never touches the model.

### 2. Narrative summarizer

Given a visitor's `LabelAward[]` + streak map, write a **one-paragraph
"who is this person"**. Not a chip list — actual prose:

> "DJ is a night-owl polyglot with a serious Python streak (3 weeks
> and counting) and a fresh Rust habit — last-7 hours are 2x prior-7.
> Terminal-first (vim + tmux dominate the editor mix) and heavy on
> Linux. Weekends stay quiet; weekday code is stacked 22:00-02:00."

Cache per-payload (payload has a hash) → one model call per profile
per stats-change. Below the hero tagline, above the widgets. This is
where a model earns its keep — the raw label list is legible but the
*story* isn't. Language models are good at stories.

### 3. Adaptive difficulty / calibration

A common gamification failure: thresholds set at authoring time
either trivialize (everyone gets Master) or ossify (no one moves).
A small model can watch the ledger population — say once a week per
label:

- If `90%` of users are hitting `python-master` (100h) → suggest
  raising to 250h or introducing an intermediate tier
- If `<1%` of users have ever hit `weekend-warrior` → the threshold's
  broken or the concept doesn't map to real behavior; suggest
  reworking or retiring

Emit a **PR against the labels seed** with a diff a human reviews.
The model touches config, not runtime.

### 4. Rival profile / peer comparison

Given `LabelAward[]` for viewer + `LabelAward[]` for the profile being
viewed, generate a **contrast summary**: "you're both Night Watch,
but they hit Legend of Python while you're Adept — 40h to go."
Motivational chunk. Same caching strategy as the summarizer.

### 5. Nudge / notification prose

Ledger has "you got Rapid Response Team yesterday." Streak walker
knows "you're at 6 days and the streak breaks at midnight." A model
writes a **push-notification-ready one-liner** that isn't
"You have a new achievement." Micro-copy is where models are best,
because there's no wrong answer.

### 6. Emblem-image self-critique

The ComfyUI worker generates an image. A vision model looks at it,
compares against the description, and flags "this doesn't read as
FIRE FIGHTER" → auto-retry with a beefed-up prompt. Cheap way to
raise average art quality without hand-curation.

## What's deliberately not AI

- **The evaluator.** Boolean predicates over numbers. If a model
  decides who gets Python Master, "who's the Python Master" stops
  meaning anything. The rule has to be legible to be a *rule*.
- **Streak counting.** Determinism is the whole point.
- **Award writing.** The log is a source of truth. Truth doesn't
  hallucinate.

Every AI edge above **suggests to a human** or **decorates for a
reader**. None mutate the runtime path.

## Try it

- Public profile with awards: `/p/<any-slug>`
- Admin catalog editor: **Settings → Admin → Labels**
- Ledger inspector: **Settings → Admin → Labels → Award ledger inspector**
  (per-label row count + streak, click-through to raw period rows)
- Streak backfill tool: **Settings → Admin → Labels → Streak backfill**
  (replay the evaluator N days back to seed historical rows)

## Files worth reading

**Go (server-side):**

- `internal/labels/types.go` — the whole DSL vocabulary (Condition
  interface + 13 primitives + 3 composers)
- `internal/labels/evaluator.go` — `EvaluateCondition` + `EvaluateAll`
- `internal/labels/dbrow.go` — DB-row → LabelSpec bridge (tierKey
  derivation lives here)
- `internal/handler/awards_eval.go` — `GET /awards` (own + public)
- `internal/handler/awards_backfill.go` — `POST /awards/backfill` for
  historical replay
- `internal/db/award_ledger.go` — persistence + streak walker
- `internal/db/migrations/00036_labels_catalog.sql` — the catalog schema
- `internal/db/migrations/00044_award_ledger.sql` — the ledger schema

**TypeScript (client-side):**

- `web/src/features/publicprofile/labels/types.ts` — just the display
  shapes the FE needs (`LabelAward`, `LabelCatalogRow`, and the
  `Condition` primitive TYPES for the tooltip formatter — no runtime
  helpers)
- `web/src/features/publicprofile/labels/useAwards.ts` — the hooks
  widgets use
- `web/src/features/publicprofile/labels/formatCondition.ts` — renders
  "Fires when: ..." in chip tooltips
- `web/src/features/publicprofile/labels/LabelChip.tsx` — the chip
  itself

Server side is ~1000 LOC of Go; client side is a couple hundred LOC of
display code. That's the shape you want when the runtime has to be
trustworthy but the vocabulary has to grow forever.
