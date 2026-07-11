# Changelog

All notable changes to boomtime are documented here. This file is generated
by [git-cliff](https://git-cliff.org) from conventional-commit history.
## [unreleased]

### Bug Fixes

- **heartbeats:** Badge Space membership for regex rules, not just exact
- **curation:** Hooks-order crash when editing a remapping rule
- **viz:** Dashboard contrast/color/scale sweep + category breakdown + files-are-files

### Chores

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

### Refactoring

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


