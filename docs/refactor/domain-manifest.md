# Domain manifest (gaka-arch / gaka-8tn)

Source-of-truth mapping for the incremental `internal/` restructure. Every current file under `internal/handler/` and `internal/db/` is listed with its target domain. Phases 1–7 move these; phase 8 shrinks what's left.

**Legend**
- `TARGET` — new package under `internal/<domain>/`
- `PHASE` — the migration phase that moves this file
- `TEST` — sibling `_test.go` file(s) that move together

## Handler files (40 total)

| File | Target | Phase | Test siblings |
|---|---|---|---|
| `handler.go` | `internal/handler/` (STAYS — shrinks to composition facade) | 8 | `handler_test.go`, `handler_suite_test.go`, `handler_helpers_test.go` |
| `scope.go` | `internal/stats/scope.go` | 6 | — |
| **meta / phase 1** | | | |
| `meta.go` | `internal/meta/handler.go` | 1 | `meta_test.go` |
| `healthz.go` | `internal/meta/healthz.go` | 1 | `healthz_test.go` |
| `logs.go` | `internal/meta/logs.go` | 1 | `logs_test.go` |
| **spaces / phase 2** | | | |
| `spaces.go` | `internal/spaces/handler.go` | 2 | `spaces_http_test.go` |
| `dashboard_layout.go` | `internal/spaces/dashboard_layout.go` | 2 | `dashboard_layout_more_test.go` |
| **goals / phase 2** | | | |
| `goals.go` | `internal/goals/handler.go` | 2 | `goals_test.go`, `goals_auth_test.go` |
| **widgets / phase 3** | | | |
| `widgets.go` | `internal/widgets/handler.go` | 3 | `widgets_test.go`, `widgets_more_test.go` |
| `widget_defs.go` | `internal/widgets/widget_defs.go` | 3 | `widget_defs_more_test.go` |
| `badges.go` | `internal/widgets/badges.go` | 3 | `badges_more_test.go` |
| **identity + awards / phase 4** | | | |
| `auth.go` | `internal/identity/auth.go` | 4 | `auth_test.go`, `auth_cluster_coverage_test.go`, `auth_seams_test.go` |
| `password.go` | `internal/identity/password.go` | 4 | `password_test.go` |
| `profile.go` | `internal/identity/profile.go` | 4 | `profile_more_test.go` |
| `timezone.go` | `internal/identity/timezone.go` | 4 | `timezone_more_test.go` |
| `wakatime_key.go` | `internal/identity/wakatime_key.go` | 4 | `wakatime_key_test.go` |
| `user_avatar.go` | `internal/identity/user_avatar.go` | 4 | `user_avatar_more_test.go` |
| `awards.go` | `internal/identity/awards.go` (or its own `awards/` if it grows) | 4 | `awards_test.go` |
| `awards_backfill.go` | `internal/identity/awards_backfill.go` | 4 | `awards_backfill_edges_test.go` |
| `awards_eval.go` | `internal/identity/awards_eval.go` | 4 | `awards_eval_test.go`, `awards_coverage_test.go` |
| **ingest / phase 5** | | | |
| `heartbeats.go` | `internal/ingest/heartbeats.go` | 5 | `ingest_cluster_test.go` (split — heartbeats slice) |
| `heartbeats_explore.go` | `internal/ingest/explore.go` | 5 | (in ingest_cluster) |
| `workouts.go` | `internal/ingest/workouts.go` | 5 | (in ingest_cluster) |
| `health_samples.go` | `internal/ingest/health_samples.go` | 5 | (in ingest_cluster) |
| `entities.go` | `internal/ingest/entities.go` | 5 | `entities_test.go` |
| `active_files.go` | `internal/ingest/active_files.go` (OR stats — TBD, see notes) | 5 | `active_files_test.go` |
| **curation / phase 5** | | | |
| `curation.go` | `internal/curation/handler.go` | 5 | `curation_http_test.go`, `curation_invariants_test.go`, `curation_edge_test.go`, `curation_unauth_test.go` |
| `labels.go` | `internal/curation/labels.go` | 5 | `labels_edge_test.go`, `labels_helpers_test.go`, `labels_http_test.go`, `labels_direct_test.go` |
| **stats / phase 6** | | | |
| `stats.go` | `internal/stats/handler.go` | 6 | `stats_more_test.go` |
| `derived.go` | `internal/stats/derived.go` | 6 | `derived_test.go` |
| `timeline.go` | `internal/stats/timeline.go` | 6 | `timezone_more_test.go` (shared?) |
| `projects.go` | `internal/stats/projects.go` | 6 | `projects_test.go` |
| `leaderboards.go` | `internal/stats/leaderboards.go` | 6 | — |
| `bigbets.go` | `internal/stats/bigbets.go` | 6 | `bigbets_test.go` |
| `commits.go` | `internal/stats/commits.go` | 6 | `commits_test.go` |
| **admin / phase 7** | | | |
| `admin_backfill.go` | `internal/admin/backfill.go` | 7 | `admin_backfill_http_test.go`, `admin_ws_integration_test.go` |
| `admin_label_images.go` | `internal/admin/label_images.go` | 7 | `admin_label_images_http_test.go` |
| `label_images.go` | `internal/admin/label_images_public.go` (public GET only) | 7 | `label_images_test.go`, `label_images_unit_test.go` |
| `import.go` | `internal/admin/import.go` | 7 | `import_cluster_test.go` |
| `backup.go` | `internal/admin/backup.go` | 7 | `backup_413_test.go`, `backup_more_test.go` |
| `sources.go` | `internal/admin/sources.go` | 7 | `sources_test.go` |
| **misc / phase 7** | | | |
| `misc_invariants_test.go` | split per domain during phase 7 | 7 | — |

