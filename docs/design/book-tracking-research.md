# Book Tracking Research — Audible · Kindle · Goodreads · Hardcover

> **Status:** research spike (2026-08). Feeds the `catalyst-books` domain (v1 of the
> domain-packages architecture — see `catalyst-domains-spike.md`).
> **Goal:** durably track everything DJ reads/listens to (Kindle + Audible now,
> `*arr` book apps later), with boomtime as source-of-truth and **Hardcover** as a
> synced downstream mirror.

---

## 1. Strategy decision — **boomtime-first**

Boomtime is the durable source of truth: it scrobbles Audible (strong data) and
Kindle (best-effort), absorbs Goodreads + Amazon-export backfills, and **pushes**
reading state *out* to Hardcover via its GraphQL write API.

**Why not Hardcover-first:** the Hardcover API is explicitly **beta**, its token
**resets every Jan 1** (and expires yearly), it has **no** access to your
Audible/Kindle progress, and there is **no bulk API** — so a Hardcover-first design
would still need the exact same ingest layer, just with a fragile store on the
critical path. The one hybrid concession: use Hardcover's **catalog** (`editions` +
`search`) as the *matching authority*, since it stores ASIN + ISBN per edition.

## 2. The headline: **one Amazon device link covers Audible *and* Kindle**

The `mkb79` maintainer confirms *"the login/device registration is the same as for
the Audible service."* A single one-time **device registration** (`adp_token` + RSA
private key) can sign **both** Audible and Kindle endpoints via
`X-ADP-Request-Digest`. So a single **"Connect Amazon"** step feeds both domains —
**no cookies, no TLS-fingerprint sidecar** for Kindle. This collapses Kindle from a
scary standalone scraper into the same auth as Audible.

- Signature: `X-ADP-Request-Digest: SIG1<base64 sig>:<timestamp>`, RSA over
  `SHA256(method \n url \n timestamp \n body \n adp_token)`.
- **Caveat:** `mkb79/kindle` is early (~14 commits, "in development"). The *auth/signing*
  is proven (we run it for Audible anyway); we port that signing onto the Kindle
  endpoint set ourselves. Integration cost, not architectural risk.

---

## 3. Per-source deep dive

### 3.1 Audible — the strong source ✅

