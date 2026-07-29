# Server-side label evaluation

**Status:** shipped 2026-07-29 as epic `gaka-hc6`.
**Superseded design:** client-side JIT evaluate + client POST to
`/awards/log`. Kept alive for one release cycle via
`POST /awards/log` (still used by the historical-replay `backfill`
tool, now server-side too).

## Why we moved

The label evaluator used to live in TypeScript at
`web/src/features/publicprofile/labels/conditions.ts` +
`evaluator.ts`. Every browser view of a dashboard or public profile
ran it against the payload it had just fetched, then POSTed the
firing set to the server so the ledger could be written. Three
problems accumulated:

1. **Test coverage was expensive.** Proving "every label in the DB
   fires against some minimum-viable input" required either running
   the evaluator against real DB-seeded heartbeats (needs a node
   subprocess from the Go integration test) or duplicating the
   catalog into a TS fixture (drifts silently). We wanted a single
   Go integration test to walk the whole catalog.

2. **Ledger integrity depended on the client.** The server accepted
   whatever the client POSTed to `/awards/log`, checked it against
   the catalog for known-label filtering, and wrote. A malicious or
   buggy client could delay, drop, or duplicate the POST — the streak
   walker would see the effect of that lie.

3. **Public-profile awards ran evaluator in every visitor's browser.**
   Wasted CPU and a tiny privacy surface (the payload the evaluator
   read had to be shipped verbatim). Server-side eval + a cache header
   collapses N visitor computations into one server compute per cache
   TTL.

## What changed

- **Evaluator ported to Go**: `internal/labels/` mirrors the TS DSL
  byte-for-byte in semantics. 13 primitives + 3 composers. Same JSONB
  wire format — `labels.condition` blobs in the DB round-trip
  through `UnmarshalCondition` unchanged.
- **Two new endpoints**:
  - `GET /api/v1/users/current/awards` — own; runs `EvaluateAll`,
    writes ledger rows via existing `LogAwards`, returns
    `[]LabelAward`. Cache-Control: `private, max-age=30`.
  - `GET /api/public/profile/:slug/awards` — public; same shape,
    **no ledger write** (a public visitor cannot advance someone
    else's streak). Cache-Control: `public, max-age=180`.
- **Historical replay moved server-side**:
  `POST /api/v1/users/current/awards/backfill {days:N}` walks N days
  back, rebuilds each day's payload, evaluates, writes ledger rows
  with `at=D`. Powers the "Streak backfill" tool in Settings > Admin;
  replaces the earlier browser-side per-day loop.
- **FE consumers rewired to `useAwards()`** in
  `web/src/features/publicprofile/labels/useAwards.ts`. Picks own
  vs public via the current route's `:slug` param.
- **Client evaluator + related TS deleted**:
  `catalog.ts`, `evaluator.ts`, `conditions.ts`, `tierLabels.ts` and
  their tests — net ~3200 LOC gone from the FE.

## What stayed

- The Condition primitive TYPE declarations in `types.ts` — needed
  by `formatCondition.ts`, which renders "Fires when: ..." on chip
  tooltips. Runtime helpers around the types are gone.
- `useAwardStreaks(slug?)` — the streak-map READ hook, unchanged.
- `useLabelsCatalog()` — the admin editor still fetches the full
  catalog to render its edit UI.
- The old `POST /awards/log` endpoint — the historical-replay
  handler is a separate route, but `POST /awards/log` stays for any
  legacy caller and for surgical single-period writes.

## Endpoint contract

| Method | Path | Body | Response | Ledger writes? | Cache-Control |
|--------|------|------|----------|---------------|---------------|
| GET | `/api/v1/users/current/awards` | — | `[]LabelAward` | yes (current period) | `private, max-age=30` |
| GET | `/api/public/profile/:slug/awards` | — | `[]LabelAward` | **no** | `public, max-age=180` |
| POST | `/api/v1/users/current/awards/backfill` | `{days:N}` (1..365) | `{daysProcessed, rowsWritten, skipped, tookMs}` | yes (N historical periods) | — |

All three respect the 3-level timezone chain (users.timezone →
`BOOM_DEFAULT_TIMEZONE` → `UTC`) via `handler.resolveUserTZ` (gaka-dg7).

## Migration path for anyone reading this later

If you want to add a fifth kind of consumer (a widget SVG generator,
a Slack notification, a cron summary): call
`labels.EvaluateAll(payload, catalog)` in Go — same result the
`/awards` endpoints return. No wire protocol needed. If you're
extending the DSL with a new primitive, do it in **one** place now
instead of two:
`internal/labels/types.go` for the type +
`internal/labels/evaluator.go` for the switch case +
`internal/labels/evaluator_test.go` for coverage.

The single-source-of-truth win is the reason we accepted the
~3200-LOC delete plus the port cost. Every extension from here forward
lands in the same file the previous one landed in.
