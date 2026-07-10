# Architecture

How gakatime fits together. For the visual tour see [DEMO.md](../DEMO.md); for the schema see [db-erd.mmd](db-erd.mmd).

## Stack

| Layer | Choice |
|---|---|
| Language / HTTP | Go 1.25 · Echo v5 |
| DB / driver / migrations | PostgreSQL 16 · pgx v5 + pgxpool · goose (embedded, auto-run at startup) |
| Auth | Argon2id password hash · UUID API tokens stored `base64(uuid)` · HttpOnly refresh cookie |
| CLI / config / logs | cobra (`run`/`create-user`/`create-token`/`run-migrations`) · `HAKA_*` env · `log/slog` |
| Frontend | React 19 · Vite 8 · Tailwind v4 · TanStack Query + Table · react-router v7 · D3 v7 + ApexCharts · shadcn/Radix |

The SPA is embedded into the Go binary (`go:embed web/dist`) and served as the fallback route, so production is a **single binary** (`HAKA_DASHBOARD_PATH` overrides to serve from disk).

## Request flow

```
editor plugin ─┐
browser  SPA ──┼─► Echo router ─► handler ─► db.* (embedded SQL, pgxpool) ─► Postgres
wakatime import┘        │
                        ├─ auth middleware (Basic <base64(uuid)> → owner)
                        ├─ per-owner TTL response cache (stats)
                        └─ import worker (goroutine) + WS hub for live logs
```

- **Wire compatibility:** paths, JSON field names (hakatime's `noPrefixOptions`), and `Authorization: Basic <token>` match Wakatime, so editor plugins and the import path are drop-in.
- **Heartbeat ingest** upserts on `(entity, sender, time_sent)`, detects language from the entity extension, parses the user-agent into editor/plugin/platform, and (optionally) forwards to a remote-write URL.

## The duration & rollup model (why it's fast)

Wakatime-style "time spent" is not stored — it's derived from gaps between heartbeats. gakatime precomputes that at ingest:

- **`gap_seconds`** = seconds to the previous heartbeat for the same sender in global time order. Computed by an anchored window-function `UPDATE` (`RecomputeGaps`) whenever heartbeats land.
- **Time spent** for a range = `SUM(CASE WHEN gap_seconds ≤ timeLimit*60 THEN gap_seconds ELSE 0 END)` — a **windowless conditional sum**, no per-request session reconstruction.
- **`hb_rollup_daily`** — a coarse per-(sender, day, project, language, editor, platform, machine) rollup, refreshed for affected days at ingest. The default 15-min path reads the rollup (fast); a non-default `timeLimit`, a hide the rollup can't apply, or a Space scope falls back to the raw scan.
- **Payload bounding** — top-N resources + an aggregated **"Other (N more)"** bucket, and ~weekly bucketing of long time-series (`MAX_CHART_POINTS`), so "All time" over ~440k rows returns quickly and never freezes the browser.

Health of this derived data (gap coverage, rollup vs raw drift, table/DB sizes) is surfaced in the Heartbeats **Derived-data panel**, with a one-click **Resync**.

## Curation (reversible, query-time)

Rules live in `curation_rules` (`sender, axis, action, match_value, new_value, match_type`) and are applied **at read time** — raw heartbeats are never mutated:

- **hide** → an exclusion predicate `AND NOT (<col> = ANY($n))` spliced into the aggregation `WHERE` (covers 8 axes; rollup falls back to raw when needed).
- **rename / merge** → an outer re-group wrapping the query, `GROUP BY` a `CASE`-remapped display value; `match_type` is `exact` (`col = ANY`) or `regex` (`col ~ pattern`). Merges combine; deleting a rule reverts instantly.
- The **Heartbeats Explorer stays raw** (audit surface) even when dashboards are hidden/merged — it just badges affected rows.

Both are threaded through every aggregation path (raw stats, rollup, projects list, leaderboards, category/punchcard/sessions/momentum, project detail) and validated by DB + handler integration tests.

## Frontend

- **Data** — TanStack Query per endpoint, keyed on `(range, timeLimit, space)`; a shared toolbar drives every page. Auth token is in-memory only, bootstrapped from the HttpOnly refresh cookie on load (60s refresh loop, cross-tab logout).
- **Charts** — a strangler-fig `RendererProvider`: each chart is a switcher over `<Name>Apex` and `<Name>D3`, toggled globally. D3 charts read theme tokens and recolor on theme flips via a MutationObserver.
- **Perf** — the same ~weekly bucketing (`viz/bucket.ts`) feeds all time-series so All-time stays bounded; the contribution calendar is the intentional raw-daily exception.

## Repo layout

```
cmd/gakatime            cobra entrypoint (run | create-user | create-token | run-migrations)
internal/
  config logging server db handler stats auth apierr wakatime importer model
  db/migrations  db/queries(*.sql, embedded)  db/main_test.go(isolated gakatime_test)
  testutil       handler-level HTTP integration harness
tools/gendata          fake-heartbeat generator   tools/fixturegen  anonymized fixture
web/                   React SPA (src/{pages,components,viz,hooks,lib,test})
docs/                  ARCHITECTURE.md · db-erd.mmd · testing/TEST_MATRIX.md · screenshots/
Taskfile.yml .air.toml docker-compose.yml Dockerfile
```
