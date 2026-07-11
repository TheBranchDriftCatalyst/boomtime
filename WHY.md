# Why boomtime exists

**We built this in ~5 hours.** Not a weekend, not a sprint — an afternoon. This is
**agentic engineering** — a human directing a fleet of subagents against a real 440k-heartbeat
dataset, verifying every step live — not vibe coding. (Though, let's be honest, the vibing
is still very much there. 🌆)

## The itch

I wanted my coding stats without paying wakatime.com a subscription. The obvious move: run
the open-source one myself. That's **[hakatime](https://github.com/mujx/hakatime)** — a
solid, Wakatime-compatible tracker written in Haskell.

So we took it and, in a single pass, **converted the whole thing to Go** — Echo + pgx +
goose, the schema and stat SQL kept byte-compatible so existing editor plugins never notice
the swap. React + Vite + Tailwind for the dashboard. One embedded binary. Point
`~/.wakatime.cfg` at it, import your history, cancel the subscription. Done.

Except "done" is where it got interesting.

## Then we made it actually good

A faithful port inherits the original's warts. So we didn't stop at parity — we fixed the
things that always bugged me about time trackers:

- **Attributed-duration `gap_seconds`.** "Time spent" isn't stored anywhere — it's inferred
  from the gaps between heartbeats, and naively that means reconstructing sessions on every
  request. We precompute the gap to the previous heartbeat **at ingest**, so a query becomes
  a windowless `SUM(CASE WHEN gap ≤ limit …)`. A daily **rollup table** backs the common path.
- **A first-class Wakatime converter/importer** — durable, resumable, live-log-streamed,
  idempotent — so migrating years of history off wakatime.com is a button, not a script.
- **All-time that's actually instant.** Top-N + "Other", ~weekly bucketing, the rollup —
  440k rows, "All time", no spinner.

## The stuff Wakatime itself can't really do

Using Wakatime for years, two things always felt broken. We rebuilt both from the ground up:

### 1. Surfacing all-time history is painful
Wakatime hides your deep history behind its UI and its plan. boomtime treats **"All time"**
as a first-class range and keeps it fast — the same `gap_seconds` + rollup + bucketing that
powers a 7-day view powers a two-year view.

### 2. Renaming / mapping projects is brittle
Wakatime's project renames and mappings are fragile and one-way. We threw that out and
rebuilt curation **into the optimized query layer itself** — which is the move that
**unlocked reversible edits across your entire history**. Rules are stored, applied at
**query time**, and your raw heartbeats are **never mutated**. Delete a rule, it reverts
instantly. The Heartbeats Explorer stays a true raw audit even while your dashboards are
merged and cleaned.

## The OP move: curation with three strategies

Curation rules are the thing I don't think Wakatime really does — and we gave them **three
matching strategies**, composed through the same query-time remap engine:

1. **`exact`** — hide or rename a specific value (`hexstrike-ai` → hidden; `my-app` → `hakatime`).
2. **`regex`** — match many at once (`^Meet -` → `Meeting` collapses 14 meeting projects into one).
3. **`template` (capture/replace)** — regex **with capture groups + a replacement template**:
   `^@(.*)$ → \1` strips the `@` off `@swarm-graph`, `@drogon`, `@cli-tools` … 16 projects,
   one rule, with a live `raw → mapped` preview before you commit.

All reversible. All non-destructive. All applied to **every aggregation path** — stats,
rollup, leaderboards, momentum, project detail — while the audit stays raw. Plus reversible
**hide/suppress** for noise, and a derived-data health panel so you can see (and resync) the
gap/rollup layer that makes it fast.

## How it was built

- A human orchestrating specialized **subagents** (backend, frontend, design) in parallel,
  each verified against the **real dataset** — not synthetic fixtures, not "looks right in
  the mock."
- Every feature landed with a live pass: create the rule, watch the 16 `@`-projects strip;
  suppress a project, watch it vanish from dashboards but stay in the audit; flip to "All
  time" and watch it *not* freeze.
- An **isolated test database** and anonymized fixtures so the growing test suite never
  touches the real data it's modeled on.
- Committed in coherent slices, screenshotted, documented ([README](README.md) ·
  [DEMO](DEMO.md) · [ARCHITECTURE](docs/ARCHITECTURE.md)).

Five hours. A free, faster, more honest Wakatime — with curation superpowers the paid one
doesn't have. That's the why.
