# catalyst-books Domain Architecture

> **Status:** architecture-of-record (2026-08). Higher-altitude companion to
> [`catalyst-books-sync-architecture.md`](./catalyst-books-sync-architecture.md).
> **Scope:** the reading/books *domain* — how boomtime bridges Kindle + Audible
> into a fused reading model, the **stable external identifiers** that make sync
> reconcilable, the **match engine**, **bidirectional sync** (outbound today,
> inbound designed), the **per-domain reading-heartbeat model**, and the
> reverse-engineered **Kindle API map** (documented here first — it is not yet in
> code).
> **Reading conventions.** Every symbol in `code font` (`reading_items`,
> `amazon.SignedGet`, `hardcover.Match`, `AudibleSyncKind`) is a real symbol on
> the current tree; file paths are cited inline so each claim is checkable.

**Relationship to the sync doc.** The companion doc is the *recipe*: the exact
Audible endpoints, response groups, paging, cursor bookkeeping, the notification
subsystem, and the phased implementation beads. **This** doc is the *domain +
identity + bidi + Kindle* layer above it. Where they touch (the match ladder, the
Hardcover push, `reading_activity`), this doc states the architecture and links
down; it does not re-derive the sync recipes. Read them together.

---

## 1. Overview — boomtime as the bridge

boomtime is not a book catalog. It is a **fusion hub** that sits between two
Amazon-owned *sources* it can only read, and one community *datasource*
(Hardcover) it can read **and** write. The design principle is: **store the
derived reading data and the groupable dimensions we need — never mirror
Hardcover's catalog.**

```mermaid
flowchart LR
    subgraph sources["SOURCES (read-only, Amazon device cred)"]
        KIN["Kindle<br/>ebooks · whispersync LPR"]
        AUD["Audible<br/>audiobooks · library + stats"]
    end

    subgraph boom["boomtime — FUSION HUB (the data we DO store)"]
        direction TB
        AMZ["internal/amazon<br/>DeviceCredential · Sign / SignedGet"]
        DOM["internal/domains/{audiobooks, books}<br/>Backfill + Forward sweeps"]
        RI[("reading_items<br/>current-state per book<br/>+ hardcover_* linkage")]
        RA[("reading_activity<br/>time-series buckets")]
        BSS[("book_sync_state<br/>forward cursors")]
        Q["internal/query<br/>reading domain DSL"]
    end

    subgraph hc["DATASOURCE (read + write, GraphQL)"]
        HC["Hardcover<br/>user_books · editions · search"]
    end

    KIN --> AMZ
    AUD --> AMZ
    AMZ --> DOM
    DOM --> RI
    DOM --> RA
    DOM --> BSS
    RI --> Q
    RA --> Q
    DOM -- "outbound push (finished edge)" --> HC
    HC -. "inbound pull (designed)" .-> DOM
    HC -- "ASIN→metadata resolution" --> DOM
```

**What we store vs. what we resolve on demand:**

- **Store (derived + dimensional):** per-book current state (`reading_items`),
  the time-series of listening-seconds / pages (`reading_activity`), forward-sync
  cursors (`book_sync_state`), and the **groupable dimensions** the query DSL
  needs — `series`, `authors`, `genres` (flattened `category_ladders`), `status`,
  `source`. These are the things a dashboard groups/filters on and that no single
  source will re-serve cheaply.
- **Do NOT store (fetch on demand):** Hardcover's catalog. `00063_hardcover_link`
  is explicit — *"Hardcover is a bidirectional external DATASOURCE, not a thing we
  mirror. We persist only the MINIMAL linkage needed to reconcile ... No local
  shelf copy."* Book details beyond our own dimensions are fetched from Hardcover
  when needed via `internal/hardcover`.

The two ingestion domains own no auth and no push — those are shared plumbing
(`internal/amazon`, `internal/hardcover`), so a third source (print, Goodreads
import) or a second push target reuses the same seams.

---

## 2. Stable external identifiers — the reconciliation keys

Sync is only safe if every book is anchored to identifiers that are **stable
across sources and across runs**. A book, here, is *a set of external ids*; sync
reconciles on them and **never** re-fuzzes an id it has already resolved.

```mermaid
flowchart TD
    subgraph book["one WORK, many external ids"]
        AASIN["Audible ASIN<br/>(reading_items.external_id)"]
        KASIN["Kindle/print ASIN<br/>(reading_items.amazon_asin)"]
        ISBN["ISBN-13<br/>(reading_items.isbn — often empty)"]
        HCB["Hardcover book_id"]
        HCE["Hardcover edition_id"]
    end

    AASIN -->|"match ladder rung 1"| HCE
    KASIN -->|"rung 1 (ebook edition)"| HCE
    ISBN  -->|"rung 2"| HCE
    HCE --> HCB
    HCB -->|"cached in linkage cols<br/>hardcover_book_id / _edition_id"| DONE["never re-fuzzed"]
```

### 2.1 The keys, per source