Auth: one-time device registration (Amazon OpenID auth-code + PKCE; NOT a public
OAuth app — `mkb79/Audible` impersonates the Audible app's client). Yields a durable
`.audible` auth file (RSA signing key + refresh token), good for months,
auto-refreshing the 60-min bearer. **Store it encrypted** (`BOOM_ENCRYPTION_KEY`).

| Endpoint | Gives |
|---|---|
| `GET /1.0/library` (with `response_groups=...,is_finished,percent_complete,listening_status`) | library + **`percent_complete` (0-100)** + **`is_finished`** inline |
| `GET /1.0/stats/status/finished` (ASIN + RFC3339 range) | **finish dates** |
| `GET /1.0/stats/aggregates` (`total_listening_stats`) | listening time (daily/monthly buckets, not per-title) |
| `GET /1.0/lastpositions/{asin}` | last playback position (ms) |
| `GET /content/{asin}/metadata` | chapter metadata |

- **Gotcha:** `percent_complete`/`is_finished` only return if you pass them in
  `response_groups` — the default library call omits them (why people wrongly think
  "only the book list is available"). Finish **dates** are NOT on `/library` — use
  `/stats/status/finished`. Paginate (`num_results` up to ~1000 + `page`). Register
  the correct **marketplace locale** or you get an empty library.

### 3.2 Kindle — device-signed API (primary), position-only ⚠️

Reuse the **same Amazon device registration** as Audible.

| Endpoint | Gives |
|---|---|
| `POST https://firs-ta-g7g.amazon.com/FirsProxy/registerDevice` | register once → `adp_token`, RSA key, numeric `user_id` (needed below — **not** the ASIN) |
| `GET https://todo-ta-g7g.amazon.com/FionaTodoListProxy/syncMetaData` (XML) | library: ASIN, title, authors, cover |
| `GET https://api.amazon.com/whispersync/v2/data/{user_id}/datasets` → `.../records` | most-recent-page-read **position** |
| `GET https://sars.amazon.com/sidecar/sa/EBOK/{ASIN}` | highlights (binary/Ion sidecar format) |

- **Progress %** = `(metadata.startPosition + position) / metadata.endPosition` —
  Kindle stores KRX position units, never a %, so you fetch book metadata to derive it.
- Poll every ~15-30 min ≈ near-real-time. **Durability: high** (presents as a native
  Kindle device). Deregister cleanly on unlink (shows under Manage Your Content & Devices).
- **Fallback only (do NOT ship to prod):** the Cloud Reader web API
  (`read.amazon.com/service/...`, `transitive-bullshit/kindle-api`) needs a
  `tls-client` sidecar + cookies that "expire every few minutes." Prototype only.
- **Sideloaded (non-Amazon) books** never appear in cloud whispersync — only via
  `My Clippings.txt` over USB.

### 3.3 Goodreads — dates + ratings backfill (no live API) ⚠️

Developer API **dead since Dec 2020** (no new keys). Two read paths survive:

- **CSV export** (`https://www.goodreads.com/review/import` → *Export Library* →
  `goodreads_library_export.csv`). Richest source. Columns: `Book Id, Title, Author,
  ISBN, ISBN13, My Rating, Average Rating, Date Read, Date Added, Bookshelves,
  Exclusive Shelf, My Review, Read Count, ...`. **Manual desktop-only button** (no
  programmatic trigger) → user-initiated upload. Strip the `="..."` Excel-guard
  wrapper on ISBNs. **No start date, no progress.**
- **Per-shelf RSS feed** (`https://www.goodreads.com/review/list_rss/{USERID}?shelf=read`)
  — **CONFIRMED LIVE 2026**, pollable. Items carry `user_read_at` (finish),
  `user_rating`, `user_shelves`, `isbn`, `user_review`. **Hard cap: 100 most-recent
  per shelf** (workaround: yearly `read-2025` shelves). Public profile or a non-secret
  `key=` RSS token. No start date, no progress, no Read Count.

**Kindle↔Goodreads link reality:** the automatic reading-**progress-%** sync **broke
~2018** and was never restored. Today Kindle only pushes coarse start/finish *status*
("Currently Reading" on open, "Read" + rating at the last page) for Amazon-purchased
books, partly a manual per-book action. So Goodreads contributes **finish date +
rating + read-shelf membership** — exactly what CSV/RSS already surface.

### 3.4 Amazon "Request My Data" (privacy export) — start dates + time-read backfill ⚠️

The official GDPR/CCPA export (`https://www.amazon.com/hz/privacy-central/data-requests/preview.html`,
select the **Kindle** category) returns real reading activity, not just purchases:

- `Kindle.KindleDocs.DocumentMetadata.csv` — per-book **first-opened/start date**,
  cumulative **time-read (ms)**, session count, page-turn count.
- `Kindle.Devices.ReadingSession.csv` — individual sessions (timestamps + durations;
  often truncated to ~30s/1 page-turn).
- `Kindle.ReadingInsights` — books read, sessions, pages, time (powers the in-app
  streak dashboard, which has **no** standalone export/endpoint).

- **No clean finish date / finished boolean** — must infer from last session reaching
  end-of-book. **Manual + days-latency** (email-confirm click; "usually less than a
  month," in practice a few days). → **periodic one-shot backfill, not a feed.**
- **Complementary to the others:** it uniquely provides **start dates + time-read**
  (which whispersync lacks), while Goodreads provides **finish dates + ratings**.

### 3.5 Hardcover — the sync target ✅

- Endpoint: `https://api.hardcover.app/v1/graphql` (Hasura). Auth: user-pasted bearer
  token (from account settings) — **expires yearly + resets every Jan 1**; store
  `encrypted_hardcover_key` + `hardcover_key_status`. Backend-only (no CORS) → always
  proxy through boomtime.
- Docs: `github.com/hardcoverapp/hardcover-docs`. Best working reference for exact
  mutation arg shapes: `billiam/hardcoverapp.koplugin` (Lua).

| Operation | Shape |
|---|---|
| Match by id | `editions(where:{isbn_13:{_eq}})` / `{asin:{_eq}}` → `edition.id`, `book_id`, `reading_format_id` |
| Fuzzy match | `search(query:"title author", query_type:"Book")` (Typesense; server-side `_like`/regex **disabled**) |
| Upsert status | `insert_user_book(object:{book_id,status_id,edition_id,...})` |
| Progress + dates | `insert_user_book_read` / `update_user_book_read` (`progress_pages`, `started_at`, `finished_at`) |
| Journal (optional) | `insert_reading_journal` (notes/quotes) |

- `status_id`: 1=Want · 2=Reading · 3=Read · 4=Paused · 5=DNF.
  `reading_format_id`: 1=Physical · 2=Audio · 4=Ebook.
- **Throttle < 60 req/min** (token bucket ~1/s); check the mutation-level `error`
  field even on HTTP 200; return an error on 429 so the job retries with backoff.
  **No bulk API** — backfill by iterating your own catalog.

---

## 4. Data-availability matrix

| Signal | Audible | Kindle (device API) | Goodreads (CSV/RSS) | Amazon export |
|---|:--:|:--:|:--:|:--:|
| Library list | ✅ | ✅ | ✅ | ✅ |
| Progress % | ✅ `percent_complete` | ✅ (computed from position) | ❌ (dead ~2018) | ❌ (unreliable) |
| Finished flag | ✅ `is_finished` | ⚠️ infer (%≈100) | ✅ read shelf | ⚠️ infer |
| **Start** date | ❌ | ❌ | ❌ | ✅ first-opened |
| **Finish** date | ✅ `/stats/status/finished` | ❌ | ✅ `Date Read` | ⚠️ infer |
| Time read | ⚠️ daily/monthly agg | ❌ | ❌ | ✅ per-book ms |
| Rating | ✅ | ❌ | ✅ `My Rating` | ❌ |
| Highlights | ❌ | ✅ sidecar | ❌ | ❌ |
| Re-read count | ⚠️ | ❌ | ✅ `Read Count` | ❌ |

**Reading:** Audible is fully covered. Kindle gives live progress but is date-blind →
Goodreads fills **finish dates + ratings**, Amazon export fills **start dates +
time-read**. No single Kindle source is complete; fuse them into `reading_state`.

## 5. The matching problem (the crux of any sync)

Every book arrives with a different identity token → resolve to a Hardcover
`book_id` + `edition_id`, **stop at first hit**, then **cache the result**:

1. **Audible ASIN** → `editions(where:{asin:{_eq}})` (audio edition)
2. **Kindle ASIN** → `editions(where:{asin:{_eq}})` (ebook edition)
3. **Goodreads ISBN-13** → `editions(where:{isbn_13:{_eq}})`
4. **Fallback** → Typesense `search()` → score candidates yourself → pick `book_id`
   → choose an edition via `editions(where:{book_id:{_eq}})` filtered by
   `reading_format_id`
5. **No match → manual-review queue** (never guess-push)

**Cache** `hardcover_book_id`, `hardcover_edition_id`, `match_method`,
`match_confidence` on the row so the periodic sync only *writes* against known IDs
(protects the 60 req/min budget + avoids re-fuzzing).

## 6. Data model sketch

```
books                       -- canonical work (per user)
  id, user_id, title, authors, series, cover_url,
  hardcover_book_id, hardcover_edition_id,
  match_method, match_confidence, created_at

book_sources                -- one row per external identity feeding a book
  id, book_id, source ('audible'|'kindle'|'goodreads'|'amazon-export'),
  external_id (ASIN/ISBN), raw_meta jsonb, last_seen_at

reading_state               -- fused truth per book (source of the Hardcover sync)
  book_id, status ('want'|'reading'|'read'|'paused'|'dnf'),
  percent, started_at, finished_at, rating, read_count,
  source_of_progress, updated_at, hardcover_synced_at
```

`reading_state.status` → Hardcover `status_id`; `percent`/dates → `user_book_read`.

## 7. boomtime reuse (nothing to lift from catalyst-py)

> ⚠️ The root `CLAUDE.md` advertises `catalyst-py`'s `audible_bookmarks/` +
> `goodreads/` modules — **those are gone from the workspace** (catalyst-py itself is
> absent). Nothing to lift; build fresh. *(Recommend a doc-cleanup bead.)*

Reuse boomtime's own substrate:
- **`internal/jobs`** (catalyst-go-jobs) — queue + leader-singleton scheduler, unchanged.
- **`internal/github`** — the closest 1:1 template for a per-user periodic external-API
  sync (encrypted token → decrypt-scoped fetch → upsert; `ErrNoToken`/`ErrRateLimited`).
- **`internal/auth/crypto.go`** — AES-256-GCM for the Amazon auth file + Hardcover key.
- **`internal/db/wakatime_key.go` / `github_token.go`** — the per-user encrypted-secret
  CRUD shape (Set/Get/GetInfo/Update/Clear/List/Rotate) to copy.
- **`web/src/features/import/`** — full import UI (form + live-log WS) for bulk backfill.
- **`config.FeatureGithubStats` / `GithubStatsRefreshEnabled()`** — the feature-gate
  pattern to mirror.

## 8. Auth & durability — isolate each source behind its own adapter + `*_key_status`

- **Audible/Kindle (medium-durable):** one encrypted Amazon device auth. Register once
  per marketplace; add to the DB backup export set + `rotate-encryption-key` (per the
  `gaka-6jm`/`gaka-awh` patterns). Break mode: password change / deregister → re-auth.
- **Hardcover (rotatable):** user-pasted bearer; expires yearly + Jan-1 reset → build a
  re-prompt into the UX; distinguish 401 (bad token) from 403 (query not allowed).
- **Goodreads / Amazon-export:** user-initiated file uploads → zero live auth, zero
  fragility.

**Principle:** each source is a pluggable adapter writing into shared `reading_state`;
the Hardcover writer reads only `reading_state`. A dead upstream = stale rows, never a
broken sync.

## 9. Phased plan (bead-able)

1. **Foundation + matching spike** — `books`/`book_sources`/`reading_state` schema,
   encrypted Hardcover key (clone `wakatime_key.go`), a Hardcover GraphQL client, and a
   manual "add book" that resolves one ASIN via `editions()` → pushes a status. *Proves
   matching + write-auth end-to-end.*
2. **Amazon connect + Audible ingest** — one encrypted Amazon device link; `internal/…/audible`
   periodic job cloned from `internal/github`; `/1.0/library` (progress groups) +
   `/1.0/stats/status/finished`. *Whole audiobook library with real progress + dates.*
3. **Hardcover periodic sync** — the `hardcover-sync` scheduled job (throttled, 401/429
   handling, leader-singleton). *Hands-off mirror.*
4. **Goodreads + Amazon-export backfills** — CSV/RSS (finish dates, ratings, shelves) +
   privacy-export (start dates, time-read). Zero live auth.
5. **Kindle ingest** — reuse the Amazon device link → whispersync position → progress %;
   highlights via sidecar. *Fuse with Goodreads/export for dates.*

## 10. Open questions for DJ

1. **Sync direction** — one-way push (boomtime overwrites Hardcover, *simple*) vs
   two-way reconcile (pull back Hardcover-app edits)? Recommend **one-way** to start.
2. **What to push** — status + dates + progress only, or also ratings / re-reads /
   review notes / privacy setting?
3. **Multi-user or just DJ?** Single-user simplifies the encrypted-token storage/UX.
4. **ToS comfort** — the Amazon device-API path is unofficial (low community
   ban-risk, no SLA) on your own account.

---

## Resources

**Audible**
- `github.com/mkb79/Audible` (PyPI `audible`) · docs `audible.readthedocs.io`
- `github.com/mkb79/audible-cli` (`audible quickstart`, `audible library export`)
- Libation — `getlibation.com`
- `moifort/audible-api-ts` (TypeScript port)
- Endpoints: `/1.0/library`, `/1.0/stats/status/finished`, `/1.0/stats/aggregates`,
  `/1.0/lastpositions/{asin}`, `/content/{asin}/metadata`

**Kindle (device-signed API)**
- `github.com/mkb79/kindle` (same auth base as mkb79/Audible)
- ptbrowne writeup — `ptbrowne.github.io/posts/whispersync-reverse-engineering` ·
  `github.com/ptbrowne/whispersync-lib`
- Endpoints: `firs-ta-g7g.amazon.com/FirsProxy/registerDevice`,
  `todo-ta-g7g.amazon.com/FionaTodoListProxy/syncMetaData`,
  `api.amazon.com/whispersync/v2/data/{user_id}/datasets`,
  `sars.amazon.com/sidecar/sa/EBOK/{ASIN}`
- Cloud Reader fallback: `github.com/Xetera/kindle-api` + `transitive-bullshit/kindle-api`
- Clippings parsers: `klip` (npm), `gfranxman/Kindle-Clippings-Parser` (Python)
- Notebook scrapers: `delebedev/kindle-notes`, `mieubrisse/kindle-highlight-scraper`
- KOReader plugins: `Billiam/hardcoverapp.koplugin`, `burneracc0112/storygraph.koplugin`

**Goodreads**
- CSV: `https://www.goodreads.com/review/import`
- RSS: `https://www.goodreads.com/review/list_rss/{USERID}?shelf=read`
- HTML list (full history): `https://www.goodreads.com/review/list/{USERID}?shelf=read&per_page=100&page=N`
- `PaulKlinger/Enhance-GoodReads-Export` (backfills start/finish dates onto the CSV)

**Amazon privacy export**
- `https://www.amazon.com/hz/privacy-central/data-requests/preview.html`
- `read.amazon.com/kindle-library/search` (semi-official Cloud Reader JSON) · `read.amazon.com/manage`
- Empirical schema writeups: `blog.hackerific.net/2026/03/26/exploring-amazon-data-exports/`,
  `jakelee.co.uk/analysing-5-years-of-amazon-kindle-reading/`, `roadtolarissa.com/kindle-tracker/`

**Hardcover**
- Endpoint: `https://api.hardcover.app/v1/graphql` (GraphiQL: `cloud.hasura.io/public/graphiql?endpoint=https://api.hardcover.app/v1/graphql`)
- Docs: `github.com/hardcoverapp/hardcover-docs`
- Reference impl: `billiam/hardcoverapp.koplugin` (Lua)
- Other OSS syncers: `booklore-app/booklore`, Calibre-Web-Automated

**Prior art / patterns**
- Audiobookshelf → Hardcover syncers · Kobo → StoryGraph native sync (June 2025, the
  "do it right" benchmark Kindle refuses to match)
