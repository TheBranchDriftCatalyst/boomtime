# The books domain is deprecated

**Status:** DEPRECATED as of 2026-11-19. Still built, still running, still
passing tests — but **no new feature work lands here.** New books/reading work
goes to the BookOrbit fork.

## What replaced it

`../bookorbit` — a fork of [`bookorbit/bookorbit`](https://github.com/bookorbit/bookorbit)
(NestJS + Vue, AGPLv3 + `ADDITIONAL_TERMS.md`), hosted privately at
<https://forge.knowledgedump.space/panda/bookorbit>.

The reasoning, in one line: BookOrbit is a mature reading platform (★6.6k,
active daily) that already runs in this homelab at `bookorbit.talos00` and
already has everything this domain was slowly reimplementing — library
management, readers, OPDS, Kobo/KOReader sync, metadata providers, a request +
indexer + download-client fulfillment pipeline, a jobs UI. What it does **not**
have is any Amazon account integration, which is exactly the part of this domain
that is genuinely novel.

The port plan, the upstream extension-seam analysis, and the per-file port
manifest live in the fork at `docs/catalyst/`.

## Port status — verified against the fork, 2026-11-20

Checked by reading the fork's `server/src/modules/amazon/` (44 files) and
confirming the *invariants*, not just the filenames.

| Area | Lines (non-test) | Status |
| --- | --- | --- |
| Device registration + PKCE + auth-file import (`connect/amazon/register.go`) | 298 | **PORTED.** Live-verified against the real account. |
| ADP request signing (`connect/amazon/signing.go`) | 77 | **PORTED**, and improved — the fork pins it with fixture vectors generated from this Go and re-verified with OpenSSL. |
| Audible owned-library sync (`ingest/audible`) | 1,284 | **PORTED.** 1,060 titles, 4 pages, 0 skipped on the first live sweep. All three invariants survived: `AUDIBLE_FINISHED_PERCENT` via `deriveFinished`, per-item skip-and-report, and `listening_status` left unmapped *with a regression test*. |
| Hardcover pull that creates records | — | **DONE in the fork** as a `will_create` path. Our push direction is not ported and is not planned. |
| **Kindle library sync** (`ingest/kindle` + `connect/amazon/{readamazon,kindle,insights,sidecar}.go`) | ~1,967 + | **NOT PORTED.** The fork has a fixture and a comment saying "Kindle sync will add its own beside it" — no implementation. Tracked as `CAT-0t4`. |
| **Liberation** (`liberate/`) | 3,404 | **NOT PORTED.** Zero lines in the fork; `DOWNLOAD_CLIENT_TYPES` is still the original seven. Tracked as `CAT-3mq`. |
| **Reading-progress polling** (`ingest/kindle/{monitor,monitorconfig,monitor_recommend,reading_time}.go`) | ~1,177 | **NOT PORTED.** Tracked as `CAT-7un`. See below — the highest-value remaining port. |
| Annotations (`ingest/annotations`, `connect/amazon/notebook.go`) | ~1,400 | **Deferred.** Not ported, not deleted. |
| `connect/hardcover` | 3,354 | **Superseded** by upstream's own 34-file module. Still imported by `ingest/kindle` and `ingest/audible`, so it stays until those go. |

**Do not delete anything in a "NOT PORTED" row.** It is the only implementation
that exists.

## Reading-progress polling is the most valuable thing still here

Amazon exposes no reading-time API. The only signal is a last-page-read
*position* in the `kindle.lpr` sidecar, so sessions must be **inferred**. This
code is the only thing that does it:

- `composeSessions` (pure, table-tested) gap-sums consecutive position samples
  using the **same 15-minute cutoff as coding heartbeats**, so a reading session
  and a coding session mean the same thing on a fused calendar. The position must
  *advance* or the interval is idle. Day-crossing intervals split at UTC midnight.
- A two-level adaptive sampler (`monitor.go`): L1 polls all in-progress books
  coarsely, L2 polls an actively-advancing book finely, and `RecommendIntervals`
  derives the cadence from observed p50 interval and p50 position delta.

The adaptive cadence is **not** an optimisation. `change-detection-patterns.md`
classifies this as `scalar-delta` extraction, which **fails by aliasing**: poll
too slowly and you silently under-count, with no error — the numbers just come
out low. A fixed-interval port throws away the defence and the failure is
invisible.

## What was actually removed (boom-sire, 2026-11-20)

Deleted — the *surfaces*, not the protocols:

`internal/books/{api,admin,jobs,pipeline}`, `internal/books/web/embed.go`
(+test), `internal/books/dedup_reads.go`, `liberate_cmd.go`,
`annotations_probe_cmd.go`, `cmd/catalyst-books/`, `cmd/boomtime/books.go`,
`cmd/boomtime/hardcover.go`. Roughly 4,500 non-test lines plus a binary.

Kept: `connect/amazon`, `connect/hardcover`, `ingest/*`, `liberate`, `db`,
`events`, `reconcile`, **`internal/books/web/src`** (live TypeScript the host app
imports via the `@books` alias — a Go-only dependency check missed it), and a
columns-only `module.go`.

`module.go` survives for one reason: `Registry.EncryptedColumns` and
`BackupColumns` aggregate over every registered module **without** consulting
`Enabled()`, so it is the only thing keeping `rotate-encryption-key` and the
whole-DB export covering the Amazon/Hardcover ciphertext still in the database.
Deleting it would not error — rotation would quietly stop covering those rows and
strand them at the next key change.

Two gates were **inverted** rather than removed, because they were wrong:

- `GET /api/v1/notifications` was gated on `BooksEnabled()`, with a TODO asking
  for exactly this. Notifications are a host feature; books was a *producer*,
  never the owner. Left alone, turning the books flag off would have taken the
  notification bell dark app-wide.
- The `reading` and `readingEvents` query domains were gated on `BooksEnabled()`
  too. They are reading-*data* surfaces and the data outlived the domain —
  `reading_activity` rows persist and the BookOrbit connector writes new ones. The
  gate would have 404'd the fused surfaces exactly when fusion started working.

`BOOM_FEATURE_BOOKS` is `"false"` in the talos00 overlay. With
`internal/books/jobs` gone there are no books job kinds or schedules left either
way, so **nothing here syncs Audible, polls Kindle, pushes Hardcover, or
liberates** — which was the point: the fork is now a second device registration on
the same Amazon account, and two sweepers against one account is what
`liberate/license.go` warns about.

## Nothing is being deleted

This code is the reference implementation for the port and the only working
record of several reverse-engineered Amazon protocols. In particular these
cannot be re-derived from documentation, because there is none:

- the **ADP signing** canonical string and header triple (`connect/amazon/signing.go`)
- the **voucher key derivation** order, `SHA256(device_type + device_serial + customer_id + asin)` —
  a wrong order decrypts to clean garbage with no error (`liberate/voucher.go`)
- the **CloudFront User-Agent-only** rule for content downloads; adding auth
  headers triggers a WAF 403 that looks like an auth failure (`liberate/fetch.go`)
- the `-audible_key`/`-audible_iv` flags belonging to ffmpeg's **mov** demuxer,
  not the one named `aax` (`liberate/ffmpeg.go`)
- the live-verified failure taxonomy: which Audible outcomes are terminal
  (`Denied`, `non_audio`/`PodcastParent`) versus retryable

Treat these files as **documentation first, code second.** Their comments
explain *why* each constant is what it is, and several record outages caused by
"cleaning them up."

## Likely future shape here: a small connector, not a domain

Boomtime's remaining reason to care about books is **data fusion** — it is
becoming an analytics engine across coding, reading, health and media. That does
not need an ingest stack; it needs a read-only feed.

So the probable end state is a **small connector** that pulls aggregates from the
BookOrbit fork's API into boomtime's `reading_activity` / stats surfaces, and
nothing else. No credentials, no Amazon calls, no liberation, no per-title
sync — those all live in the fork, which becomes the system of record for books.

That connector is **not built and not designed.** Do not assume it exists.

## Rules until this is actually removed

1. **No new features in `internal/books/`.** Bug fixes to keep the build and
   tests green are fine.
2. **Don't delete anything** without an explicit decision — see above.
3. **Migrations still apply to both trees.** `internal/shared/db/migrations/`
   and `internal/books/db/migrations/` remain live while the code ships.
4. **Porting questions belong in the fork**, at `docs/catalyst/`. Don't fork the
   design discussion across two repos.
