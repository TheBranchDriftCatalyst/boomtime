# Change Detection & the Event Envelope

> **Status:** definitive (2026-09). Read before adding a source or an event type.
> **Scope:** how boomtime turns sources that publish no change feed into an event
> stream — the four extraction strategies, their distinct failure modes, and the
> two envelopes events actually live in.
> **Companions:** [`reading-cadence-measurement.md`](./reading-cadence-measurement.md)
> goes deep on the scalar-delta case; [`catalyst-books-sync-architecture.md`](./catalyst-books-sync-architecture.md)
> covers the books domain's job/module wiring.

---

## 1. Is this CDC?

Partly, and the part that does not fit is the part that bites.

Poll-and-diff *is* a recognised member of the change-data-capture family. The
usual taxonomy runs log-based (WAL/binlog — Debezium), trigger-based,
query-based (`WHERE updated_at > watermark`), and snapshot/diff-based. We are the
fourth, by necessity: Amazon publishes no change feed, so we sample current state
and derive changes against what we stored.

But the property that makes log-based CDC trustworthy — **lossless, ordered,
every intermediate state** — is exactly what we do not get. We only ever see the
states we happened to sample. `reading-cadence-measurement.md` states the
inversion plainly:

> boomtime **becomes** the history by polling. There is no history to fetch — we
> manufacture one by sampling the snapshot over time and diffing.

So "CDC" is defensible shorthand for one of our four strategies. Used as a name
for all of them it flattens a distinction that has already cost real debugging
time, because **the four fail in completely different ways and no single defence
covers two of them.**

---

## 2. The four extraction kinds

Declared in code as `events.ExtractionKind` (`internal/books/events`).

| Kind | The source gives us | The event is | Fails by | Defended by |
|---|---|---|---|---|
| **`scalar-delta`** | one advancing number + a timestamp | the *temporal* difference between consecutive samples | **aliasing** — poll slower than the source writes and the advance is never observed | cadence probing (`monitor.go`), not set logic |
| **`set-reconcile`** | the *current set* for a scope | the *set* difference against stored keys | **absence-as-deletion** — an empty response is identical to "the user deleted everything" | `internal/books/reconcile` |
| **`push`** | the event itself | observed directly | delivery | retries, idempotency keys |
| **`replicated`** | a peer's state, bidirectionally | a converged value | **echo loops** — our own write read back as a remote edit (boom-m5kq) | LWW + echo suppression |

### Why they cannot share an engine

`scalar-delta`'s unit is *time*; `set-reconcile`'s unit is *membership*. A cadence
probe does nothing for absence-as-deletion, and a non-empty-fetch guard does
nothing for aliasing. Consolidating them would suggest one defence covers both.

What they *can* share is the shape of the decision, and that is what
`internal/books/reconcile` isolates.

### `scalar-delta` produces events that never existed

Worth stating bluntly, because it governs how the output may be used: a *reading
session* is not something Amazon recorded and we captured. It is reconstructed
from two position samples and an assumed reading speed — `monitor.go` even
branches on whether whispersync is writing continuously or only at session
boundaries, and reconstructs differently in each case. That is inference, and it
is labelled `Confidence: derived` wherever it feeds analytics.

---

## 3. The two envelopes

A recurring instinct is that everything should be a *reading heartbeat*, by
analogy with the coding side. It is half right, and the wrong half is
load-bearing.

**A heartbeat's defining property is that it has a time.** The entire model —
gap-summing consecutive observations into sessions — depends on it.

- A **reading session** is genuinely a heartbeat. Isomorphic to a coding one.
- An **Audible clip** carries a real `creationTime`, so it can be one.
- A **Kindle highlight has no timestamp at all.** The notebook reports none. It
  cannot be a heartbeat without fabricating the one field that defines a
  heartbeat.
- A **finished book** is a date describing a span, not an observation at an
  instant — a state transition on an artifact.

So there are two envelopes, and both already exist in the schema:

| Envelope | Question it answers | Tables |
|---|---|---|
| **Activity** — timestamped observations, gap-summable | *when, and how much* | `heartbeats`, `kindle_reading_positions` → `reading_activity` |
| **Artifact** — durable things discovered by set diff, time often unknown | *what exists, and what happened to it* | `reading_items`, `book_annotations`, `reading_events` |

**Highlights are artifacts. Sessions are heartbeats.**

### Heartbeat as a projection, not a container

The useful synthesis: a **timed** artifact event is independent evidence of
activity. An Audible clip created at 22:03 proves listening at 22:03 — without
depending on the position-poll cadence at all. That is exact activity data the
aliasing-prone sampler structurally cannot produce.

The precondition is exactly the event's time semantics, so it is already
declared and machine-checkable:

```go
func (t Type) EmitsActivity() bool {
    return t.Time == TimeSourceReported || t.Time == TimeInferred
}
```

The heartbeat is therefore a **downstream projection** that some event types
support — not the universal envelope every event lives in.

> **Consequence for phase 2.** Ingesting Audible clips is not only a corpus
> feature: every clip is a free, exact reading-activity anchor. It improves
> reading-time accuracy on a dimension the sampler cannot reach.

---

## 4. The layering

```
raw samples         kindle_reading_positions              scalar-delta, lossy, forward-only
   │ derive
   ▼
derived event log   reading_activity · reading_events · book_annotations
   │ project
   ▼
read models         *_enriched views → query DSL domains → explorer / charts
```

This is event-sourcing-adjacent: a derived log with projections over it. The CDC
part is only the middle arrow, and only for `set-reconcile`.

---

## 5. The rules

### 5.1 Deletion

Ranked by preference. Inferring where an explicit signal exists is strictly worse.

1. **Explicit signal → hard delete.** The origin told us it is gone.
   `DeleteReadingEventsByExternalIDs` (the Hardcover dedup sweep) is this.
