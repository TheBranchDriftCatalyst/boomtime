# catalyst-books Sync Architecture

> **Status:** definitive architecture (2026-08). Implement from this.
> **Scope:** the `catalyst-books` (Kindle) + `catalyst-audiobooks` (Audible) domains —
> the domain model, the two sync modes, the Hardcover push connector + matching
> engine, finished-detection → events, a domain-agnostic notification subsystem,
> and the job wiring / feature gates / fusion hook.
> **Precedents this builds on:** `docs/design/catalyst-domains-spike.md` (the
> `Module` seam + fusion strategy) and `docs/design/book-tracking-research.md` (the
> per-source data reality). This doc GUIDES the phase-2/3/4 implementation beads.

**Reading conventions.** "CTX-verified" = a live-verified Audible fact from the
implementer's fieldwork (recipes reproduced verbatim in §2). Everything named in
`code font` (`reading_items`, `amazon.SignedGet`, `AudibleSyncKind`, …) is a real
symbol on the current tree — verify the exact signature at implement time, but the
names are correct as of writing.

**Non-negotiable push-safety rules** (from the task brief; they constrain every
section below):

- **Additive migrations only.** New columns/tables; never alter or drop an
  existing one. `reading_items` already exists (`00058`); we `ADD COLUMN` onto it.
- **Everything new is gated** behind `cfg.BooksEnabled()` (`BOOM_FEATURE_BOOKS`).
  `main` ships with the flag off and stays deployable.
- **Must BUILD:** `CGO_ENABLED=0 go build ./...` clean; FE `yarn typecheck` clean.
- **Siloed:** book data lives only in `reading_items` (+ the new `reading_activity`);
  it never writes into `heartbeats`/`stats`/any core model. The fusion layer READS
  it; it never writes into core.

---

## 0. The shape at a glance

```
                         ┌─────────────────────────────────────────┐
   Amazon device cred    │            internal/amazon              │
   (ONE "Connect Amazon") │  Store.Load → *DeviceCredential          │
        ─────────────────►│  Sign() / SignedGet(ctx,cred,host,path)  │
                          │  AudibleAPIHost(marketplace)             │
                          └───────┬─────────────────────┬───────────┘
                                  │                     │
                 ┌────────────────▼──────┐   ┌──────────▼───────────────┐
                 │ internal/domains/     │   │ internal/domains/books   │
                 │   audiobooks (Audible)│   │   (Kindle / whispersync) │
                 │  Backfill + Forward   │   │  Backfill + Forward      │
                 └────────────┬──────────┘   └──────────┬───────────────┘
                              │  UpsertReadingItem       │
                              │  UpsertReadingActivity   │
                              ▼                          ▼
                  ┌───────────────────────────────────────────────┐
                  │  db.reading_items   +   db.reading_activity     │   (siloed)
                  └───────────┬───────────────────────┬────────────┘
    finished diff → event     │                       │  DailySeries("books",…)
                              ▼                       ▼
             ┌─────────────────────────┐   ┌────────────────────────────┐
             │  internal/notify        │   │ fusion overlay (core/stats) │
             │  per-user event bus     │   │  reading vs coding calendar │
             │  + ONE WebSocket        │   └────────────────────────────┘
             └───────────┬─────────────┘
                         │  notify.Publish(BookFinished{…})
                         ▼
             web: useNotifications() → sonner toast

     ┌───────────────────────────────────────────────────────────────┐
     │  internal/hardcover  (PUSH connector + matching engine)         │
     │  reads reading_items → match ladder → insert_user_book(_read)   │
     └───────────────────────────────────────────────────────────────┘
```

Every arrow above is either already built (§ "already built" in the brief) or
specified concretely below.

---

## 1. Domain model

### 1.1 `reading_items` — the fused per-book row (extend in place)

`reading_items` exists today (`internal/db/migrations/00058_reading_items.sql`,
`internal/db/reading_items.go`). Current columns:

| column | type | notes (existing) |
|---|---|---|
| `id` | `bigserial` PK | |
| `owner` | `text NOT NULL` | `REFERENCES users(username) ON DELETE CASCADE` |
| `source` | `text NOT NULL` | `'audible' \| 'kindle'` |
| `external_id` | `text NOT NULL` | ASIN; `UNIQUE (owner, source, external_id)` |
| `title` | `text` | |
| `authors` | `text` | CSV of author names |
| `cover_url` | `text` | already present |
| `status` | `text` | `want \| reading \| read \| paused \| dnf` |
| `progress_percent` | `integer` | 0..100 |
| `finished` | `boolean` | |
| `started_at` | `timestamptz` | `COALESCE`-preserved on upsert |
| `finished_at` | `timestamptz` | `COALESCE`-preserved on upsert |
| `rating` | `numeric` | `COALESCE`-preserved |
| `raw_meta` | `jsonb` | full source item, so new attrs need no migration |
| `synced_at` | `timestamptz` | |

