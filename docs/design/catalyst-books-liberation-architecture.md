# catalyst-books Liberation Architecture (Libation rebuild)

> **Status:** definitive architecture (2026-08). Implement from this.
> **Scope:** rebuilding [Libation](https://getlibation.com) (rmcrackan/Libation, C#/.NET)
> into the `catalyst-books` domain — Audible content licensing, AAXC download, DRM
> strip, M4B remux with chapters/cover/tags, a filesystem library sink, and the
> liberated-state tracking that makes re-runs idempotent.
> **Precedents this builds on:** `catalyst-books-sync-architecture.md` (the domain
> model + job wiring this extends) and `book-tracking-research.md` §10/Resources (the
> unofficial-device-API posture). This doc GUIDES the liberation epic's beads.

> **Implementation status (2026-08-24).** Built and green: `SignedPost` +
> licensing (§2.1), voucher decrypt (§2.2), naming template + FS sink (§6),
> config gate (§7), prod storage (§7.1), and the live-verification harness
> (§9.1). Not yet built: streaming download (§2.3), the ffmpeg decryptor (§2.4),
> chapters/tags (§2.5), the migration (§3), the service (§4), jobs (§5), and the
> HTTP/FE/CLI surfaces (§8).

**Reading conventions.** Everything in `code font` (`amazon.Sign`, `reading_items`,
`AudibleSyncKind`, …) is a real symbol on the current tree — verify the exact
signature at implement time, but the names are correct as of writing.
**"NEEDS-LIVE-VERIFY"** marks a protocol detail derived from upstream
(mkb79/audible-cli, Libation) that has NOT yet been exercised against a real
Amazon response on this tree. Every one of them is a place where a wrong guess
surfaces as an Amazon-side error, never a local one.

**Non-negotiable rules** (inherited from the sync architecture, unchanged):

- **Additive migrations only.** `reading_items` already exists (`00058`); we
  `ADD COLUMN` onto it. Never alter or drop an existing column.
- **Everything new is gated** behind a NEW flag `BOOM_FEATURE_BOOKS_LIBERATION`,
  itself nested under `cfg.BooksEnabled()` (`BOOM_FEATURE_BOOKS`). `main` ships with
  the flag off and stays deployable.
- **Must BUILD:** `CGO_ENABLED=0 go build ./...` clean; FE `yarn typecheck` clean.
- **Siloed:** liberation state lives only on `reading_items` (+ the new
  `book_liberation_attempts`); it never writes into `heartbeats`/`stats`/any core model.
- **Never log a content key, voucher, or `adp_token`.** The AAXC key/IV are
  per-book secrets on the same footing as the device credential
  (`CLAUDE.md` → Encryption at Rest). They are used in-memory and discarded.

---

## 0. The shape at a glance

```
   ONE "Connect Amazon" device cred            internal/books/connect/amazon
   (register.go, ALREADY SHIPPED)  ──────────► Store.Load → *DeviceCredential
                                               Sign(cred, method, path, body, now)
                                               SignedGet  (exists)
                                               SignedPost (NEW — §2.1)
                                                     │
                    ┌────────────────────────────────┴──────────────────────┐
                    │                                                       │
        internal/books/ingest/audible                    internal/books/liberate  (NEW)
        (library sweep → reading_items)                  ┌──────────────────────────────┐
        ALREADY SHIPPED                                  │ 1. license.go   licenserequest│
                    │                                    │ 2. voucher.go   AES-CBC unseal│
                    │  reading_items.external_id (ASIN)   │ 3. fetch.go     stream AAXC   │
                    └────────────────────────────────────► 4. remux.go     ffmpeg -c copy│
                                                          │ 5. tag.go       chapters/cover│
                                                          │ 6. sink.go      fs library    │
                                                          └───────────┬──────────────────┘
                                                                      │
                                             BOOM_BOOKS_LIBRARY_PATH/<template>.m4b
                                             (PVC / NFS → truenas00; Audiobookshelf scans it)
```

The insight that scopes this whole epic: **`internal/books/connect/amazon` is already
a Go port of Libation's `AudibleApi` project.** Device registration
(`register.go:1`), ADP request signing (`signing.go:44`), and the library sweep
(`ingest/audible/audiobooks.go`) are done and running in prod. What is missing is
Libation's `FileLiberator` + `AAXClean` + `FileManager` — steps 1-6 above.

---

## 1. Parity matrix — Libation vs this tree

| Libation project | Responsibility | State here |
|---|---|---|
| `AudibleApi` — registration | PKCE device auth → adp_token + RSA key | **SHIPPED** `connect/amazon/register.go` |
| `AudibleApi` — signing | `SHA256withRSA:1.0` ADP headers | **SHIPPED** `connect/amazon/signing.go` |
| `AudibleApi` — library | `/1.0/library` paging + response_groups | **SHIPPED** `ingest/audible/audiobooks.go` |
| `AudibleApi` — **licensing** | `/1.0/content/{asin}/licenserequest` | **MISSING** → §2.1 |
| `AudibleApi` — **voucher** | AES-CBC unseal → content key/IV | **MISSING** → §2.2 |
| `DataLayer` (EF/SQLite) | book rows + **liberated state** | Partial — `reading_items` exists, no liberation cols → §3 |
| `FileLiberator` | orchestrate download→decrypt→convert | **MISSING** → §2.3-2.4, §5 |
| `AAXClean` | AES-CBC `mdat` + MP4 remux, no re-encode | **MISSING** → ffmpeg subprocess, §2.4 |
| `FileManager` | naming templates, folder tree, tags, cover | **MISSING** → §6 |
| WinForms / Avalonia UI | library grid + liberate buttons | Partial — `features/books/BooksPage.tsx` + `BookDetailSheet.tsx` exist → §8 |
| `LibationCli` | `libation liberate` / `scan` / `export` | **MISSING** → §8.3 (and the CLI-completion convention applies) |

**v1 (this epic) covers:** licensing, voucher, download, remux to M4B with chapters +
cover + tags, filesystem sink, liberated-state, per-book + bulk liberate.
**Deferred to the follow-up epics in §10:** PDF/supplement sidecars, MP3 chapter
splitting, naming-template editor UI, library export, auto-liberate-on-scan,
multi-account.

---

## 2. The liberation protocol

### 2.1 License request — `SignedPost`

`Sign()` already accepts `method` and `body` (`signing.go:44`), so the only gap on the
client is a POST sibling to `SignedGet` (`client.go:28`). It belongs in
`connect/amazon/client.go` beside its GET twin — same `httpClient`, same
`metrics.AmazonCallsTotal.WithLabelValues("signed")` counter, same 64 MiB read cap.

```
POST https://api.audible.<tld>/1.0/content/{asin}/licenserequest
x-adp-token / x-adp-alg / x-adp-signature   (Sign with method="POST", body=the JSON below)
Content-Type: application/json

{
  "supported_media_features": {
    "chapter_titles_type": "Tree",
    "codecs": ["mp4a.40.2", "mp4a.40.42", "ec+3", "ac-4"],
    "drm_types": ["Mpeg", "Adrm"]
  },
  "consumption_type": "Download",
  "quality": "High",
  "response_groups": "content_reference,chapter_info,pdf_url,last_position_heard",
  "spatial": false
}
```

**NEEDS-LIVE-VERIFY:** the canonical string for a POST. `Sign()` builds
`method\npath\ndate\nbody\nadp_token` — the `body` element is currently only ever
`""` in practice (every caller is `SignedGet`). Confirm Amazon expects the raw JSON
bytes there, byte-for-byte identical to what goes on the wire (no re-marshal between
signing and sending — sign the exact `[]byte` you post).

Response fields we consume, all under `content_license`:

| Field | Use |
|---|---|
| `license_response` | the sealed voucher → §2.2 |
| `content_metadata.content_url.offline_url` | the AAXC CDN URL → §2.3 |
| `content_metadata.chapter_info` | chapter tree, ms offsets → §2.5 |
| `content_metadata.content_reference.content_format` | e.g. `AAX_44_128` — codec/DRM discriminator |
| `content_metadata.content_reference.file_version` / `sku` | provenance, stored in `raw_meta` |
| `pdf_url` | supplement PDF — **deferred to §10 epic B**, but capture it into `raw_meta` now |

`status_code: "Denied"` (rather than `"Granted"`) is the "not owned / license
refused" terminal case — it must NOT be retried, and it must not look like a
transport failure. Map it to a distinct `liberation_status = 'denied'`.

### 2.2 Voucher decrypt

The voucher is AES-128-CBC sealed with a key derived from four values we already
hold. Per mkb79/audible-cli (`decrypt_voucher`, itself from mkb79/Audible#3):

```
buf    = device_type + device_serial + customer_id + asin      (ASCII concat)
digest = sha256(buf)
key    = digest[0:16]
iv     = digest[16:32]
plain  = AES-CBC-decrypt(base64decode(license_response), key, iv)
```

`plain` is JSON: `{"key": "<hex>", "iv": "<hex>", "rules": [...]}` — the AAXC content
key and IV.

Every input is on hand: `deviceType` is the const at `register.go:44`
(`A2CZJZGLK2JJVM`), and `DeviceSerial` + `CustomerID` are fields on `DeviceCredential`
(`amazon.go`). Pure stdlib (`crypto/aes`, `crypto/sha256`, `encoding/base64`) — no new
dependency.

**NEEDS-LIVE-VERIFY:** the trailing-bytes handling. Upstream strips trailing `\x00`
rather than doing PKCS#7 unpad; the robust implementation is to decrypt, then trim to
the last `}` before `json.Unmarshal`. Also confirm the concat ORDER of the four
values — a wrong order yields a clean AES decrypt of garbage, which is a confusing
failure mode. **Write the unit test against a fixture captured from a real response**
so the order is pinned once and never re-guessed.

**Concatenation order note:** `device_type + device_serial + customer_id + asin` is
what audible-cli uses. Libation's `AudibleApi` derives the same digest. If the first
live attempt produces non-JSON, permute before assuming the endpoint changed.

### 2.3 Download

`GET` the `offline_url` and stream it to a temp file under
`BOOM_BOOKS_WORK_PATH` (default `os.TempDir()`). Notes:

- It is a pre-signed CloudFront URL. **NEEDS-LIVE-VERIFY** whether the ADP headers
  are required on it (audible-cli attaches auth; the URL may be self-authorizing).
  Attach them — harmless if ignored.
- **Do not** route this through `SignedGet`: its 64 MiB `io.LimitReader` cap
  (`client.go`) would silently truncate a 500 MB audiobook. Liberation downloads get
  their own unbounded streaming path with an explicit `Content-Length` check.
- Files are 100 MB – 1 GB+. Stream to disk; never buffer in memory.
- Emit progress. The job must heartbeat throughout (see §5) — a 20-minute download
  must not look like a hung worker to the reaper.
- Verify `Content-Length` against bytes written; a short read is a retryable failure,
  not a corrupt-but-accepted file.

### 2.4 DRM strip + remux — ffmpeg subprocess

**Decision (2026-08): shell out to ffmpeg**, behind a `Decryptor` interface so a
native Go remuxer can replace it later without touching callers.

```
ffmpeg -nostdin -y \
  -audible_key <hex key> -audible_iv <hex iv> \
  -i <work>/<asin>.aaxc \
  -i <work>/<asin>.chapters.txt   # ffmetadata, §2.5
  -map_metadata 1 \
  -c copy \
  -movflags +faststart \
  <work>/<asin>.m4b
```

`-c copy` means no re-encode: this is a remux, so it is I/O-bound and fast, and the
audio is bit-identical to what Audible served.

**Why an interface, not a bare exec:**

```go
// internal/books/liberate/decrypt.go
type Decryptor interface {
    // Decrypt strips DRM from src (AAXC/AAX) and writes an M4B to dst,
    // applying meta (chapters, cover, tags). Never logs key/iv.
    Decrypt(ctx context.Context, src, dst string, k ContentKey, meta Metadata) error
}
```

- `ffmpegDecryptor` — v1, the subprocess above.
- `nativeDecryptor` — §10 epic D, an AAXClean-equivalent port
  (AES-CBC over `mdat` samples + rewrite `sinf`/`schm`) if xHE-AAC or image size bites.

**Risks this decision accepts, explicitly:**

1. **Image size.** The runtime stage is `alpine:3.20` with only
   `ca-certificates tzdata bash bash-completion` (`Dockerfile:75`). Adding `ffmpeg`
   costs roughly 80 MB. Mitigation: add it ONLY to the runtime stage, and make the
   liberation feature degrade cleanly (feature-flag off → never exec'd) so an image
   without ffmpeg still boots. **Probe `ffmpeg -version` at startup when the flag is
   on and log a clear ERROR if absent** — same posture as the `BOOM_ENCRYPTION_KEY`
   prod check.
2. **xHE-AAC / `mp4a.40.42`.** Newer Audible titles use USAC. This is precisely why
   Libation wrote AAXClean instead of using ffmpeg. `-c copy` is a remux and *should*
   pass the codec through untouched, but ffmpeg's `aax` demuxer may refuse to parse
   the container. Mitigation: record `content_format` per book, and let a failure here
   set `liberation_status='unsupported_codec'` with the format captured — that turns
   an unknown into a queryable count, and it is the trigger that justifies epic D.
3. **Legacy AAX (activation_bytes).** Pre-AAXC titles use the activation-bytes
   scheme (`ffmpeg -activation_bytes ...`). Out of scope for v1; detect via
   `content_format` and mark `unsupported_format`. Epic C picks it up.

### 2.5 Chapters, cover, tags

- **Chapters** — `content_metadata.chapter_info.chapters[]` gives `title` +
  `start_offset_ms` + `length_ms`, possibly nested (hence `chapter_titles_type: Tree`).
  Flatten depth-first and emit an ffmetadata file:
  ```
  ;FFMETADATA1
  [CHAPTER]
  TIMEBASE=1/1000
  START=0
  END=142000
  title=Chapter 1
  ```
  Also honour `is_accurate` and `brandIntroDurationMs`/`brandOutroDurationMs` when
  present — Audible offsets can include the branding segments.
- **Cover** — `product_images` is already persisted as `json.RawMessage` on
  `LibraryItem` (`audiobooks.go`). Take the largest available, fetch, and attach as
  the M4B cover (`-disposition:v attached_pic`).
- **Tags** — title, subtitle, authors, narrators, series + sequence, release date,
  publisher, genre from the category ladder, and the description. All of these are
  ALREADY in `reading_items.raw_meta` from the library sweep — **no extra Amazon call
  is needed for tagging.** That is a meaningful simplification over Libation, which
  re-fetches.

---

## 3. Data model

### 3.1 Migration `000NN_book_liberation.sql` (additive)

```sql
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS liberation_status   text,        -- see enum below
    ADD COLUMN IF NOT EXISTS liberated_at        timestamptz,
    ADD COLUMN IF NOT EXISTS audio_path          text,        -- relative to BOOM_BOOKS_LIBRARY_PATH
    ADD COLUMN IF NOT EXISTS audio_bytes         bigint,
    ADD COLUMN IF NOT EXISTS audio_format        text,        -- 'm4b'
    ADD COLUMN IF NOT EXISTS content_format      text,        -- Audible's AAX_44_128 etc.
    ADD COLUMN IF NOT EXISTS liberation_error    text;        -- last failure, user-visible
```

`liberation_status` enum (text, not a PG enum — additive-only rule):
`pending` · `licensing` · `downloading` · `converting` · `liberated` · `failed` ·
`denied` · `unsupported_codec` · `unsupported_format` · `skipped`.

**Why on `reading_items` and not a separate table:** the same reason the curation
override layer went there (`00069`) — one row per owned title, and the Explorer
already reads that table, so a `liberation_status` column becomes a filterable
column + facet for free.

### 3.2 `book_liberation_attempts` (NEW table)

One row per attempt, for the diagnostics/admin surface: `id, owner, asin,
started_at, finished_at, status, bytes, duration_ms, error, content_format`.
`reading_items` carries the CURRENT state; this carries the history. Keeps the
"why did this book fail three times last week" question answerable without
scraping job logs out of MinIO.

### 3.3 Idempotency

The re-run contract, mirroring `BackfillUser`/`SyncUser`: a book with
`liberation_status='liberated'` AND a file present at `audio_path` with matching
`audio_bytes` is **skipped**. Missing file → re-liberate (someone deleted it).
Present but wrong size → re-liberate (truncated download). `--force` overrides.

---

## 4. Package layout

```
internal/books/liberate/           NEW — the FileLiberator equivalent
  liberate.go     Service + Options; LiberateBook / LiberateAll orchestration
  license.go      LicenseRequest → LicenseResponse   (§2.1)
  voucher.go      DecryptVoucher → ContentKey        (§2.2)
  fetch.go        streaming AAXC download + progress (§2.3)
  decrypt.go      Decryptor interface                (§2.4)
  ffmpeg.go       ffmpegDecryptor + startup probe
  chapters.go     chapter_info → ffmetadata          (§2.5)
  tags.go         raw_meta → tag args + cover fetch  (§2.5)
  sink.go         Sink interface + fsSink            (§6)
  template.go     naming template render + sanitize  (§6)
```

`connect/amazon/client.go` gains `SignedPost`. That is the ONLY change to an existing
shipped package on the Go side — everything else is new files. Deliberate: the amazon
connect package is load-bearing for prod Kindle + Audible sync, so the liberation epic
touches it once, additively, with its own test.

---

## 5. Job wiring

Two new kinds in `internal/books/jobs/jobs.go`, registered inside the existing
`cfg.BooksEnabled()` block and additionally gated on the liberation flag:

| Kind | Shape | Cap |
|---|---|---|
| `books-liberate-book` | owner + ASIN payload; ONE book end-to-end | 2 |
| `books-liberate-sweep` | owner-scoped; enqueue `books-liberate-book` for every unliberated title | 1 |

**Cap of 2, not 1:** unlike the Hardcover kinds (capped at 1 because Hardcover's rate
limit is a global resource), liberation is bounded by disk and Amazon's CDN.
Two concurrent gives useful throughput without looking like abuse. Make it
`BOOM_BOOKS_LIBERATE_CONCURRENCY`.

**Heartbeat is mandatory.** A single book is minutes of download plus minutes of
remux. Per `boomtime-jobs-deploy-resilience`, the reaper kills jobs that stop
heartbeating — the download loop and the ffmpeg wait MUST both tick. This is the
single most likely source of "it worked locally, it dies in prod."

**Resumability.** A killed job restarts the book from scratch in v1 (partial temp
files are discarded on start). HTTP range-resume is epic B.

**No schedule by default.** `books-liberate-sweep` is enqueued on demand from the UI
or CLI. An auto-liberate-on-scan schedule is epic B — deliberately not v1, because a
first run against a large library is hundreds of GB and that should be a decision, not
a side effect of turning on a flag.

**Notifications.** Reuse `notify.Hub` exactly as `audible.Service` does — a
`BookLiberated` event per completion so the browser toasts, and a sweep-complete
summary.

---

## 6. The filesystem sink

**Decision (2026-08): filesystem, not S3.** The entire point of Libation is producing
a tree that Audiobookshelf/Plex/Jellyfin scans. MinIO is wired here (`objstore`) but
nothing in the homelab can scan an S3 prefix, and a full library is hundreds of GB.

```go
type Sink interface {
    // Commit atomically moves a finished file into the library, returning
    // the path relative to the library root.
    Commit(ctx context.Context, workPath, relPath string) (string, error)
    Stat(ctx context.Context, relPath string) (size int64, ok bool, err error)
    Remove(ctx context.Context, relPath string) error
}
```

`FSSink` writes under `BOOM_BOOKS_LIBRARY_PATH`. An `s3Sink` remains possible
later via `objstore` — the interface exists so that stays a config change, not a
refactor.

**`NewFSSink` refuses to create its root.** A typo'd or unmounted
`BOOM_BOOKS_LIBRARY_PATH` would otherwise silently `MkdirAll` onto the
container's ephemeral layer and quietly fill it with audiobooks that vanish on
the next deploy. An unmounted NFS volume is exactly this failure, so the root
must already exist and be a directory or construction fails loudly.

**Atomicity:** convert into `BOOM_BOOKS_WORK_PATH`, then `os.Rename` into place.
A reader (Audiobookshelf) must never see a partial `.m4b`. When work and library are
on different filesystems — the NFS case — `os.Rename` fails with `EXDEV`; fall back to
copy-to-`.partial`-then-rename **within the library filesystem**. Get this right or
Audiobookshelf indexes half-written files.

**Naming template.** IMPLEMENTED (boom-w20s.11) with one deviation from the
original sketch above: instead of two separate templates for the series and
standalone cases, the template supports **optional groups** in `[...]` — a group
is dropped whole when any placeholder inside it renders empty. One template
serves both cases:

```
{author}/[{series}/]{title}/{title}.m4b        # liberate.DefaultTemplate

Neal Stephenson/Snow Crash/Snow Crash.m4b
James S. A. Corey/The Expanse/Leviathan Wakes/Leviathan Wakes.m4b
```

This also removes the dangling-separator problem the two-template approach
papered over (`[{series_index} - ]{title}` renders cleanly with or without an
index). Placeholders: `author`, `title`, `subtitle`, `narrator`, `series`,
`series_index`, `year`, `asin`.

**Rendering rules — and the ORDER, which is security-critical.** The original
sketch said "sanitize per path segment". That is not sufficient, and the
implementation does something stronger. Sanitising only the assembled segments is
WRONG because the split on `/` happens after substitution: a title containing
`../../etc/passwd` would already have become real directory levels by then. (This
was caught by the hostile-input table during implementation, not by review.)

The implemented order is:

1. **sanitise every VALUE first** — after this no substituted value can contain
   `/` or `\`, so it cannot forge a path component
2. drop optional groups whose (sanitised) values are empty
3. substitute
4. reject a template that produced a file with no name (an empty `{title}` would
   otherwise yield a file literally called `m4b`)
5. split on `/` — by construction these are template-authored separators only
6. sanitise each segment again for template-literal junk; drop empties
7. verify no segment is `.` or `..` and the result is relative

Per-segment sanitisation strips NUL, C0 controls, and Unicode format characters
(RTL overrides — a filename-spoofing vector), maps `/` `\` and the
Windows/SMB-hostile set ``<>:"|?*`` to `-`, collapses whitespace, trims leading
and trailing dots/spaces, suffixes Windows reserved device names (`CON` → `CON_`),
and caps each segment at 120 bytes with UTF-8-safe truncation. Template is
config-only in v1 (`BOOM_BOOKS_NAMING_TEMPLATE`); the editor UI is epic C.

---

## 7. Config + gates

| Env | Default | Meaning |
|---|---|---|
| `BOOM_FEATURE_BOOKS_LIBERATION` | `false` | master gate, nested under `BOOM_FEATURE_BOOKS` |
| `BOOM_BOOKS_LIBRARY_PATH` | `""` | library root; empty = liberation disabled even if flagged on. Prod: `/media/audiobooks/liberated` |
| `BOOM_BOOKS_WORK_PATH` | `os.TempDir()` | scratch for download + convert. Prod: `/tmp/boomtime-liberate` — deliberately NOT on the NFS mount |
| `BOOM_BOOKS_NAMING_TEMPLATE` | see §6 | path template |
| `BOOM_BOOKS_LIBERATE_CONCURRENCY` | `2` | per-kind cap |
| `BOOM_BOOKS_FFMPEG_PATH` | `ffmpeg` | binary lookup |

`LiberationEnabled() bool` sits on `config.Config` next to `BooksEnabled()`,
returning `c.FeatureBooks && c.FeatureBooksLiberation && c.BooksLibraryPath != ""`.
One predicate, checked everywhere — the same shape as `AudibleSyncEnabled()`.
IMPLEMENTED, with gate specs in `config_more_test.go`.

### 7.1 Prod storage (IMPLEMENTED)

The library is a **static NFS PV + PVC** on the Synology media volume, following
`arr-stack-private/base/storage/synology-private-storage.yaml`:

- `k8s/overlays/talos00-knowledgedump/audiobook-library.yaml` — PV
  (`nfs://192.168.1.36/volume1/media`, `synology-nfs` class, RWX, `Retain`) +
  the bound PVC.
- `k8s/overlays/talos00-knowledgedump/patch-server-audiobook-library.yaml` —
  mounts it at `/media/audiobooks/liberated` with
  **`subPath: audiobooks/liberated`**.

**The PV names the EXPORT ROOT, not the library directory.** `showmount -e
192.168.1.36` reports exactly four exports — `/volume1/media`,
`/volume1/appdata`, `/volume1/downloads`, `/volume1/666`. An earlier draft of
this doc pointed the PV at `/volume1/media/audiobooks`, which is not one of
them; the pod would have hung in `ContainerCreating` on a mount that could never
succeed, and because the Argo Application syncs `main` with
`automated/selfHeal/prune`, that would have taken prod boomtime down on merge.
Always `showmount -e` before writing an NFS path into a PV.

Static rather than dynamically provisioned because the export already exists and
is shared with the media servers — a provisioner would bind a new empty directory
instead of the library everyone else reads. `Retain` so an accidental
`kubectl delete pvc` cannot take the library with it. RWX because boomtime writes
while Audiobookshelf/Plex read, concurrently. The `subPath` both scopes boomtime's
output away from the rest of the share and gets the directory created by kubelet
on first mount, which is what satisfies `NewFSSink`'s must-already-exist rule
without an initContainer.

Ships **inert**: `BOOM_FEATURE_BOOKS_LIBERATION: "false"` in `app-config.yaml`.
The storage is wired first on purpose, so enabling the feature is a one-line
ConfigMap change rather than a storage rollout.

**When liberation jobs move to the worker fleet, the same volume/volumeMount pair
must be copied into the worker patch** — a job cannot write to a library it has
not mounted.

---

## 8. Surfaces

### 8.1 HTTP (`internal/books/api/`)

- `POST /api/v1/books/items/:id/liberate` — enqueue one book. 409 if already in flight.
- `POST /api/v1/books/liberate/sweep` — enqueue the owner sweep.
- `GET  /api/v1/books/liberation/status` — counts by `liberation_status` + in-flight.
- `DELETE /api/v1/books/items/:id/liberate` — forget local file (clears state, optionally unlinks).

### 8.2 FE (`internal/books/web/src/features/books/`)

- `booksExplorerConfig.tsx` — add a `liberation_status` column + facet. Because state
  lives on `reading_items`, this is nearly free.
- `BookDetailSheet.tsx` — a Liberate button + status/progress + last error.
- A library-wide "Liberate all" action with a confirm that states the estimated size —
  a first sweep is hundreds of GB and the user should see that number before clicking.

### 8.3 CLI (`cmd/boomtime`)

`boomtime books liberate <asin|--all> [--force]` and `boomtime books liberation status`.
Per the standing convention (`boomtime-cli-smart-completion` memory): **both need
dynamic completion** — ASIN/title completion via `dbEntityCompleter` off
`reading_items`, and enum completion for any `--status` filter.

---

## 9. Test plan

Per the standing test-layering rule — each layer must catch something the others
cannot, no tautologies.

**Unit** (no network, no ffmpeg):
- `voucher_test.go` — decrypt a CAPTURED real voucher fixture → known key/iv. This is
  the test that pins the concat order forever. Highest-value test in the epic.
- `chapters_test.go` — nested `chapter_info` → correct flattened ffmetadata, including
  the brand intro/outro offset case.
- `template_test.go` — sanitization + traversal rejection. Table-driven, with hostile
  titles (`../../etc`, NUL, 300-char, RTL override, Windows reserved names like `CON`).
- `license_test.go` — request body shape + `Sign` canonical-string for POST.

**Integration** (DB + `httptest` Amazon, real ffmpeg on a tiny generated fixture):
- Full `LiberateBook` against a stubbed Amazon serving a small AAXC-shaped file →
  asserts the row lands `liberated`, the file is where the template says, and
  `audio_bytes` matches.
- Idempotency: second run is a no-op; deleted file re-liberates; truncated file
  re-liberates. (Mirrors the existing Kindle ingest idempotency test.)
- Failure mapping: `Denied` → `denied` and NOT retried; short read → `failed` and
  retryable.
- `EXDEV` cross-filesystem commit path.

**E2E** (Playwright): flag on → Liberate button appears on the detail sheet, click
→ status transitions → row shows liberated. Flag off → 404. Follows the existing
`spec 404=flag off / 401=on` convention from the local e2e stack.

**Explicitly NOT tested:** real Amazon licensing. That is a live-verification
checklist item (every NEEDS-LIVE-VERIFY in §2), run by hand once against the real
account, with the resulting fixtures committed to make the unit tests real.

### 9.1 The live-verification harness (IMPLEMENTED, boom-w20s.19)

Rather than a one-off script, live verification mounts on the **existing** admin
books-diagnostics surface as a third source next to Audible and Kindle:

```
GET /api/v1/admin/books/diagnostics?source=liberation[&asin=B0…]
```

`internal/books/liberate/probe.go` runs six probes and reports a `verdict`
(`pass`/`warn`/`fail`) plus a human `detail` per item, rendered by the same
`ProbeView` component as the existing dumps (Admin › Books › Source diagnostics ›
**Liberation**). With no `asin` it picks the first title in the library.

| Probe | Answers |
|---|---|
| 0 · credential completeness | are `device_serial` + `customer_id` + `adp_token` present |
| 0b · pick a title | which ASIN the sweep is using |
| 1 · POST licenserequest | **is the signed-POST canonical string accepted** |
| 2 · voucher key derivation | **which concat order actually unseals the voucher** |
| 3 · CDN offline_url | is it self-authorizing; does it support Range (→ epic B resume) |
| 4 · content format | what codec real titles report (→ the epic D trigger) |
| 5 · chapter tree | did nested chapters + brand offsets arrive |

Probe 2 is the design's answer to the "a wrong key decrypts cleanly to garbage"
trap: `KeyOrder` is an enum, `AllKeyOrders` is swept, and the probe NAMES the
winner rather than making anyone guess. If a non-canonical order wins it says so
and tells you to update `keyMaterial`'s default and the fixture test.

**Redaction.** The sweep touches a content voucher and a presigned capability
URL. `redactLicenseBody` strips `license_response` and `voucher` and reduces
`offline_url` to scheme+host, failing CLOSED (emits nothing) on any parse
trouble. `ContentKey` implements both `fmt.Stringer` and `slog.LogValuer` so even
an accidental `%v` or `slog.Info(..., "key", k)` prints `[redacted]` — asserted by
tests that check rendered output, not method existence.

---

## 10. Follow-up epics (full-parity roadmap)

Sequenced by value. Each is independently shippable on top of v1.

**Epic B — Robustness + automation.**
Range-resume for interrupted downloads · auto-liberate-on-scan schedule ·
PDF/supplement sidecars (`pdf_url`, already captured in `raw_meta` by v1) ·
cover art as a separate `cover.jpg` for scanners that prefer it ·
disk-space preflight + quota · retry/backoff policy per failure class.

**Epic C — FileManager parity.**
Naming-template editor UI with live preview · per-book path override ·
re-organize existing library when the template changes · legacy AAX
(activation_bytes) support · library export (CSV/JSON, Libation's export feature) ·
"replace/upgrade quality" flow.

**Epic D — Native decoder (drop ffmpeg).**
AAXClean-equivalent in pure Go via `abema/go-mp4`: AES-CBC over `mdat` samples,
rewrite `sinf`/`schm`, build `chpl`. Removes ~80 MB from the image, removes the
subprocess surface, and fixes xHE-AAC. **Triggered by data**, not by taste — ship it
when `liberation_status='unsupported_codec'` counts justify it. The `Decryptor`
interface from §2.4 is what makes this a drop-in.

**Epic E — Formats + reach.**
MP3 chapter splitting (Libation's other output mode) · Opus/AAC transcode profiles for
smaller mobile copies · multi-account support · Audiobookshelf API integration (tell it
to rescan on completion instead of waiting for its poll).

---

## 11. Risk register

| Risk | Severity | Mitigation |
|---|---|---|
| Voucher concat order wrong | High (blocks everything) | Fixture-based unit test from a real capture; permute before assuming API change |
| POST canonical-string mismatch | High | Sign the exact posted bytes; verify live first |
| ffmpeg can't parse xHE-AAC | Medium | Record `content_format`; `unsupported_codec` status; epic D |
| Long jobs reaped mid-download | Medium | Heartbeat in the download loop AND the ffmpeg wait |
| Disk exhaustion on first sweep | Medium | Preflight estimate + confirm-with-size in the UI; epic B adds quota |
| Partial files indexed by Audiobookshelf | Medium | Work dir + atomic rename; EXDEV fallback within library FS |
| Path traversal from Amazon-supplied titles | Medium | Sanitize per segment + resolve-and-prefix-check |
| Amazon rate-limits / flags the account | Low | Cap 2, no default schedule, sequential per user |
| ffmpeg absent from image | Low | Startup probe + clear ERROR; feature degrades off |

**Posture note.** This is the unofficial device API, against the operator's own
purchased library — the same posture already documented in
`book-tracking-research.md` §10.4 for the metadata sync, with bytes attached. It is
the same thing Libation has done publicly since 2020.