| Key | Column(s) | Audible | Kindle | Print | Hardcover | Notes |
|---|---|---|---|---|---|---|
| **Amazon ASIN** | `reading_items.external_id` (uniqueness key), also `amazon_asin` | ✅ audio ASIN (the reliable id) | ✅ ebook ASIN | rarely | resolvable via `editions.asin` | The primary key everywhere Amazon touches. `UNIQUE (owner, source, external_id)` (`00058`). |
| **ISBN-13** | `reading_items.isbn` | ❌ **none** — audiobooks have no ISBN | sometimes | ✅ | `editions.isbn_13` | Audible has NO ISBN, so ASIN is the only reliable audio id. Kindle/print *may* carry one. |
| **Hardcover `book_id`** | `reading_items.hardcover_book_id` | resolved once | resolved once | resolved once | native | The work-level id; the diff basis for pull. |
| **Hardcover `edition_id`** | `reading_items.hardcover_edition_id` | resolved once | resolved once | resolved once | native | The format-specific edition (audio vs ebook vs physical). |

The `amazon_asin` column is the **cross-format join key**: an Audible edition and
its Kindle sibling can be recognized as the same work later because the library
item carries both the audio `asin` and the print/kindle `amazon_asin`
(`internal/domains/audiobooks/audiobooks.go` maps both onto the row).

### 2.2 Resolve-once, cache-forever

The Hardcover ids are resolved **once** by the match engine (§3) and cached in the
`00063_hardcover_link` columns — `hardcover_book_id`, `hardcover_edition_id`,
`hardcover_match_confidence`, `hardcover_matched_at`. A partial index
(`reading_items_hardcover_book_idx ... WHERE hardcover_book_id IS NOT NULL`) lets
the reconcile passes scan only already-linked rows. Re-matching happens only when
a row has no `hardcover_book_id`, or its confidence was `none`/`search` and new
identity fields appeared, or an operator forces it — this is what protects
Hardcover's < 60 req/min budget.

---

## 3. The match engine

One engine (`internal/hardcover/match.go`) powers **both** the outbound Hardcover
push **and** Kindle metadata resolution (§6). It walks a ladder from strongest to
weakest identity and **stops at the first confident hit**:

```mermaid
flowchart TD
    START["MatchInput{ASIN, ISBN13, Title, Author}"] --> A{ASIN present?}
    A -- yes --> AQ["editions where asin _eq"]
    AQ -- hit --> AR["MatchByASIN · confidence 1.0"]
    A -- no --> B
    AQ -- miss --> B{ISBN-13 present?}
    B -- yes --> BQ["editions where isbn_13 _eq"]
    BQ -- hit --> BR["MatchByISBN13 · confidence 1.0"]
    B -- no --> C
    BQ -- miss --> C{Title present?}
    C -- yes --> CQ["search query_type=Book (Typesense)"]
    CQ --> SC["scoreCandidate: title-token Jaccard<br/>+ author bonus · floor 0.6"]
    SC -- ">= floor" --> CR["MatchBySearch · pickEdition(book_id)"]
    SC -- "< floor" --> NM["MatchNone — manual review, NEVER guess-push"]
    C -- no --> NM
```

Grounding (`internal/hardcover/match.go`, `client.go`):

- **Rung 1 — ASIN** (`MatchByASIN`): `editionByField(ctx, "asin", asin)`. Covers
  BOTH the Audible audio ASIN and the Kindle/print `amazon_asin` — the caller
  passes whichever it has via `MatchInput.ASIN`. Exact-equality on
  `editions.asin`, confidence `1.0`.
- **Rung 2 — ISBN-13** (`MatchByISBN13`): `editionByField(ctx, "isbn_13", isbn)`,
  after `normalizeISBN` strips hyphens/spaces to bare digits.
- **Rung 3 — fuzzy search** (`MatchBySearch`): Hardcover's server-side
  `_like`/regex is disabled, so the only fuzzy path is the Typesense-backed
  `search(query, query_type:"Book")`. Candidates are scored **locally**
  (`scoreCandidate` — title-token Jaccard + a 0.15 author-match bonus) and only
  the best above a **0.6 floor** is accepted. `pickEdition(book_id)` then chooses
  an edition (a status-only push off `book_id` is still possible when a book lists
  no editions).
- **Rung 4 — no match** (`MatchNone`): IDs stay zero. The caller records it and
  **never guess-pushes** — a wrong push is worse than a miss. These surface in the
  admin manual-review list.

The `field` argument to `editionByField` is a compile-time constant from the
ladder (never user input), so the interpolated GraphQL is injection-safe by
construction. Every rung is at most one GraphQL call, and later rungs run only on
earlier misses — the ladder is throttle-friendly by design.

The resolved `MatchResult{BookID, EditionID, Method, Confidence}` is written to
the linkage columns and the format is chosen from the row's `source` via the
`reading_format_id` map (`FormatPhysical=1 · FormatAudio=2 · FormatEbook=4`,
`push.go`).

---

## 4. Bidirectional sync

Hardcover is a **read + write** datasource. Today the **outbound** half is built
and dry-run-gated; the **inbound** half is designed against the linkage columns
that already exist (`00063`) for exactly this purpose.