**New migration `00059_reading_items_metadata.sql`** — additive `ADD COLUMN … `
(all nullable / defaulted, so existing rows are untouched):

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.reading_items
  ADD COLUMN IF NOT EXISTS subtitle        text  NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS narrators       text  NOT NULL DEFAULT '',   -- Audible narrators[].name CSV
  ADD COLUMN IF NOT EXISTS series          text  NOT NULL DEFAULT '',   -- series[0].title (+ sequence in raw_meta)
  ADD COLUMN IF NOT EXISTS runtime_min     integer,                     -- runtime_length_min (audio) / page-derived (kindle)
  ADD COLUMN IF NOT EXISTS purchase_date   timestamptz,                 -- library item purchase_date
  ADD COLUMN IF NOT EXISTS isbn            text  NOT NULL DEFAULT '',    -- isbn / amazon-side isbn
  ADD COLUMN IF NOT EXISTS amazon_asin     text  NOT NULL DEFAULT '',   -- library amazon_asin (print/kindle sibling of the audio asin)
  ADD COLUMN IF NOT EXISTS genres          jsonb,                       -- category_ladders flattened → ["Fiction","Sci-Fi",…]
  ADD COLUMN IF NOT EXISTS goodreads_rating numeric,                    -- goodreads_ratings.rating (community avg, not user's)
  -- Hardcover match cache (see §3.4) — additive, one place, avoids re-fuzzing:
  ADD COLUMN IF NOT EXISTS hardcover_book_id    bigint,
  ADD COLUMN IF NOT EXISTS hardcover_edition_id bigint,
  ADD COLUMN IF NOT EXISTS match_method         text,   -- 'asin' | 'isbn13' | 'search' | 'manual' | 'nomatch'
  ADD COLUMN IF NOT EXISTS match_confidence     numeric,
  ADD COLUMN IF NOT EXISTS hardcover_synced_at  timestamptz,
  ADD COLUMN IF NOT EXISTS hardcover_user_book_id bigint; -- id of the pushed user_book row, for update vs insert
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.reading_items
  DROP COLUMN IF EXISTS subtitle, DROP COLUMN IF EXISTS narrators, /* … */ ;
-- +goose StatementEnd
```

Notes:
- `cover_url` is **already there** — the task listed it under "NEW" but it exists;
  just start populating it (`product_images` largest URL).
- `finished_at` is **already there**; the NEW piece is *where its value comes from*:
  the Audible **finished sweep** `event_timestamp` (§2.2), not `/library`. `/library`
  has no finish date.
- Extend the `ReadingItem` struct + `UpsertReadingItem`/`ListReadingItems` scans in
  `internal/db/reading_items.go` in the same additive way. Keep the existing
  `COALESCE(EXCLUDED.x, reading_items.x)` guard for every backfill-able field
  (`started_at`, `finished_at`, `rating`, `purchase_date`, `goodreads_rating`) so a
  later low-fidelity source can't clobber a high-fidelity value.
- Nothing here is `source`-specific in schema; a Kindle row and an Audible row are
  the same shape, distinguished by `source`. The `amazon_asin` column is the join
  key that lets a Kindle edition and its Audible edition be recognized as the same
  work later (§3 matching can dedupe on it).

### 1.2 `reading_activity` — NEW daily/monthly time-series (for the fusion overlay)

`reading_items` is the *current-state* table (one row per book). The **fusion
overlay** (reading-time vs coding-time on one calendar, §6.4) needs a *time-series*:
how many seconds listened / pages read on each day. That is a different grain, so it
gets its own siloed table — **do not** bolt buckets onto `reading_items`.

**New migration `00060_reading_activity.sql`:**

```sql
-- +goose Up
-- +goose StatementBegin
-- Per-day (or per-month) reading/listening activity, the time-series the fusion
-- layer overlays on the coding calendar. SILOED like reading_items: ON DELETE
-- CASCADE with the user, never writes into heartbeats/stats.
--
--   source      — 'audible' | 'kindle' | 'amazon-export'
--   granularity — 'day' | 'month'  (Audible aggregates support both; §2.3)
--   bucket      — the bucket's start date (a DATE; for 'month', the 1st)
--   listening_seconds — Audible total_listening_stats for the bucket
--   pages       — Kindle/derived pages read in the bucket (nullable; audio has none)
CREATE TABLE IF NOT EXISTS public.reading_activity (
    id                bigserial   PRIMARY KEY,
    owner             text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source            text        NOT NULL,
    granularity       text        NOT NULL DEFAULT 'day',
    bucket            date        NOT NULL,
    listening_seconds bigint      NOT NULL DEFAULT 0,
    pages             integer,
    raw_meta          jsonb,
    synced_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, source, granularity, bucket)
);
CREATE INDEX IF NOT EXISTS reading_activity_owner_bucket_idx
    ON public.reading_activity (owner, bucket);
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.reading_activity;
-- +goose StatementEnd
```

DAL (`internal/db/reading_activity.go`, mirror `reading_items.go`):

```go
type ReadingActivity struct {
    Owner            string
    Source           string      // "audible" | "kindle" | "amazon-export"
    Granularity      string      // "day" | "month"
    Bucket           time.Time   // date
    ListeningSeconds int64
    Pages            *int
    RawMeta          []byte
}
func (d *DB) UpsertReadingActivity(ctx, ReadingActivity) error   // ON CONFLICT (owner,source,granularity,bucket) DO UPDATE
func (d *DB) ListReadingActivity(ctx, owner, source string, from, to time.Time) ([]ReadingActivity, error)
func (d *DB) DeleteReadingActivity(ctx, owner, source string) (int64, error)  // the "wipe my data" path
```

Granularity choice: the **backfill** writes `granularity='month'` all-time (cheap,
one aggregates call per ≤12-month window — §2.3), and the **forward sync** writes
`granularity='day'` for the current/recent window (fine grain where it matters). The
fusion query can prefer `day` rows and fall back to spreading a `month` bucket
evenly — but keep them as separate rows; never overwrite a `day` bucket with a
`month` one (the `UNIQUE` includes `granularity`, so they coexist).

### 1.3 Both tables are siloed + user-wipeable

Register both new pieces the same way `reading_items` is:

- `ON DELETE CASCADE` on `owner` (automatic wipe when a user is deleted).
- A per-user delete path (`DeleteReadingItems` / `DeleteReadingActivity`) for the
  "delete my book data" request.
- **Backup registry:** neither table needs an *encrypted* column (the Amazon secret
  lives on `users.encrypted_amazon_device`, already registered in
  `internal/domains/registry.go`). But if the whole-DB backup enumerates tables,
  add `reading_items` + `reading_activity` to `internal/db/dump.go`'s table set (or,
  better, to a `BackupTables()` entry once the `Module` seam lands) so a
  backup→restore round-trip preserves synced book data. **This is the "silently
  dropped from backups" incident class — do it explicitly.**

---

## 2. Sync architecture — two modes

Both domains (Audible, Kindle) run the **same two-mode pattern**. A domain's
`Service` exposes two idempotent entrypoints; the job layer (§6) decides which to
call.

```
Backfill(ctx, owner)  — one-shot, all-time. Sweeps everything, walks every
                        cursor to exhaustion, seeds sync_state. Run once on connect
                        (or on demand from the admin/diagnostics page).