2. **Inferred absence → soft tombstone, guarded.** Only for a source that returns
   the *whole* set for a scope, and only on a complete, non-empty fetch.
   `book_annotations.deleted_at` is this.
3. **No reconciliation → say so.** `reading_items` does not remove rows when a
   book leaves the Amazon library. That is a deliberate `RetireNever`, not an
   oversight, and it is recorded here so nobody "fixes" it into a library wipe.

**The guard, stated once:** a fetch that returned nothing retires nothing. Not a
heuristic — a hard precondition. It lives in `reconcile.retireVerdict` and is
mirrored as defence in depth in `TombstoneMissingBookAnnotations`. Both are
mutation-verified: removing either turns a DOM change into a corpus wipe.

### 5.2 Timestamps

Declared per event type as `events.TimeSemantics`, and **enforced** —
`events.Validate` rejects an event carrying a timestamp on a type declared
`TimeUnknown`.

| Semantics | Meaning | Example |
|---|---|---|
| `source-reported` | the source said when it happened | Audible clip `creationTime`, Hardcover `finished_at` |
| `observed` | when *we* saw it, not when it happened | a library row first appearing |
| `inferred` | we computed it | a reconstructed session boundary |
| `unknown` | no honest time exists | Kindle notebook highlights |

The rule this enforces: **never fabricate an event time.** Stamping `time.Now()`
on a Kindle highlight would date the entire corpus to the first sync and silently
corrupt every time-bucketed chart built on it. `captured_at` stays NULL instead.

### 5.3 Idempotency keys

Prefer a stable key the source guarantees. Verify what it is made of first: the
one Amazon `annotationId` we have seen in full is
`<deviceId>-<ASIN>-EBOK-furthest-page-read` — it embeds the **device serial**, so
re-registering the device would mint new ids and silently double the corpus.
Hence annotations key positionally (`kind:start:end`), with the source id kept in
`raw_meta`.

A positional key means an *edited* item lands as a new row and orphans the old
one, which is why the positional key and the tombstone reconcile ship together.

---

## 6. Why the event registry is Go, not a parsed DSL

`internal/books/events` declares every event type the extraction layer can
produce. It mirrors `internal/shared/query/domains.go`, which does the same on
the read side.

**No text/YAML DSL.** There are no non-engineer authors and no runtime
reconfiguration need, so a data-file DSL would trade compile-time safety and
jump-to-definition for a parser, a schema, a validator, and a drift test between
the files and the Go types they describe.

**No declarative field-mapping either**, because there is no common *wire* format
to write one against: Fiona CDE JSON, scraped notebook HTML, the Audible library
feed, Hardcover GraphQL, pushed wakatime heartbeats. A mapper covering those
needs JSON paths, DOM selectors, unit conversions and fallback chains — a
programming language with worse tooling.

**The common format is the `Event` on the way out**, so the seam is:

```
wire → source-specific parser → typed struct → Extractor → Event → Validate → storage
```

`events.Extractor[In]` is a plain generic func type — the same "declare metadata,
attach a func" shape as `pipeline.Steps`, `climeta.DBLister` and
`corejobs.HandlerFunc`.

Validation runs **before** the storage row is built, so the declaration gates what
reaches the database rather than merely describing it.

### Event identity is finer-grained than the storage row

`annotation.highlight.captured` and (phase 2) `annotation.clip.captured` are
separate types even though both land in `book_annotations` with a `kind` column.
They disagree on time semantics — `unknown` vs `source-reported` — and a single
type cannot declare both. Collapsing them would force either fabricating a time
for highlights or discarding a real one for clips.

---

## 7. Where each source sits today

| Source | Kind | Retire policy | Event time |
|---|---|---|---|
| Kindle position (`kindle_reading_positions`) | `scalar-delta` | n/a (append-only samples) | source `creationTime`, poll-time fallback |
| Kindle notebook (`book_annotations`) | `set-reconcile` | `RetireOnCompleteFetch` | **none** — `TimeUnknown` |
| Audible clips (phase 2, boom-siwi.6) | `set-reconcile` | `RetireOnCompleteFetch` | `creationTime` → can project to activity |
| Kindle / Audible library (`reading_items`) | `set-reconcile` | `RetireNever` — see §5.1(3) | `observed` |
| Hardcover pull | `replicated` | explicit origin signal → hard delete | `finished_at` |
| wakatime heartbeats | `push` | n/a | source-reported |

**Not yet consolidated.** The library syncs (`reading_items`) still upsert without
going through `reconcile` or `events`. That is deliberate for now: adopting the
kernel there is behaviour-preserving only while the policy stays `RetireNever`,
and the migration should be its own change with its own tests rather than a
side effect of the annotations work.

---

## 8. Adding a source — the checklist

1. Pick the **extraction kind**. If you cannot, the source is probably two things.
2. Write the **parser pure** (bytes → typed struct) so it unit-tests against
   inline fixtures. Never commit a captured page: they contain user prose and
   this repository is public.
3. Decide what a **structurally empty response** means, and make the parser
   distinguish "genuinely nothing" from "I no longer understand this" with a
   signal independent of the parse itself. For scraped HTML that is a raw marker
   count; see `ErrNotebookShapeUnknown`.
4. **Register the event type(s)** with honest `Time` and `Confidence`. If the
   source reports no time, declare `TimeUnknown` and let `captured_at` stay NULL.
5. Pick the **key**, and check what the source's own id is made of before
   trusting it.
6. Choose a **retire policy**; default to `RetireNever` until you can show the
   feed is complete per scope.
7. **Mutation-verify** the guards: write the test, watch it go red reproducing the
   real symptom, then fix. A guard test that never went red proves only that it
   agrees with the code it was written against.