```mermaid
sequenceDiagram
    autonumber
    participant AUD as Audible
    participant SVC as audiobooks.Service
    participant DB as reading_items
    participant JOB as hardcover-push queue
    participant HC as Hardcover GraphQL

    Note over SVC,HC: OUTBOUND (built) — finished false→true edge
    AUD->>SVC: SyncUser forward sweep
    SVC->>DB: MarkReadingItemFinished (false→true)
    DB-->>SVC: transitioned=true + FinishedReadingItem meta
    SVC->>JOB: Enqueue HardcoverPushPayload (capped, 1 concurrent)
    JOB->>SVC: RunHardcoverPush
    SVC->>HC: Match ladder (reads pass through even in dry-run)
    alt dry-run ON (default)
        SVC-->>SVC: log + toast "would mark read" · NO write
    else dry-run OFF (opt-in)
        SVC->>HC: insert_user_book (status_id=read)
        SVC->>HC: insert/update_user_book_read (finished_at)
        SVC->>DB: set hardcover_pushed_at = now()
    end

    Note over HC,DB: INBOUND (designed) — pull + reconcile
    HC-->>SVC: user_books (book_id, status, updated_at)
    SVC->>DB: reconcile on hardcover_book_id (stable id)
    SVC-->>SVC: if remote.updated_at <= hardcover_pushed_at → echo, skip
    SVC->>DB: else last-writer-wins → update status/dates<br/>set hardcover_remote_updated_at
```

### 4.1 Outbound (built, dry-run-safe)

The forward sweep detects the **finished `false→true` edge**
(`MarkReadingItemFinished` in `internal/db/reading_items.go` returns
`(transitioned, meta, found)`), then `announceFinished` publishes a
`book.finished` notification and calls `mirrorFinishedToHardcover`. With an
`Enqueuer` wired the push is marshalled into a `HardcoverPushPayload` and routed
onto the `HardcoverPushKind` queue (capped at concurrency 1 — Hardcover's rate
limit is a *global* resource); otherwise it runs inline.

`RunHardcoverPush` (`audiobooks.go`) runs the match ladder, then:
- **Dry-run ON** (`client.DryRun()`): logs `hardcover DRYRUN: would push finished
  book` and publishes a `hardcover.dryrun` toast previewing exactly what *would*
  be written (bookId, editionId, status `read`, `finishedAt`) — then returns
  before any mutation.
- **Dry-run OFF:** `UpsertUserBook(bookID, editionID, StatusRead, FormatAudio)` →
  `UpsertRead(userBookID, {FinishedAt, EditionID, ReadingFormatID})`. On
  `ErrBadToken`, `hardcoverError` flips the stored key status to `invalid` so the
  UI prompts a re-paste.

Only the finish is mirrored outbound today; boomtime is the writer, so this is a
one-way overwrite of that field.

### 4.2 Inbound (designed) — the columns are already there

`00063_hardcover_link` provisions two provenance timestamps specifically for the
pull:

- **`hardcover_pushed_at`** — the last write *we* made out. **Echo-suppression:** a
  pulled `user_book` whose Hardcover `updated_at` is `<= hardcover_pushed_at` is
  our own write bouncing back — skip it.
- **`hardcover_remote_updated_at`** — Hardcover's `updated_at` at last sight. The
  **delta cursor** and the **last-writer-wins** basis: if the remote row is newer
  than both our push and our last-seen remote stamp, Hardcover wins and we update
  `reading_items` (status/dates) and advance `hardcover_remote_updated_at`.

Reconciliation keys on the **stable** `hardcover_book_id` (never a re-fuzz), so a
pulled shelf change lands on the right local row deterministically. The pull is
**dry-run-safe by the same gate** — it is a read (`user_books` query passes
through) plus a local write into our own siloed table; it never mutates
Hardcover. Building it is the main open item (§8): grab a real token, verify the
`user_books` shape, implement the pull + reconcile.

---

## 4A. Status curation + three-way sync (Kindle/Audible ⇄ boomtime ⇄ Hardcover)

> **This is THE reference for the three-way merge.** Read it before touching
> status, rating, or finished-date sync. It documents the model implemented across
> migration `00069`, `internal/db/reading_items.go`, `internal/query/domains.go`,
> and `internal/hardcover/{push,pull}.go`. The plan of record is
> `gaka-books` (Hardcover status/curation override + bidirectional LWW).

**The problem.** Three systems disagree about a book's status. **Amazon (Kindle +
Audible) is a read-only device source — we NEVER sync TO it.** It is also too
permissive: a stalled book keeps reporting `reading`, and Amazon has no way to
express **DNF** or **Paused**. The user wants to override the status that maps to
Hardcover, push that override out, and — when the shelf changes *in* Hardcover —
pull it back, latest-value-wins. Same for **rating** and **finished-date**.