Forward(ctx, owner)   — periodic delta. Uses the stored cursors to fetch only
                        what's new since last run, then advances the cursors.
```

`SyncUser` (the current `audiobooks.Service.SyncUser`) becomes the **Forward**
path's per-user body. `Backfill` is new.

### 2.1 Where cursors live — `sync_state`

Forward sync needs three per-user, per-domain cursors. Put them in a small NEW
table rather than smearing columns across `reading_items`:

**Migration `00061_reading_sync_state.sql`:**

```sql
CREATE TABLE IF NOT EXISTS public.reading_sync_state (
    owner                  text NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source                 text NOT NULL,          -- 'audible' | 'kindle'
    library_purchased_after timestamptz,           -- forward library delta cursor (RFC3339)
    finished_cursor        timestamptz,            -- last finished-sweep event_timestamp seen
    activity_cursor        date,                   -- last aggregates window filled
    last_backfill_at       timestamptz,            -- null until the one-shot backfill completes
    last_forward_at        timestamptz,
    PRIMARY KEY (owner, source)
);
```

DAL: `GetSyncState(ctx, owner, source)`, `SetSyncState(...)` (upsert). Advance
cursors **only after** a page/window is durably upserted, so a crash re-fetches the
last window rather than skipping it (at-least-once, idempotent upserts absorb the
overlap).

### 2.2 Audible — the exact recipes (CTX-verified)

All calls go through `amazon.SignedGet(ctx, cred, host, pathAndQuery)` with
`host = amazon.AudibleAPIHost(cred.Marketplace)`. `cred := Amazon.Load(ctx, owner)`.

**(a) Library sweep** — `GET /1.0/library`
- `response_groups=<comma list>` selects fields. The verified useful set:
  `product_desc,contributors,product_attrs,product_extended_attrs,series,category_ladders,is_finished,percent_complete,listening_status`.
  156 fields/item are available; map: `asin`, `title`, `subtitle`, `authors[].name`,
  `narrators[].name`, `series[].title`/`.sequence`, `is_finished`,
  `percent_complete`, `listening_status`, `runtime_length_min`, `purchase_date`,
  `product_images` (→ `cover_url`), `category_ladders` (→ `genres`), `isbn`,
  `amazon_asin`, `publisher_name`, `language`, `goodreads_ratings.rating`, `rating`.
- **Paging:** `num_results=1000&page=N` — **max 1000/page**. Loop `page=1,2,…` until
  a page returns **fewer than 1000** items (short page = last page). The current
  `FetchLibrary` hardcodes `page=1`; the backfill must loop.
- **Backfill:** full sweep, no date filter.
- **Forward delta:** add `&purchased_after=<RFC3339>&sort_by=-PurchaseDate` where the
  timestamp is `reading_sync_state.library_purchased_after`. After the sweep, set
  the cursor to the newest `purchase_date` seen.

**(b) Finished sweep** — `GET /1.0/stats/status/finished`
- `?start_date=2000-01-01T00:00:00Z` for the all-time backfill.
- Response: `{ continuation_token, mark_as_finished_status_list: [ {asin,
  event_timestamp, is_marked_as_finished, update_date}, … ] }`. **100/page** — follow
  `continuation_token` until it is empty/absent.
- **`event_timestamp` IS the finish date** → write it to `reading_items.finished_at`
  (and set `finished=true`, `status='read'`) for the matching `owner+source+asin`.
- **Forward:** `start_date=<reading_sync_state.finished_cursor>`; advance the cursor
  to the max `event_timestamp` seen. **This sweep is what powers finished-detection →
  events (§4)** — a row that flips `finished false→true` here is a newly finished
  book.

**(c) Listening aggregates** — `GET /1.0/stats/aggregates?response_groups=total_listening_stats&store=Audible`
- Two window modes (use EITHER):
  - **daily:** `daily_listening_interval_duration=<=30` (days) +
    `daily_listening_interval_start_date=YYYY-MM-DD`. Verified working live.
  - **monthly:** `monthly_listening_interval_duration=<=12` (months) +
    `monthly_listening_interval_start_date=YYYY-MM`.
- **No max lookback.** Loop windows *backward* from today to the account start; stop
  when a window returns empty buckets (nothing before the account existed).
- **Backfill:** walk **monthly**, 12 months per call, back to the first non-empty
  window → `UpsertReadingActivity(granularity='month', source='audible')`.
- **Forward:** one **daily** call for the current window (last ≤30 days) →
  `granularity='day'`. Advance `reading_activity` cursor to today.

### 2.3 Audible mode → call matrix

| | Library | Finished | Aggregates | Writes |
|---|---|---|---|---|
| **Backfill** (one-shot) | all pages, no filter | `start_date=2000-01-01`, follow token | monthly windows back to account start | `reading_items` (state + metadata + `finished_at`), `reading_activity` (month) |
| **Forward** (periodic) | `purchased_after=<cursor>&sort_by=-PurchaseDate` | `start_date=<finished_cursor>`, follow token | current ≤30-day daily window | same tables; advances all three cursors; **emits BookFinished events** (§4) |

### 2.4 Kindle parallel (follow-up)

`internal/domains/books.Service.SyncUser` is a stub today. Same two-mode shape, but
against the **whispersync / Fiona** endpoints (reuse the SAME `DeviceCredential`;
`amazon.Sign` is verified). Per `book-tracking-research.md §3.2`:

- **Library:** `GET …/FionaTodoListProxy/syncMetaData` (XML) → ASIN, title, authors,
  cover → `reading_items` (`source='kindle'`).
- **Progress:** `GET api.amazon.com/whispersync/v2/data/{user_id}/datasets` →
  position; **`progress_percent = (startPosition + position) / endPosition`** (needs
  the book metadata to derive — Kindle stores KRX units, never a %). `user_id` is the
  numeric `DeviceCredential.CustomerID`, NOT the ASIN.
- Kindle is **date-blind** — no start/finish dates. `reading_activity` pages for
  Kindle come from the **Amazon "Request My Data" export** (`source='amazon-export'`,
  a user-uploaded-file backfill), and finish dates come from **Goodreads** CSV/RSS.
  Both are file-upload backfills (zero live auth) — separate follow-up beads, not on
  the periodic path.

Ship Audible fully first (it's the strong source); Kindle + the file backfills are
strictly additive follow-ups behind the same `BooksEnabled()` gate.

---

## 3. Hardcover connector + matching engine

A NEW package `internal/hardcover` (sibling of `internal/github` — a self-contained
external-API connector). It is the **push** side: it reads `reading_items`, resolves
each to a Hardcover `book_id`+`edition_id`, and mirrors reading state out. It writes
to `reading_items` ONLY the match-cache + sync-bookkeeping columns (§1.1).

### 3.1 Token store (per-user secret)

Hardcover auth is a user-pasted **bearer token** (from account settings) that
**expires yearly + resets every Jan 1**. Store it exactly like the Amazon device
credential / github token:

- Migration `00062_hardcover_token.sql`:
  `ALTER users ADD encrypted_hardcover_key bytea, hardcover_key_status text,
  hardcover_key_checked_at timestamptz` (additive).
- `internal/db/hardcover_token.go` — copy `github_token.go` shape
  (Set/GetInfo/Get/List/Clear), seal via `internal/auth.Encrypt`/`Decrypt`.
- **Register in `internal/domains/registry.go`:** add
  `{Domain:"hardcover", Table:"users", Column:"encrypted_hardcover_key",
  KeyColumn:"username"}` to `encryptedColumns` and a `backupColumns` entry — so key
  rotation (`cmd/boomtime/rotate.go`) and the DB backup pick it up **automatically**.
  This is the whole point of that registry; missing it = stranded-on-rotation.
- `hardcover_key_status`: `valid | invalid` (flip to `invalid` on a 401, prompt a
  re-paste in the UI — the Jan-1 reset makes this a routine event, build it into the
  UX).

### 3.2 GraphQL client

`internal/hardcover/client.go`: a thin POST client against
`https://api.hardcover.app/v1/graphql` (Hasura). `Authorization: Bearer <token>`.
Every response is HTTP 200 even on error → **check the mutation-level `errors`
field** in the JSON body, not just the status code. Map:
- transport/`errors` with an auth message, or a real HTTP 401 → `ErrBadToken`
  (flip `hardcover_key_status=invalid`).
