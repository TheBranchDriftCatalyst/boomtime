# boomtime ↔ BookOrbit integration contract

**Mirrored.** The twin of this file lives in the fork at
`../bookorbit/docs/catalyst/BOOMTIME-INTEGRATION.md`. They describe one boundary
from two sides and must be edited together — a change to either half is a change
to the contract.

Status 2026-11-20: **designed, not built.** boomtime side is `boom-ksvt`; fork
side is `CAT-iew` (verify only — no fork code is expected to change).

---

## What crosses the boundary

Exactly one thing: **daily reading seconds**, so boomtime can fuse reading time
with coding time. No per-book data, no credentials, no write-back.

```
BookOrbit (system of record for books)
  user_reading_daily_stats (user_id, library_id, day)
    → reading_seconds, progress_delta, sessions_count
          │
          │  GET /api/v1/user-statistics/daily-reading?days=N
          │  X-Api-Key: <read-scoped key>
          │  → [{ day, readingSeconds, progressDelta, eventsCount }]
          ▼
boomtime (analytics / data fusion)
  reading_activity (owner, source='bookorbit', granularity='day', bucket_date)
    → listening_seconds
          │
          ▼
  reading.seconds  =  sum(listening_seconds) grouped by `source`
  → every existing dashboard, widget, leaderboard and award, unchanged
```

The fork's endpoint already sums across libraries and groups by day, so the
connector never aggregates libraries itself.

## Ownership

| Concern | Owner |
| --- | --- |
| Amazon credentials, device registration | **fork only.** boomtime holds none. |
| Kindle / Audible / Hardcover sync | **fork only.** |
| Liberation | **fork only.** |
| Session composition (position samples → reading seconds) | **fork**, once `CAT-7un` lands. Until then, nobody does it. |
| Daily aggregation | **fork** (`user_reading_daily_stats`) |
| Fusion with coding time, stats surfaces, awards | **boomtime** |

boomtime is a **read-only consumer**. If it ever needs to write to BookOrbit,
that is a new contract, not an extension of this one.

## The three things that must not drift

**1. The cutover date.** boomtime's historical `reading_activity` rows carry
`source='kindle'` and `source='audible'` from when boomtime did the syncing. The
connector writes `source='bookorbit'` **only** for
`bucket_date >= BOOM_BOOKORBIT_CUTOVER_DATE`. Without it, every overlapping date
is counted twice in every fused surface.

Two defences, deliberately both:
- *Structural* — the upsert's conflict target includes `source`, so a
  `bookorbit` write can never touch a `kindle`/`audible` row.
- *Semantic* — a startup refusal when the flag is on and the cutover is unset,
  plus a terminal job error if the cutover overlaps the newest legacy row.

A missing cutover must **fail loudly**, never default. A silent default is the
bug this paragraph exists to prevent.

**2. Zero must be distinguishable from no-data.** `daily-reading` *omits* days
with no data; the sibling `reading-heatmap` zero-fills them. The connector uses
`daily-reading` **for that reason** — a `source='bookorbit'` row existing means
the fork affirmatively reported that day. Switching endpoints would make "they
read nothing" and "the pull failed" indistinguishable.

Related: nothing is written until the whole window validates, and the write
returns the *set* of dates that landed rather than a count, so "0 of N wrote"
cannot read as success. The fork's own live testing produced exactly that bug —
a run that failed 1,060/1,060 items reported `completed` with a green tick.

**3. A fetch returning nothing retires nothing.** If the fork later stops
reporting a day, boomtime's existing row goes stale rather than being deleted.
Deleting on absence turns one empty response into silent data loss — the same
rule `change-detection-patterns.md` sets for set-reconcile sources. The escape
hatch is explicit and operator-driven: delete the `bookorbit` rows and re-pull.

## Auth

A BookOrbit **API key** (`api-key` module), read-scoped. The key *is* the
identity — `ApiKeyAuthenticator` resolves it to its owner server-side, so no user
id is ever sent and none can be spoofed.

boomtime maps that to one local username via `BOOM_BOOKORBIT_OWNER`. Single-owner
today; stated as an assumption rather than assumed silently.

**Known unverifiable risk:** if the API key is library-limited, or the owner has
libraries hidden from stats, the connector **silently undercounts** and cannot
detect it from this endpoint. Mitigated by a `boomtime bookorbit check` command
that prints row count / first day / last day / total seconds against a live key,
plus a metric to alarm on a sustained drop. There is no programmatic fix.

**Guard caveat:** sibling routes under `user-statistics` carry
`@RequireAppAttest()`, which refuses API-key credentials outright.
`daily-reading` does not — that is why it is the correct endpoint, not merely a
convenient one. If upstream ever adds that decorator to it, this integration
breaks; `CAT-iew` exists to verify the guard chain against a live key.

## Direction of travel: this should become a plugin

The pull design above needs **zero** fork changes, which is why it goes first.
But the integration belongs in the fork as a connector, not in boomtime as a
bespoke job — see `CAT-3qf`.

Today the fork's `packages/plugin-api` exposes only `IndexerPlugin`: there is no
plugin kind for a sync connector, no scheduled-execution host capability, no data
access on `PluginHost`, and the whole indexer/download-client contract is
operator-scoped with no user identity. Generalizing it is real upstream work that
benefits every integration — hardcover, readwise and storygraph are each a
bespoke module today, while indexers are clean drop-in plugins.

**When the plugin seam lands, the boomtime pull job retires.** That retirement is
the acceptance criterion on `CAT-3qf`, recorded so the interim pull does not
quietly become permanent.

## Why the reading query domains are no longer gated on books

`reading` and `readingEvents` used to 404 unless `BooksEnabled()`. They are
reading-*data* domains, not books domains, and the data outlived the domain that
first populated it. Gating them on the deprecated books flag would have 404'd
these fused surfaces exactly when the fusion started working. Un-gated in
`boom-sire`; see `books-domain-deprecated.md`.

## Pointers

- boomtime: `internal/shared/db/reading_activity.go`,
  `internal/shared/query/domains.go` (`registerReading`),
  `internal/shared/db/migrations/00061_reading_activity.sql`
- fork: `server/src/modules/user-statistics/user-statistics.repository.ts`
  (`getDailyReadingStats`), `server/src/modules/api-key/`,
  `server/src/db/migrations/0000_baseline.sql` (`user_reading_daily_stats`)