**The trap this avoids.** Amazon re-derives status on *every* sync with a *fresh*
timestamp. A naive row-level last-write-wins would therefore let the next Amazon
sync clobber a user's DNF override — Amazon's clock is always "newer." The fix is
a **two-layer model**: ingest only ever writes the *derived* layer; overrides live
in a separate *override* layer; and LWW arbitrates **only** between the two
curation writers (user and Hardcover) — Amazon is not a participant.

### 4A.1 Field-ownership + sync-direction matrix

| Field | Source of truth | Direction(s) | Conflict rule |
|---|---|---|---|
| **status** | Amazon derives; user/Hardcover curate | Amazon → boomtime (derived, one-way); boomtime ⇄ Hardcover (effective, bidirectional) | Effective = `override ?? derived`. Between user and Hardcover: **LWW** on `curation_updated_at` vs `hardcover_remote_updated_at`. Amazon never enters LWW. |
| **progress / position** (LPR, listening-seconds) | Amazon | Amazon → boomtime, **one-way** | Amazon-owned; feeds `reading_activity` + the derivation heuristic. Never pushed, never overridden. |
| **finished / finished_at** | Amazon derives a real finish; user/Hardcover override the date | Amazon → boomtime (derived); boomtime ⇄ Hardcover (effective) | Effective `finished_at = COALESCE(finished_at_override, finished_at)`. A real Amazon finish may **promote** an override → `read` once (see 4A.5a). |
| **rating** | Hardcover-owned inbound; user may override | Hardcover → boomtime (inbound); boomtime → Hardcover (on user override) | Effective `rating = COALESCE(rating_override, rating)`. Pull adopts remote rating into the override layer under LWW. |
| **canonical metadata** (title, author, cover, series, genre) | Hardcover (resolved via match ladder) | Hardcover → boomtime, **inbound only** | Hardcover-owned; boomtime never writes metadata back. See §3. |
| **existence** (which books) | Amazon (Kindle + Audible libraries) | Amazon → boomtime, **one-way** | Amazon is the roster. boomtime never creates books in Amazon or Hardcover beyond linking. |

**One-line read:** Amazon is a read-only *source* → boomtime is the *hub* →
Hardcover is the one *read+write* datasource. Effective **status** and
**finished_at** round-trip boomtime⇄Hardcover under LWW; **rating** and
**canonical metadata** are Hardcover-owned inbound; **progress/existence** are
Amazon-owned inbound. Nothing ever flows back to Kindle or Audible.

### 4A.2 Two-layer status model — derived vs override

Every curated field is stored as **two columns**: a *derived* column that ingest
recomputes each sync, and an *override* column that only the two curation writers
touch.

| Layer | Columns | Written by | Recomputed each sync? |
|---|---|---|---|
| **derived** | `status`, `finished`, `finished_at`, `rating` | Amazon ingest (`UpsertReadingItem`) | **Yes** — clobbered every sync, by design |
| **override** | `status_override`, `finished_at_override`, `rating_override`, plus the row-level LWW stamp `curation_updated_at` | ONLY the PATCH endpoint (source=user) or the Hardcover pull LWW branch (source=hardcover) | **No** — sticky |

```
effective_status      = COALESCE(status_override,      status)
effective_finished_at = COALESCE(finished_at_override, finished_at)
effective_rating      = COALESCE(rating_override,      rating)
```

Migration `00069` adds the four override columns (all NULL, no backfill).
`UpsertReadingItem`'s column list is **deliberately unchanged** — overrides are
not in the upsert, so ingest *structurally cannot* clobber them. That invariant is
the whole safety story; keep the comment on the upsert and never add an override
column to it.