- HTTP 429 or rate-limit `errors` → `ErrRateLimited` (return it so the JOB retries
  with backoff — same contract as `github.ErrRateLimited`).

**Throttle < 60 req/min** (≈ token-bucket 1/s). The connector holds a limiter and
paces every call; the matching ladder is the expensive part, which is exactly why we
cache (§3.4).

### 3.3 The match ladder (matching engine)

Every book arrives with a different identity token → resolve to a Hardcover
`book_id`+`edition_id`, **stop at the first hit**, cache the result. Order:

1. **Audible ASIN** → `editions(where:{asin:{_eq:$asin}}){ id book_id reading_format_id }`
   (audio edition). `match_method='asin'`.
2. **Kindle/print ASIN** (`reading_items.amazon_asin`) → same `editions(where:{asin})`
   (ebook edition). `match_method='asin'`.
3. **ISBN-13** (`reading_items.isbn`) → `editions(where:{isbn_13:{_eq:$isbn}})`.
   `match_method='isbn13'`.
4. **Fallback fuzzy** → `search(query:"<title> <author>", query_type:"Book")`
   (Typesense — server-side `_like`/regex is disabled, so this is the only fuzzy
   path). Score candidates locally, pick a `book_id`, then choose an edition via
   `editions(where:{book_id:{_eq}})` filtered by `reading_format_id`
   (1 physical / 2 audio / 4 ebook — pick to match `source`). `match_method='search'`,
   set `match_confidence` from your score.
