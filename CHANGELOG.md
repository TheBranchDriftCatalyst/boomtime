# Changelog

All notable changes to boomtime are documented here. This file is generated
by [git-cliff](https://git-cliff.org) from conventional-commit history.
## [0.5.3] - 2026-07-13

### Bug Fixes

- **widgets:** Project scope now handles renamed/merged names (gaka-xuc)

### Chores

- **docs:** README self-embed widget + rename db-erd.mmd -> db-erd.md

### Features

- **ai:** Capture wakatime.com AI-assistance fields + Overview AI card (gaka-1l9)
- **import:** Copy drift findings for schema-update feedback loop (gaka-rl6)

## [0.5.2] - 2026-07-13

### Bug Fixes

- **import:** Drop client-side base64 on wakatime api_key; add show/hide toggle (gaka-f2l)

### Features

- **brand:** 'Made by Catalyst Development' attribution (gaka-486)

## [0.5.1] - 2026-07-13

### Bug Fixes

- **security,web:** Cookie-auth logs WS + refresh-loop robustness (gaka-af5, gaka-ia3)
- **k8s:** Pin prod overlay image tag to :gakatime for initial rollout
- **build:** Reconstruct internal/cache TTL package
- **heartbeats:** Badge Space membership for regex rules, not just exact
- **curation:** Hooks-order crash when editing a remapping rule
- **viz:** Dashboard contrast/color/scale sweep + category breakdown + files-are-files

### Chores

- Ignore agent worktrees, sync beads log, add repo descriptor
- **logs:** Log DB query tracer at DEBUG (out of the INFO stream)
- **config:** Enable DB query tracer + slow-query EXPLAIN by default in dev
- Rename dev database test -> boomtime (in-place ALTER, data preserved)
- **compose:** Add restart: unless-stopped to db/app/web

### Documentation

- **query-engine:** Add mermaid diagrams for the two-layer query construction
- Add QUERY_ENGINE.md — deep dive on the aggregation/curation/scope engine
- Refresh all page screenshots (synthwave + Spaces); document Spaces, drop tag refs
- Add WHY.md (origin story) + link from README
- README + DEMO (full-page tour) + ARCHITECTURE + screenshots

### Features

- **settings:** Plugin setup tab with wakatime.cfg snippet (gaka-pi0)
- **heartbeats:** Entity Explorer + search fix + index sizes (gaka-90x)
- **widgets:** Named/saved widget defs table (gaka-3nu)
- **server,web:** /healthz + BOOM_GRADE_* env + welcome modal (gaka-oih, gaka-unq.4, gaka-cly)
- **auth:** Shared user/token creation service (gaka-0tb)
- **tilt:** Tiltfile for local k3s dev (gaka-7q9)
- **k8s,ci:** Fix image path + continuous GHCR publish (gaka-acx)
- **k8s:** Argo-managed manifests + local overlay (gaka-a4d)
- **widgets:** Roll links, per-link hit tracking + origins, drop delete (gaka-hsj follow-up)
- **widgets:** Interactive builder — compose primitives via URL-inline spec (gaka-567)
- **widgets:** 4 more chart twins via primitives (gaka-unq.3)
- **widgets:** DRY primitives + 4 new widget kinds incl. composite (gaka-unq.2)
- **widgets:** Embeddable SVG stats widgets + widget-builder foundation (gaka-hsj)
- **viz:** 'Other' bucket breakdown tooltip (gaka-7m4)
- **release:** Changelog + versioning + GHCR + secrets safety (gaka-o5k)
- **importer:** Detect + persist wakatime.com API schema drift (gaka-unq.1)
- **viz:** Tooltip audit + shared helper across all charts (gaka-9pt)
- **rollup:** Widen hb_rollup_daily to 8 axes (+category/plugin/branch) (gaka-e0l)
- **dashboard:** Persist date-range/time-limit across navigation
- **heartbeats:** Add-to-Space action + Space membership badges on group rows
- **backup:** Save/load the entire database to/from a file (gaka-x0v)
- **web:** Opt-in GA4 analytics module with SPA route tracking
- **logs:** Live server-log viewer tab (WebSocket, reload-durable)
- **sources:** Show heartbeat count per source-health row
- **projects:** Stack 'Total activity' by language
- **curation:** Edit name-remapping rules in place (pencil icon)
- **overview:** Stack 'Total activity' by category
- **curation/spaces:** Autocomplete axis values + live per-strategy preview
- DB observability + source-health panel; track cmd/gakatime entrypoint
- Spaces — rule-based scoped dashboards; remove unused tags
- **projects:** Cross-project active-files table (lynchpins)
- **curation:** Capture/replace-group remappings + Explorer regex/template badge
- **ui:** Synthwave/cyberpunk dark theme (first pass)
- **curation:** Regex name remappings + view-affected + project-extras remap
- **ui:** Collapsible sidebar → icon-only rail
- **projects:** Split into aggregate rail + explicit per-project selector
- **curation:** Non-destructive reversible rename + Settings remappings list + db:mermaid
- **explorer:** Unified TanStack Explorer, import backfill, suppress-from-explorer + curation coverage/tests
- **viz:** Council big-bets — category streamgraph, punchcard, deep-work sessions, momentum grid
- **viz:** Council quick-wins — 6 D3 visualizations + per-project metrics
- Gakatime — Go + React 1:1 port of hakatime, with import, curation, D3 charts and fast rollups

### Performance

- **db:** Pg_trgm + text_pattern_ops for Space regex queries (gaka-o4m)
- **web:** Route-split routes + vendor chunks (gaka-4hv)
- **ingest:** SaveHeartbeats runs atomically in one tx via pgx.Batch (gaka-4sq)

### Refactoring

- **logging:** Return LogHub from Setup, thread through New (gaka-yzs)
- **importer:** HTTP timeout, cancel-ack, unexport internals, drop dead code (gaka-al6)
- **web:** DRY D3 viz layer + import-path updates for feature folders
- Split backend monoliths per-domain; move web to feature folders
- Rename application gakatime -> boomtime
- **charts:** Remove ApexCharts, complete D3 strangler-fig migration
- **sources:** Source health keys on (plugin, machine), not editor/plugin/machine
- **curation:** DRY — one shared RemappingForm for Settings + Explorer

### Tests

- **e2e:** Playwright suite for add-to-Space + membership badges
- **frontend:** Vitest + RTL + msw + mock-socket AIO harness + 70 unit tests
- **backend:** Shared AIO harness + DRY existing tests + P0 gaps + handler HTTP integration
- **curation:** Regex remap integration test across all aggregation paths
- **db:** Isolated gakatime_test database + anonymized real-data fixtures