## DB files (37 total)

| File | Target | Phase | Test siblings |
|---|---|---|---|
| `db.go` | `internal/db/` (STAYS — pool wrapper) | 8 | `main_test.go`, `harness_test.go`, `db_suite_test.go` |
| `migrate.go` | `internal/db/` (STAYS — migration runner) | 8 | — |
| `observability.go` | `internal/db/` (STAYS) | 8 | `observability_test.go` |
| `predicates.go` | `internal/db/` (STAYS — SQL helpers) | 8 | — |
| `rows.go` | `internal/db/` (STAYS — shared row scanners) | 8 | — |
| `splice.go` | `internal/db/` (STAYS — SQL string splice) | 8 | — |
| `remap.go` | `internal/db/` (STAYS — regroup helpers) | 8 | — |
| `axes.go` | `internal/db/` (STAYS — axis constants) | 8 | `axes_test.go` |
| **domain-owned queries** | | | |
| `ingest.go` | `internal/ingest/db.go` | 5 | `ingest_test.go` |
| `heartbeats_explore.go` | `internal/ingest/explore_db.go` | 5 | `heartbeats_explore_test.go` |
| `active_files.go` | `internal/ingest/active_files_db.go` (or stats/) | 5 | `active_files_test.go` |
| `entities.go` | `internal/ingest/entities_db.go` | 5 | `redact_entities_test.go` |
| `health.go` | `internal/ingest/health_db.go` | 5 | `health_test.go` |
| `curation.go` | `internal/curation/db.go` | 5 | `curation_test.go`, `apply_rename_test.go`, `case_fold_test.go`, `purge_hidden_test.go`, `rename_merge_test.go`, `template_rename_test.go`, `toggle_curation_test.go`, `regex_all_aggregations_test.go`, `suppression_test.go` |
| `labels.go` | `internal/curation/labels_db.go` | 5 | `labels_test.go` |
| `spaces.go` | `internal/spaces/db.go` | 2 | `spaces_test.go` |
| `dashboard_layouts.go` | `internal/spaces/dashboard_layouts_db.go` | 2 | `dashboard_layouts_test.go` |
| `goals.go` | `internal/goals/db.go` | 2 | `goals_test.go` |
| `widgets.go` | `internal/widgets/db.go` | 3 | `widget_links_test.go` |
| `widget_defs.go` | `internal/widgets/widget_defs_db.go` | 3 | `widget_defs_test.go` |
| `auth.go` | `internal/identity/auth_db.go` | 4 | `auth_test.go` |
| `wakatime_key.go` | `internal/identity/wakatime_key_db.go` | 4 | `wakatime_key_test.go` |
| `user_avatars.go` | `internal/identity/user_avatars_db.go` | 4 | `user_avatars_test.go` |
| `user_timezone.go` | `internal/identity/user_timezone_db.go` | 4 | — |
| `public_profile.go` | `internal/identity/public_profile_db.go` | 4 | — |
| `award_ledger.go` | `internal/identity/award_ledger_db.go` | 4 | `award_ledger_test.go` |
| `activity.go` | `internal/stats/activity_db.go` | 6 | — |
| `ai_activity.go` | `internal/stats/ai_activity_db.go` | 6 | `ai_activity_test.go` |
| `bigbets.go` | `internal/stats/bigbets_db.go` | 6 | — |
| `projects.go` | `internal/stats/projects_db.go` | 6 | — |
| `project_extras.go` | `internal/stats/project_extras_db.go` | 6 | — |
| `leaderboards.go` | `internal/stats/leaderboards_db.go` | 6 | — |
| `backfill.go` | `internal/admin/backfill_db.go` | 7 | `fixture_test.go`, `backfill_test.go`, `branch_padding_test.go`, `error_branches_test.go`, `misc_coverage_test.go` |
| `label_images.go` | `internal/admin/label_images_db.go` | 7 | `label_images_test.go` |
| `sources_health.go` | `internal/admin/sources_health_db.go` | 7 | `sources_health_test.go` |
| `dump.go` | `internal/admin/dump_db.go` | 7 | `dump_test.go` |
| `importjobs.go` | `internal/admin/importjobs_db.go` | 7 | `importjobs_test.go` |