5. **No confident match → leave `match_method='nomatch'`** and surface it in a
   manual-review list (the admin Books diagnostics page). **Never guess-push.**

### 3.4 Match caching

Write the resolved `hardcover_book_id`, `hardcover_edition_id`, `match_method`,
`match_confidence` onto the `reading_items` row (§1.1). The periodic push then only
*writes* against known IDs — it never re-runs the ladder for an already-matched row,
which protects the 60 req/min budget and avoids re-fuzzing. Re-match only when the
row has no `hardcover_book_id`, or `match_method='nomatch'` and new identity fields
appeared, or an operator forces it.

### 3.5 The push

For each matched row whose `reading_state` changed since `hardcover_synced_at`:

- **Status upsert:** `insert_user_book(object:{ book_id, status_id, edition_id })`.
  `status_id` map from `reading_items.status`: `1 want · 2 reading · 3 read ·
  4 paused · 5 dnf`. `reading_format_id`: `1 physical · 2 audio · 4 ebook` (from
  `source`). Capture the returned `user_book.id` → `hardcover_user_book_id`.
- **Progress + dates:** `insert_user_book_read` (first time) /
  `update_user_book_read` (subsequent) with
  `{ progress_pages, started_at, finished_at }`. Audio has no pages → push
  `progress_pages` from `percent_complete × runtime`-derived pages, or omit and rely
  on status + dates (decide per DJ's open question in the research doc; default:
  status + dates + progress, one-way push, boomtime overwrites Hardcover).
- On success set `hardcover_synced_at=now()`. On `ErrRateLimited`/`ErrBadToken`,
  return the error so the job retries / prompts re-auth; leave `hardcover_synced_at`
  unchanged so the row re-pushes next run.