**Why Amazon is NOT a LWW participant.** LWW compares two timestamps and keeps the
newer value. Amazon re-derives with `now()` on every sync, so it would *always*
win a timestamp race and erase a DNF/Paused override on the next poll — the exact
trap. Therefore ingest writes only the derived layer, and **LWW runs strictly
between `curation_updated_at` (user's last override) and
`hardcover_remote_updated_at` (Hardcover's `updated_at`)**. Amazon's fresh
timestamps never touch that comparison.

### 4A.3 Canonical 1:1 status vocabulary

One vocabulary, 1:1 with Hardcover's enum — no lossy remap:

| boomtime | id | Hardcover `status_id` | Produced by |
|---|---|---|---|
| `want` | 1 | Want | Amazon derivation, user, Hardcover |
| `reading` | 2 | Reading (Currently Reading) | Amazon derivation, user, Hardcover |
| `read` | 3 | Read | Amazon derivation, user, Hardcover |
| `paused` | 4 | Paused | **user or Hardcover override only** |
| `dnf` | 5 | Did Not Finish | **user or Hardcover override only** |

The enum lives in `internal/hardcover/push.go` (`StatusWant=1 … StatusDNF=5`) and
maps back via `pull.go` `StatusString`. **The rule:** *filter labels == group
values == pill labels == Hardcover status names.* One set, everywhere — the Status
filter, the group-by axis values, the FE `STATUS_META` pills, and Hardcover all
speak `want / reading / read / paused / dnf` (plus `all` on the filter). This
retires the old rot where the filter said `All / Reading / Finished / Want` (a
mislabel of `read`) while grouping showed raw `want / reading / read` — filter and
group-by used to disagree; now they can't. **Amazon only ever produces
`want / reading / read`;** `paused` and `dnf` come *only* from a user or Hardcover
override.

### 4A.4 Derivation heuristic + the two group-by axes

Amazon exposes no explicit shelf state we trust for "done," so the derived
`status` is computed from **completion percentage**:

- **> 95% (audio)** or **100% (kindle)** ⇒ `read` / finished.
- Otherwise `reading` if opened (an LPR exists), else `want`.

Grounded in `internal/domains/books` ingest (`statusFromPercent`) and
`internal/domains/audiobooks` (`toReadingItem`). Because status now has two
meanings, the query DSL exposes **two group-by axes**:

- **`status`** (default) = **effective** `COALESCE(status_override, status)` — what
  the user/Hardcover curated. This is what the filter, rollups, goals, and pills
  read, so a DNF/Paused book leaves the "reading" set everywhere.
- **`statusDerived`** = the raw Amazon column `status`, untouched — "what the source
  computed." Group by this to see the heuristic's `want / reading / read` buckets
  before overrides.

Both axes are selectable in the explorer. `finished` as a measure counts
effective-read rows: `sum(case when COALESCE(status_override,status)='read' then 1
else 0 end)`. Reading **goals** inherit all of this for free because they run
through `query.Q("reading")` — making the DSL effective makes goals effective.

### 4A.5 The three sync mechanics

**(a) Amazon-finish promotion.** The one place Amazon may touch the override layer.
When a finish sweep (`MarkReadingItemFinished` /
`SetReadingItemFinishedFromInsights`) sees `finished` transition true AND effective
status ≠ `read`, it sets `status_override='read'` + `curation_updated_at=now()`.
This is a deliberate **one-time promotion** — a genuine finish (pct≥100 / an
insights `date_read`) should win over a stale override — and it is **idempotent**:
once effective status is `read`, it's a no-op. It is emphatically *not* a per-sync
clobber; it fires only on the finish edge.

**(b) Echo-suppression via `hardcover_pushed_at`.** On a successful push, stamp
`hardcover_pushed_at=now()` and record the pushed status. The pull's LWW branch
**skips** adopting a remote change whose value equals our own last push — otherwise
our own write would bounce back off Hardcover's `updated_at` and re-import as if a
user had edited it in Hardcover. Reconciliation keys on the stable
`hardcover_book_id`, so the echo lands on the right row deterministically.

**(c) Dry-run-gated writes (`BOOM_HARDCOVER_DRYRUN`, default TRUE).** Every
Hardcover mutation — status, rating, finished-date — flows through the process-wide
dry-run gate in `internal/hardcover/client.go`. With dry-run on (the default), the
client logs `hardcover DRYRUN: write blocked` and returns a simulated success, so
the whole curation loop builds, tests, and runs end-to-end **without touching a
real shelf**. Reads (the pull's `user_books` query) pass through. The user flips
`BOOM_HARDCOVER_DRYRUN=false` only when ready to write for real. **Safe by
default** — see §7.

### 4A.6 Pull LWW branch (inbound arbitration)

`UpdateHardcoverLinkFromPull` (`internal/db/reading_items.go`) carries the
arbitration: when a pulled `user_book` has `hardcover_remote_updated_at >
curation_updated_at` **AND** its status ≠ our last-pushed status (echo-suppressed
via `hardcover_pushed_at`), Hardcover wins → adopt remote status/rating/finished
into the **override** columns and set `curation_updated_at` = the remote time.
Otherwise keep local; the next push reconciles Hardcover. Note the adoption lands
in the *override* layer, not the derived layer — so a subsequent Amazon sync still
can't clobber it.

---

## 5. The reading-heartbeat model (per-domain translation)

boomtime's coding analytics are built on **heartbeats**: sparse editor pings whose
*gaps* are summed into duration. That model is native to coding and **does not
generalize** to reading. The architecture instead makes each domain responsible
for translating its **own** native signal into the common `reading_activity`
shape — one table, one grain (`owner, source, granularity, bucket_date,
listening_seconds, pages`), many translations.

```mermaid
flowchart TD
    subgraph coding["coding (catalyst-waka)"]
        HB["editor heartbeats"] --> GAP["gap-sum between pings<br/>→ attributed seconds"]
    end
    subgraph audible["Audible (audiobooks)"]
        AGG["stats/aggregates<br/>listening-seconds per period"] --> RET["RETROACTIVE<br/>read totals as-is"]
    end
    subgraph kindle["Kindle (books)"]
        LPR["whispersync LPR<br/>last-page-read position"] --> FWD["FORWARD<br/>poll & diff positions<br/>→ reading sessions"]
    end

    GAP --> RASHAPE
    RET --> RASHAPE["common reading_activity shape<br/>(owner, source, granularity, bucket, seconds|pages)"]
    FWD --> RASHAPE
```

| Domain | Native signal | Direction | Translation into `reading_activity` |
|---|---|---|---|
| **coding** | plugin heartbeats | forward | gap-summed between pings → attributed seconds (heartbeat model) |
| **Audible** | `stats/aggregates` listening-seconds per day/month | **retroactive** | Amazon already aggregated it; `sweepAggregates` reads the totals directly into `listening_seconds` buckets. No inference. |
| **Kindle** | whispersync **LPR** (last-page-read) polled over time | **forward** | poll-and-diff: successive LPR positions for an ASIN become reading sessions; the position delta over the interval is the "pages" signal (§6). No native duration exists — it is *reconstructed* from polling. |

This is why `reading_activity` carries **both** `listening_seconds` (audio) and a
nullable `pages` (ebook): the two domains produce structurally different signals
that the fusion layer overlays on one calendar. Audible fills buckets *backward*
from a stats endpoint that already knows the answer; Kindle must fill them
*forward* because Amazon exposes only the current position, never a history —
boomtime *becomes* the history by polling. The companion sync doc details the
Audible aggregates recipe (windows, granularity, cursor advance); the Kindle
forward model is specified in §6 below.

The query DSL (`internal/query/domains.go`) consumes this uniformly: the
`reading` domain exposes `seconds` → `sum(listening_seconds)` over
`reading_activity.bucket_date`, and `books`/`runtime` measures over
`reading_items.finished_at`, grouped by the `source · status · series · author ·
genre · title` dimensions. Canonical grouping keys (e.g. collapsing a series or an
author's variant spellings) ride the shared curation **pin** mechanism
(`internal/curation`, `CurationPin` action) — the same canonical-entity seam the
coding domain uses, not a books-specific one.

### 5.1 Polling economics — the two-level "limit hack" + the persistent monitor

Kindle reading-time is *reconstructed by polling* (§5, §6), and that reconstruction
runs into a hard constraint worth stating loudly so nobody naively fast-polls
everything:

> **Amazon never pushes, and the sidecar is per-book** (`sidecar?type=EBOK&key=<ASIN>`).
> So to learn whether *anyone is reading right now* you must poll **every in-progress
> book, for every user** — cost is `O(books × users × 1/interval)`, all ADP-signed
> calls to `cde-ta-g7g.amazon.com`. That per-book detection sweep is the floor we
> minimize; hammering it is how we'd get throttled or noticed.

**The nuance that makes a cheap poll safe — `creationTime`.** The `kindle.lpr` record
carries `creationTime` = *Amazon's* event time for when the furthest-page-read was
set (not our poll time). So a **coarse** poll never loses total-reading **detection**:
we always learn the current furthest position **and when Amazon last advanced it**.
Fast polling buys exactly **one** thing — **intra-session cadence**: resolving whether
an A→B jump was one continuous session or several bursts with gaps, which is what the
gap-sum session-boundary composition needs. Detection is cheap; only *fine session
shape* is expensive.

**Two-level adaptive polling** (the hack):

| Level | Scope | Interval | Purpose |
|---|---|---|---|
| **L1 — detect** | ALL in-progress books, per user | `T1` (coarse, minutes) | spot which book(s) advanced = actively being read |
| **L2 — capture** | ONLY the active book(s) | `T2` (fine, seconds) | sample the cadence for accurate session boundaries, until idle for gap `G` → drop back to L1 |

This collapses cost from "every book fast, always" to "every book slow + the 1–2
books *actually being read* fast, only while they're read."

**Choosing `T1`/`T2`/`G` — measure, don't guess.** `T2` ≥ the whispersync push cadence
(no point sampling faster than Amazon actually writes the LPR); `G` = the longest gap
seen *within* one real reading session; `T1` = how stale detection may be before it
misses a session *start* — and because `creationTime` backfills the true event time,
a larger `T1` mostly costs toast **latency**, not data. The **admin reading-monitor
panel is the empirical probe** for these: it streams every advance and derives the
observed cadence (min / median advance interval, avg Δlocation, implied sec/location),
then **recommends** `T1`/`T2`/`G` from *your own* reading instead of a guessed
`interval=6s`.

**Persistent server-side monitor (not tab-tied).** A per-user `reading_monitor_enabled`
flag drives the two-level engine from the **leader-singleton scheduler**
(catalyst-go-jobs), so it runs whether or not the admin panel is open — toggle it on,
walk away, come back to the full report. It pushes **toasts** through `notify.Hub`
(owner-scoped, app-wide — same seam Audible finishes use) on a ping/status change,
with a **mode flag**: *debounced-per-book* (one coalesced toast per advancing book /
status change — normal use) ↔ *every-ping* (a toast on each observed change — verbose,
for the diagnostic/reverse-engineering phase). The panel is then a thin view+toggle
over persisted state, not the engine.

> **Open decision (scope fork):** whether the monitor *emits* `reading_activity` /
> reading-heartbeats from the captured sessions (forward composition into the fusion
> layer) or stays **diagnostic-only** until the cadence is trusted. Until settled, the
> monitor measures + toasts but does not write reading_activity.

---

## 6. Kindle API map (reverse-engineered live, 2026-08)

> **This section is the primary record of the Kindle wire protocol** — it is not
> yet in code (`internal/domains/books` is a stub whose `SyncUser` returns nil).
> All of it is authenticated by the **shared** `internal/amazon` device
> credential: `amazon.Sign` ADP-signs every request (`x-adp-token` /
> `x-adp-alg: SHA256withRSA:1.0` / `x-adp-signature`), exactly as Audible does —
> **one Amazon registration authenticates both.** Hosts differ per call.

### 6.1 Endpoint map — what each surface actually is

```mermaid
flowchart TD
    CRED["internal/amazon DeviceCredential<br/>ADP-signs everything (Sign)"]

    CRED --> TODO["todo-ta-g7g.amazon.com<br/>/FionaTodoListProxy/syncMetaData?type=EBOK"]
    CRED --> ITEMS["todo-ta-g7g.amazon.com<br/>/FionaTodoListProxy/getItems?type=EBOK"]
    CRED --> COLL["api.amazon.com<br/>/whispersync/v2/data/&lt;customer_id&gt;/datasets"]
    CRED --> SIDE["cde-ta-g7g.amazon.com<br/>/FionaCDEServiceEngine/sidecar?type=EBOK&key=&lt;ASIN&gt;"]

    TODO -.->|"EMPTY delta-sync — red herring, NOT the library"| X1["✗"]
    ITEMS -.->|"todo/notification feed (mostly Audible events)"| X2["✗"]
    COLL ==>|"CloudCollections = SHELVES<br/>records key books by amzn://&lt;ASIN&gt;/BOOK"| SHELF["shelf ⇒ status"]
    SIDE ==>|"kindle.lpr = last-page-read<br/>position + timestamp (+ annotations)"| HEART["reading-heartbeat source"]

    SHELF --> RESOLVE
    HEART --> RESOLVE["ASIN → metadata via Hardcover match ladder<br/>(device auth returns NO title/author/cover)"]
```

| Host + path | What it *actually* is | Use |
|---|---|---|
| `todo-ta-g7g.amazon.com/FionaTodoListProxy/syncMetaData?type=EBOK` | An **empty delta-sync** feed. The initial red herring — it looks like it should be the library but returns nothing useful. | ✗ not the library |
| `todo-ta-g7g.amazon.com/FionaTodoListProxy/getItems?type=EBOK` | A **todo/notification feed** — mostly Audible events, not owned ebooks. | ✗ not the library |
| `api.amazon.com/whispersync/v2/data/<customer_id>/datasets` | **CloudCollections** = the user's **shelves** (Currently Reading / Done Reading / Have Not Read / series). Each `.../datasets/<id>/records` lists books by **ASIN** as keys of the form `amzn://<ASIN>/BOOK`. | ✅ **library + status.** Shelf ⇒ status: reading / read / want. `<customer_id>` is the numeric `DeviceCredential.CustomerID`, **not** the ASIN. |
| `cde-ta-g7g.amazon.com/FionaCDEServiceEngine/sidecar?type=EBOK&key=<ASIN>` | Per-book **`kindle.lpr`** — last-page-read (position + timestamp) plus annotations/bookmarks. | ✅ **the reading-heartbeat source** (§5). Poll over time; diff positions into sessions. |

### 6.2 No metadata over device auth

The device-signed endpoints return **no title/author/cover** — only ASINs, shelf
membership, and reading positions. So Kindle metadata is resolved the same way
everything else is: **ASIN → Hardcover** via the match ladder (§3, rung 1 on
`amazon_asin`). This is the second consumer of the one match engine.

> **Alternative (noted, not chosen):** the clean full-metadata library lives at
> `read.amazon.com` (the Cloud Reader), but it is **web-cookie** auth, not device
> auth — a different, more fragile session model. We deliberately resolve metadata
> through Hardcover instead of taking on a second Amazon auth surface.

### 6.3 Kindle ingest — Option A (chosen)

```mermaid
flowchart LR
    C["CloudCollections<br/>datasets/records"] -->|"ASINs + shelf status"| MAP["shelf ⇒ status<br/>reading/read/want"]
    S["sidecar?key=ASIN"] -->|"LPR position + date"| POLL["poll & diff over time"]
    MAP --> RI["reading_items(source='kindle')"]
    HCV["Hardcover match ladder"] -->|"ASIN → title/author/cover"| RI
    POLL --> RA["reading_activity<br/>(pages from LPR deltas)"]
```

The chosen ingest pipeline, mapping onto the existing tables:

1. **CloudCollections → ASINs + shelf status.** Enumerate `datasets`, read each
   `records` set, extract the `amzn://<ASIN>/BOOK` keys, map shelf → `status`.
2. **sidecar → LPR position + date** for each ASIN. This is the polled signal.
3. **Hardcover → metadata.** Resolve each ASIN to title/author/cover through the
   match ladder (device auth has none).
4. **Write** `reading_items(source='kindle')` (state + resolved metadata + the
   `hardcover_*` linkage) and derive `reading_activity` rows from **polled LPR
   deltas** (the forward reading-heartbeat model, §5).

This slots straight into the existing domain shape: `books.Service` already holds
the shared `*amazon.Store` and the DB; only `SyncUser` (and a `BackfillUser`
mirror) need filling in, reusing `amazon.SignedGet`/`Sign`, `hardcover.Match`,
`UpsertReadingItem`, and `UpsertReadingActivity`.

---

## 7. Safety + gating

Every mechanism above is behind two independent, fail-safe gates and rides the
domain registry so secrets are never stranded.

- **Feature gate — `BOOM_FEATURE_BOOKS` (default false).** `cfg.BooksEnabled()`
  (`internal/config/config.go`) is the master switch for both domains + the shared
  Amazon connect flow. `main` ships with it off and stays deployable; all job
  registration and scheduling sits inside `if cfg.BooksEnabled()`
  (`cmd/boomtime/main.go`). `AudibleSyncEnabled()` folds in a positive
  `BOOM_AUDIBLE_SYNC_INTERVAL` (default `6h`).
- **Hardcover dry-run — `BOOM_HARDCOVER_DRYRUN` (default TRUE).** Fail-safe: with
  dry-run on, **no write ever reaches Hardcover.** `hardcover.Configure` sets the
  process-wide default at startup (`main.go` L112); the client's `graphql()`
  intercepts every `mutation`, logs `hardcover DRYRUN: write blocked`, and returns
  a simulated success so the push chain becomes a logged no-op (reads pass
  through). `RunHardcoverPush` *additionally* surfaces a per-book `hardcover.dryrun`
  toast before it would mutate — the user sees exactly what would be written. Two
  layers, both defaulting closed.
- **Concurrency caps.** `hardcover-push` is capped at concurrency **1**
  (`jobReg.SetConcurrency(audiobooks.HardcoverPushKind, 1)`) because Hardcover's
  < 60 req/min is a **global** resource shared across all users; the
  `KindLimiter` (`internal/jobs/limiter.go`, Redis- or mem-backed) enforces the
  fleet-wide cap. `audiobooks-audible-sync` and `-backfill` are likewise capped at
  1.
- **Domain registry drives rotate + backup.** The Amazon device credential and
  the Hardcover token are per-user AES-256-GCM secrets registered in
  `internal/domains/registry.go` (`encryptedColumns` + `backupColumns`). This is
  what makes the rotate-encryption-key command re-encrypt them and the whole-DB
  backup include their ciphertext + status columns **automatically** — the whole
  point of the registry is that a new domain adds one entry instead of being
  silently stranded on the next rotation or dropped from backups.
- **Siloed data.** `reading_items` / `reading_activity` / `book_sync_state` are
  all `ON DELETE CASCADE` with the user and never write into `heartbeats`/`stats`;
  the fusion layer *reads* them. Plaintext secrets are never logged and never
  returned by any API.

---

## 8. Open / next

- **Inbound Hardcover pull.** Grab a real Hardcover token, verify the live
  `user_books` shape, and build the pull → reconcile on `hardcover_book_id` →
  echo-suppress via `hardcover_pushed_at` → last-writer-wins via
  `hardcover_remote_updated_at` (§4.2). Columns exist; the query + reconcile loop
  do not yet.
- **Kindle ingest job.** Implement `books.Service.SyncUser`/`BackfillUser` against
  the §6 map (CloudCollections → sidecar LPR → Hardcover metadata →
  `reading_items`/`reading_activity`), reusing the shared `amazon` + `hardcover`
  plumbing. Register `KindleSyncKind` + a schedule behind `BooksEnabled()`.
- **Outbound beyond finish.** Today only the finished edge is mirrored; extending
  the push to status/progress changes is a follow-up once the inbound
  last-writer-wins arbitration is in place (so the two directions don't fight).
- **Manual-review surface.** Wire the `MatchNone` rows into an admin diagnostics
  list so low-confidence books can be resolved by hand rather than guess-pushed.

---

### Appendix — file map

| Concern | Path |
|---|---|
| Shared Amazon auth + signing | `internal/amazon/{amazon,register,signing,client}.go` |
| Hardcover connector + dry-run gate | `internal/hardcover/{client,match,push,hardcover}.go` |
| Audible domain (full) | `internal/domains/audiobooks/audiobooks.go` |
| Kindle domain (stub) | `internal/domains/books/books.go` |
| Domain registry (rotate/backup) | `internal/domains/registry.go` |
| Data model migrations | `internal/db/migrations/000{58,59,60,61,62,63}_*.sql` |
| DAL | `internal/db/{reading_items,reading_activity,book_sync_state,hardcover_token,amazon_device}.go` |
| Query DSL (reading domain) | `internal/query/domains.go` |
| Jobs concurrency limiter | `internal/jobs/{jobs,limiter,local}.go` |
| Wiring + gates | `cmd/boomtime/main.go`, `internal/config/config.go` |
| Sync recipe companion | [`docs/design/catalyst-books-sync-architecture.md`](./catalyst-books-sync-architecture.md) |