## Notes / open questions per file

- **`handler/active_files.go` + `db/active_files.go`** — belongs to ingest OR stats? It's a projects.go-adjacent read query but backed by ingested heartbeats. Default: ingest. Flip during phase 5 if it makes stats/projects.go awkward.
- **`stats/goals.go`** (eval + aggregation) — moves to `internal/goals/eval.go` in phase 2. Rename cascade: `stats.GoalProgress` etc. Verify grep + goimports fix.
- **`identity/awards*`** — awards may deserve its own package if the identity domain grows. Deferred; keep flat in identity for now.
- **`admin/label_images.go`** — the PUBLIC GET endpoint (unauth) stays under admin/ because it's the same subsystem, just the read-only face. `internal/handler/label_images.go` is the public route file.

## What DOES NOT move

Everything under `internal/` NOT listed above stays put:
- `apierr/`, `auth/` (crypto lib), `cache/`, `config/`, `logging/`, `model/`, `openapi/`, `server/` (shrunk to fan-out), `testutil/`, `fixture/`, `wakatime/`, `labelcatalog/`, `labels/`, `widget/` (renderer), `comfyui/`
- `importer/`, `backfill/git/`, `queue/backfilljobs/`, `queue/imagejobs/`, `worker/labelimages/` — leaf worker subsystems that admin domain USES but doesn't own

## Verification

After each phase:

```bash
task test:db:reset
task test:domain -- <phase-domain>    # e.g. task test:domain -- meta
task test                              # full suite
task test:coverage:summary             # verify overall stays within 0.5% of 88.3% baseline
```

Delete this doc + `moves-phase*.txt` files after phase 8 lands.