Reference for exact mutation arg shapes: `billiam/hardcoverapp.koplugin`. No bulk API
— iterate the user's own `reading_items` in `updated`-order.

---

## 4. Finished-detection → events

The forward sync is where a **newly finished** book is detected and turned into a
notification. Mechanism, precisely:

1. Before writing the finished-sweep results, load the current `finished` flags for
   the owner's rows the sweep touches (or read them inside the upsert path). The
   sweep (§2.2b) returns `{asin, event_timestamp, is_marked_as_finished}`.
2. For each swept ASIN where **stored `finished` was `false` and the sweep says
   `is_marked_as_finished=true`** → this is a *transition*. Update the row
   (`finished=true`, `finished_at=event_timestamp`, `status='read'`) **and** collect
   a `BookFinished` event.
3. After the upserts commit, publish each collected event:
   `notify.Publish(ctx, notify.BookFinished{Owner: owner, Title: it.Title,
   ASIN: it.ASIN, FinishedAt: ts})`.

Why diff against the **stored** flag (not "did the sweep list it"): the all-time
sweep lists *every* finished book every time, so "present in sweep" is not "newly
finished". Only the `false→true` edge is new. This makes the detection idempotent —
a re-run of forward sync emits nothing because the rows are already `finished=true`.
The **backfill** deliberately does NOT emit (it would fire hundreds of toasts for
historical finishes); guard with a flag on the sweep call
(`emitEvents bool` — true only in Forward).

Edge cases:
- A book finished then *un*-finished (`is_marked_as_finished` back to false): update
  the flag, no event.
- Kindle "finished" is inferred (`%≈100`) with no reliable date — treat a
  `progress_percent` crossing ~99–100 as the transition for `source='kindle'`, same
  `false→true` edge rule.

---

## 5. Notification subsystem — `internal/notify`

Today there is ONE push channel: `internal/jobsevents.Hub` + `/api/v1/jobs/ws` +
`web/src/features/jobs/useJobNotifications.ts`. It is **job-terminal-event-specific**
(`jobs.JobEvent = {id,kind,owner,status,error}`) and lives inside the jobs subsystem.
Book-finished is not a job-terminal event — a single forward-sync job finishes once
but may have finished *three* books. So we generalize.

### 5.1 Generalize the hub → a domain-agnostic per-user event bus

Create `internal/notify` by **lifting the fan-out mechanism** out of `jobsevents`
(the `Hub` in `hub.go` is already generic: `subs map[owner]set<chan>`,
non-blocking `Notify`, `Subscribe(owner) (<-chan, cancel)`). Change only the payload
type from `jobs.JobEvent` to a general envelope:

```go
// internal/notify/notify.go
type Event struct {
    Type  string          `json:"type"`  // "book.finished" | "job.done" | "job.failed" | …
    Owner string          `json:"owner"` // dropped if "" (system events have no user to toast)
    Title string          `json:"title"` // toast title, e.g. "Finished a book"
    Body  string          `json:"body"`  // toast body,  e.g. "You finished “Dune”."
    Data  json.RawMessage `json:"data"`  // type-specific payload the FE may use (asin, jobId, …)
}

type Bus struct { /* same mu/subs internals as jobsevents.Hub */ }
func NewBus() *Bus
func (b *Bus) Publish(ev Event)                                  // fan-out, non-blocking, drops ""-owner
func (b *Bus) Subscribe(owner string) (<-chan Event, func())     // buffered chan + unsubscribe
```

Typed constructors keep call-sites clean and self-documenting:

```go
// internal/notify/events.go
func BookFinished(owner, title, asin string, at time.Time) Event {
    data, _ := json.Marshal(map[string]any{"asin": asin, "finishedAt": at})
    return Event{Type: "book.finished", Owner: owner,
        Title: "Finished a book", Body: "You finished “" + title + "”.", Data: data}
}
```

Domains publish through the injected bus: the audiobooks/books `Service` gets a
`Notify *notify.Bus` field (nil-safe: a nil bus = no push, exactly like
`jobs.Notifier` today) and calls `s.Notify.Publish(notify.BookFinished(...))`.

### 5.2 ONE WebSocket channel

Collapse to a single stream `/api/v1/notify/ws` (cookie-authed, same auth as
`/api/v1/jobs/ws`). The handler `Subscribe`s the authed user on the `Bus` and pumps
`Event`s as JSON. **Relationship to the existing jobs WS:** make `jobsevents` a thin
adapter — its `Notify(jobs.JobEvent)` maps the job event into a `notify.Event`
(`Type:"job.done"/"job.failed"`, `Title` from the kind label, `Data` = the job id)
and calls `Bus.Publish`. Concretely:

- `provider.SetNotifier(...)` in `main.go` still receives `jobs.JobEvent`s; the
  adapter it's given wraps the shared `*notify.Bus`. No change to the jobs package's
  `Notifier` interface — we just implement it with a translator.
- `/api/v1/jobs/ws` can either stay (back-compat) or be redirected to
  `/api/v1/notify/ws`; the FE consolidation (below) makes the single endpoint the
  target. Recommended: land `/api/v1/notify/ws` first, migrate the FE, then retire
  the jobs-only endpoint in a follow-up (keeps `main` deployable throughout).

### 5.3 FE — one `useNotifications` hook → sonner toast

Generalize `useJobNotifications.ts` into `web/src/features/notify/useNotifications.ts`:
subscribe once (mounted in `AppShell`), reconnect on drop (keep the existing retry
logic), and toast on every `Event` using `Type`→handler routing:

```ts
interface NotifyEvent { type: string; owner: string; title: string; body: string; data?: unknown }
// book.finished → toast.success(title, {description: body}); job.failed → toast.error(...); etc.
```

Unknown `type`s fall back to a generic `toast(title, {description: body})` — so a new
domain's events toast correctly with **zero FE changes** (the server already fills
`title`/`body`). This is the payoff of the envelope: domains add event *types*
without touching the FE hook.

### 5.4 Why this over "just reuse jobsevents"

Reusing the jobs hub would force every notification to masquerade as a job-terminal
event (the `ty='workout'`-through-`heartbeats` anti-pattern the spike warns about).
A domain-agnostic `notify.Bus` with a typed envelope is the right generalization: the
jobs subsystem becomes *one publisher among many*, and book-finished, (future)
import-complete, hardcover-reauth-needed, etc. all ride one socket and one FE hook.

---

## 6. Job wiring, cadence, gates, fusion hook

### 6.1 Job kinds (catalyst-go-jobs)

Reuse the existing kinds where present, add the rest. All are `internal/jobs`
`Registry.Register(kind, HandlerFunc)` + `Scheduler.Register(ctx, kind, interval)`,
leader-singleton via the DB (safe to run on every server role).

| kind (const) | package | mode | what it does |
|---|---|---|---|
| `audiobooks-audible-sync` (`audiobooks.AudibleSyncKind`, exists) | audiobooks | Forward | fan over `ListUsersWithAmazonDevice` → `Service.Forward` |
| `audiobooks-audible-backfill` (new) | audiobooks | Backfill | one-shot per user, enqueued on connect / from admin |
| `books-kindle-sync` (`books.KindleSyncKind`, exists) | books | Forward | Kindle whispersync delta (follow-up) |
| `hardcover-push` (new, `hardcover.PushKind`) | hardcover | push | fan over `ListUsersWithHardcoverKey` → match + push changed rows |

### 6.2 Wiring in `cmd/boomtime/main.go`

Follow the **github pattern verbatim** (main.go ~L394–535): the handler is wired
where the `Service` + `db.DB` are in scope so the jobs package stays domain-free.
Gate the whole block on `cfg.BooksEnabled()`:

```go
if cfg.BooksEnabled() {
    az := amazon.NewStore(database)
    audio := audiobooks.New(database, az, logger)
    audio.Notify = notifyBus // §5

    jobReg.Register(audiobooks.AudibleSyncKind, jobs.HandlerFunc(func(jctx context.Context, _ jobs.Job) error {
        users, err := database.ListUsersWithAmazonDevice(jctx)   // NEW db helper, mirror ListUsersWithGithubToken
        if err != nil { return err }
        for _, u := range users {
            if serr := audio.Forward(jctx, u); serr != nil {
                if errors.Is(serr, amazon.ErrRateLimited) { return fmt.Errorf("audible rate limited at %q: %w", u, serr) }
                logger.Warn("audible forward: user sync failed", "user", u, "err", serr)
            }
        }
        return nil
    }))
    // backfill kind: per-user payload {owner}; enqueued on Connect-Amazon success.
    jobReg.Register(audiobooks.AudibleBackfillKind, jobs.HandlerFunc(func(jctx context.Context, job jobs.Job) error {
        return audio.Backfill(jctx, job.Owner)
    }))
    // hardcover-push … same shape over ListUsersWithHardcoverKey
}
```

Scheduler (in the `cfg.IsServerRole()` block, next to the github schedule):

```go
if cfg.IsServerRole() && cfg.BooksEnabled() {
    _ = sched.Register(ctx, audiobooks.AudibleSyncKind, cfg.AudibleSyncInterval)
    _ = sched.Register(ctx, hardcover.PushKind,         cfg.HardcoverPushInterval)
    // backfill is NOT scheduled — it's enqueued on demand.
}
```

### 6.3 Feature gates + cadence config

- Master gate exists: `cfg.BooksEnabled()` → `BOOM_FEATURE_BOOKS` (default false,
  `internal/config/config.go`). Everything above is dark until flipped.
- Add interval fields mirroring `GithubStatsRefreshInterval`
  (`parseJobInterval(getEnv("BOOM_…_INTERVAL", default))`):
  - `AudibleSyncInterval` — `BOOM_AUDIBLE_SYNC_INTERVAL`, default `6h` (Audible data
    changes slowly; nightly-ish is plenty).
  - `HardcoverPushInterval` — `BOOM_HARDCOVER_PUSH_INTERVAL`, default `1h` (pushes
    are cheap and cached; keep the mirror fresh).
  - Optional `KindleSyncInterval` — default `30m` (whispersync is near-real-time).
- Add `AudibleSyncEnabled()` / `HardcoverPushEnabled()` helpers folding
  `BooksEnabled() && interval > 0`, mirroring `GithubStatsRefreshEnabled()`, and gate
  each `sched.Register` on them.

### 6.4 The fusion-layer hook

The whole point (per the spike §5): overlay reading-time on the coding calendar,
replacing the single hardcoded `StatsPayload.GithubDailyTotal` overlay. Concretely:

- **Source of the overlay series:** `reading_activity` (§1.2). A day's coding seconds
  come from `hb_rollup_daily`; a day's listening seconds come from
  `reading_activity` (`source='audible'`, prefer `granularity='day'`, fall back to
  spreading a `month` bucket).
- **The hook:** when the P2 metric registry (`internal/core/metric.Series` +
  `DailySeries(owner,key,t0,t1)`) lands, the books/audiobooks domain implements
  `DailySeries("audiobooks","listening-seconds",t0,t1)` (and `"pages-read"` for
  Kindle) by querying `ListReadingActivity`. The fusion layer requests any registered
  series over a shared window and overlays arbitrary pairs — so
  `overlays:[{domain:"audiobooks",key:"listening-seconds"}]` renders reading-time on
  the same calendar as `waka:coding-seconds`. **No heartbeat analogue, no writing
  into core** — `reading_activity` is the domain's own pre-aggregated rollup, exactly
  the model the spike recommends over a universal fact table.
- **Until P2:** the books dashboard widget is `target:"fe-only"` self-fetch (like
  github/wellness) reading `ListReadingItems`/`ListReadingActivity` via a
  `web/src/features/books/` react-query context — full parity with the other real
  domains, zero core edits. The fusion overlay is the *first real* cross-domain
  correlation and is the payoff that justifies the `reading_activity` table now.

---

## 7. Build/verify checklist (per push-safety)

1. `CGO_ENABLED=0 go build ./...` clean after each additive migration + DAL + wiring.
2. `yarn typecheck` clean (FE feature folder is additive, gated on the same flag).
3. Migrations are `ADD COLUMN`/`CREATE TABLE IF NOT EXISTS` only — no ALTER/DROP of
   existing objects; `reading_items` `00058` stays untouched.
4. New encrypted secret (`encrypted_hardcover_key`) registered in
   `internal/domains/registry.go` → rotation + backup pick it up. New tables added to
   the backup table set.
5. Every new job kind + schedule is inside a `cfg.BooksEnabled()` (and interval)
   guard; `main` with the flag off behaves exactly as today.
6. Do NOT `git commit`/`push` — hand off changed files + validation.

---

## 8. Implementation order (bead-able, each independently shippable)

1. **`reading_items` metadata columns** (`00059`) + struct/upsert extension +
   populate from the existing `FetchLibrary` (loop all pages, add `response_groups`).
2. **`reading_activity`** (`00060`) + DAL + Audible aggregates loop; **`sync_state`**
   (`00061`) + `Backfill`/`Forward` split on the audiobooks Service.
3. **Finished sweep** into Forward + the `false→true` diff (§4) — but publish through
   a temporary logger until §5 lands.
4. **`internal/notify`** bus + `/api/v1/notify/ws` + `useNotifications`; adapt
   `jobsevents` onto it; wire `BookFinished` publish.
5. **`internal/hardcover`** token store (`00062` + registry entry) + client + match
   ladder + cache + push; `hardcover-push` kind + schedule.
6. **FE `features/books/`** dashboard widget (fe-only) + Connect-Amazon already
   exists; add the reading list + activity views.
7. **Fusion overlay** (after core/metric P2): `DailySeries` impl + overlay request.
8. **Kindle + file backfills** (whispersync, Goodreads CSV/RSS, Amazon export) —
   follow-ups, same tables, same gate.

Steps 1–6 deliver a fully working Audible→boomtime→Hardcover pipeline with
finished-toasts; 7 delivers the fusion payoff; 8 rounds out Kindle.
