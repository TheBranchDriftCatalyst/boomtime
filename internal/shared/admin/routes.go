// routes.go — Echo route registrations for the admin domain
// (boom-8tn phase 7). Extracted from internal/server/server.go's
// registerImportRoutes + the admin/backup/label-images/sources chunks
// inside registerMiscRoutes + registerStatsRoutes + registerHeartbeat
// Routes so those functions collapse toward N domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 7.
package admin

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/objstore"
)

// Register wires the admin-domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence:
//
//   - source-health (from registerHeartbeatRoutes)
//   - whole-DB backup export + destructive restore (from registerStatsRoutes)
//   - public label-image GET (from registerMiscRoutes)
//   - label-images admin cluster (info / regenerate / WS)
//   - wakatime.com import cluster (create + config + range + jobs list +
//     one-job / cancel / logs / WS)
//
// Route inventory:
//
//	GET    /api/v1/users/current/sources/health                     (h.SourceHealth)
//	GET    /api/v1/users/current/db/export                          (h.DBExport)
//	POST   /api/v1/users/current/db/import                          (h.DBImport)
//	GET    /api/v1/labels/:id/image                                 (h.LabelImage)               PUBLIC
//	GET    /api/v1/admin/label-images                               (h.AdminLabelImagesInfo)
//	POST   /api/v1/admin/label-images/regenerate                    (h.AdminLabelImagesRegenerate)
//	POST   /import                                                  (h.ImportRequest)
//	GET    /import/config                                           (h.ImportConfig)
//	POST   /import/wakatime-range                                   (h.WakatimeRange)
//	GET    /import/jobs                                             (h.ImportJobs)
//	GET    /import/jobs/:id                                         (h.ImportJob)
//	POST   /import/jobs/:id/cancel                                  (h.ImportJobCancel)
//	GET    /import/jobs/:id/logs                                    (h.ImportJobLogs)
//	GET    /import/jobs/:id/ws                                      (h.ImportJobWS)
//	GET    /api/v1/admin/cli/spec                                   (h.CLISpec)      only when FeatureAdminCLI
//	POST   /api/v1/admin/cli/run                                    (h.CLIRun)       only when FeatureAdminCLI
//	GET    /api/v1/admin/cli/run/ws                                 (h.CLIRunWS)     only when FeatureAdminCLI
//	POST   /api/v1/admin/cli/complete                               (h.CLIComplete)  only when FeatureAdminCLI
func Register(e *echo.Echo, h *Handler) {
	// Source health (per plugin/editor/machine last check-in — ingestion
	// health). Owner-scoped read, cached like other reads. Was previously
	// registered under registerHeartbeatRoutes (kept there through phase
	// 5a with a comment pointing at phase 7); the ingest.Register
	// deliberately left this behind for the admin phase to lift.
	//
	// WritesJSON, not GET: the handler is an apihelpers.CachedJSON route that
	// serves PRE-MARSHALLED bytes off the TTL cache. WritesJSON declares the
	// payload type for the spec while leaving the cache hot path untouched.
	apiroute.WritesJSON[sourceHealthResponse](e, http.MethodGet,
		"/api/v1/users/current/sources/health", h.SourceHealth).
		Doc("Ingestion source health",
			"One row per (plugin, machine) pair for the calling owner: the raw "+
				"MAX(time_sent) check-in and the total heartbeat count, ordered "+
				"stalest-first so a silent source surfaces at the top. Heartbeats with "+
				"no plugin are excluded; a blank machine collapses to 'unknown'. This is "+
				"the data behind the Heartbeats \"Source health\" panel — the "+
				"active/idle/stale/silent status is derived CLIENT-side from lastSeen, "+
				"so the server stays a thin aggregate query. Owner-scoped and served "+
				"from the shared TTL cache, so a just-received heartbeat can take until "+
				"the next cache expiry to move lastSeen.")

	// Whole-database backup: streaming dump download + destructive
	// restore (requires ?confirm=replace-all-data; see backup.go). Was
	// previously registered inside registerStatsRoutes with a comment
	// pointing at phase 7.
	//
	// DBExport goes on the seam via Raw: it streams a ZIP straight onto the
	// response writer, so there is no JSON response type to carry — but the
	// media type IS knowable, and documenting it as a generic JSON object would
	// be actively wrong. Raw records "200 application/zip" and passes the
	// handler through to echo unchanged.
	// DBImport goes on the typed seam via POSTNoBody — its RESPONSE
	// (db.RestoreSummary) is typed, while its REQUEST is a raw ZIP upload the
	// seam's JSON binder must not touch.
	apiroute.Raw(e, http.MethodGet, "/api/v1/users/current/db/export",
		"application/zip", http.StatusOK, h.DBExport).
		Doc("Whole-database backup download",
			"Streams a full logical dump of the ENTIRE database — every user's "+
				"heartbeats and settings, password hashes, API tokens, and the "+
				"encrypted-at-rest secret columns — as a ZIP attachment named "+
				"boomtime-backup-<UTC yyyymmdd-hhmmss>.zip. The response is "+
				"application/zip, not JSON. Single-flighted against restore: 409 while "+
				"another backup or restore is already running. The archive carries "+
				"CIPHERTEXT only — BOOM_ENCRYPTION_KEY lives in the environment and is "+
				"never included — so restoring elsewhere needs the same key.")
	apiroute.POSTNoBody(e, "/api/v1/users/current/db/import", h.DBImport).
		Doc("Destructive whole-database restore",
			"DESTRUCTIVE. Uploads a backup ZIP and REPLACES the entire application "+
				"state with it. The request body is the raw archive (application/zip), "+
				"not JSON. Guards, in order: ?confirm=replace-all-data must be present "+
				"or the call is a 400; 409 if a backup/restore is already in flight or "+
				"an import job is running; the upload is spooled to a temp file and the "+
				"archive fully validated BEFORE anything is truncated. A malformed or "+
				"invalid archive is a 400, a schema-version mismatch with the running "+
				"database is a 409, and an upload past BOOM_RESTORE_MAX_BYTES (default "+
				"4 GiB) is a 413 — in all three cases nothing was written. On success "+
				"the restore runs as one transaction, derived tables are rebuilt for "+
				"every sender, every cached aggregate is dropped, and the response "+
				"reports the goose schema version plus per-table and total row counts.")

	// boom-93f.6: admin caps dashboard — users + roles/tiers + effective
	// capabilities. Admin-gated in the handler (requireAdmin).
	apiroute.GET(e, "/api/v1/admin/users", h.ListUsers).
		Doc("Capability dashboard",
			"Every account with its role/tier, disabled flag, and EFFECTIVE "+
				"capabilities — role defaults merged with the per-user override blob, "+
				"with a disabled account short-circuiting to all-false. Also returns the "+
				"canonical capability list (the table's column order) and the "+
				"role→default-capabilities legend, so the UI can show what each tier "+
				"grants without hard-coding it. Admin-gated: 401 without a token, 403 "+
				"for an authenticated user outside the admin allowlist. Read-only — "+
				"changing a role or disabling an account stays in the `boomtime user` "+
				"CLI.")

	// boom-zp2s: the per-DOMAIN admin surfaces moved OUT of this god-package —
	// catalyst-books (/api/v1/admin/books/*) to internal/books/admin (mounted via
	// books.Module.RegisterAdminRoutes), and boomtime's label-image regeneration
	// (/api/v1/admin/label-images/* + the public /api/v1/labels/:id/image) plus the
	// wakatime.com import cluster (/import/*) to internal/boomtime/admin (mounted via
	// boomtime.Module.RegisterRoutes). Route strings + gating are byte-identical; this
	// package now imports no data domain.

	// catalyst-go-jobs admin (boom-hney.2): the WHOLE jobs-admin HTTP surface
	// (list/queues/schedules/trigger/retry/cancel + per-job & bulk log clear) now
	// lives in the jobs package as a PORTABLE plugin — jobs.RegisterAdminRoutes.
	// This is the thin host mount: it owns the URL prefix + route-level CapAdmin
	// middleware, and injects the boomtime seam via jobs.Deps — the live job
	// subsystem accessors (wired AFTER this runs, hence functions), boomtime's
	// requireAdmin as the in-handler guard, and the logger. The plugin never
	// imports boomtime auth. Route strings + behavior are byte-identical to the
	// pre-move set (503 when the subsystem isn't wired).
	if h != nil && h.DB != nil {
		jobsCap := apihelpers.RequireCap(h.DB, auth.CapAdmin, "view admin jobs")
		g := e.Group("/api/v1/admin/jobs", jobsCap)
		jobs.RegisterAdminRoutes(g, jobs.Deps{
			Store:    func() *jobs.Store { return h.JobStore },
			Enqueuer: func() jobs.Enqueuer { return h.JobEnqueuer },
			Registry: func() *jobs.Registry { return h.JobRegistry },
			ObjStore: func() objstore.Store { return h.JobLogStore },
			// Adapt boomtime's requireAdmin (CapAdmin + IsAdmin) to the plugin's
			// plain-error guard seam. The returned *apierr.Error keeps its exact
			// JSON shape via the plugin's guardErr.
			Guard: func(c *echo.Context) (string, error) {
				owner, aerr := h.requireAdmin(c)
				if aerr != nil {
					return "", aerr
				}
				return owner, nil
			},
			Logger: h.Logger,
		})
	}

	// boom-metrics: generic in-memory rate-metric registry snapshot (router
	// request rates + per-kind job rate-limiter + external-API call rates).
	// requireAdmin in-handler; CapAdmin route middleware for defense-in-depth,
	// same posture as the jobs cluster. Nil-safe for the OpenAPI drift router.
	if h != nil && h.DB != nil {
		metricsCap := apihelpers.RequireCap(h.DB, auth.CapAdmin, "view admin metrics")
		apiroute.GET(e, "/api/v1/admin/metrics", h.AdminMetrics, metricsCap).
			Doc("Prometheus registry snapshot",
				"Gathers the SAME in-process Prometheus registry that /metrics exposes "+
					"to the cluster scrape and flattens it for the admin Metrics tab: one "+
					"entry per metric family (name, help, type) with its per-label-set "+
					"samples. Counters, gauges and untyped metrics set `value`; histograms "+
					"and summaries set `count` and `sum` instead (the UI derives an average "+
					"from sum/count) and leave `value` unset. Families come back sorted by "+
					"name. Optional `names` query param filters to families matching any of "+
					"a comma-separated list of name PREFIXES (e.g. names=http_,jobs_); "+
					"omitted or empty returns every family. The registry is process-global "+
					"and in-memory, so in a multi-pod deployment this describes only the pod "+
					"that served the request — Prometheus/Grafana remain the cross-pod "+
					"truth. Read-only and admin-gated.")
	}

	// Admin CLI-runner (BOOM_FEATURE_ADMIN_CLI, default off): flag off ⇒
	// the routes are NEVER registered, so the endpoints 404 like any
	// unknown path — the feature is fully inert. Flag on ⇒ every route is
	// double-gated: CapAdmin route middleware (defense-in-depth; inert
	// until BOOM_FEATURE_USER_MODEL) + requireAdmin inside each handler
	// (the BOOM_ADMIN_USERS hard gate, which runs before any body read).
	// The nil-guard mirrors importCap: the OpenAPI drift router registers
	// with a nil handler to enumerate paths and must not dereference h.
	if h != nil && h.Cfg != nil && h.Cfg.FeatureAdminCLI {
		cliCap := apihelpers.RequireCap(h.DB, auth.CapAdmin, "use the admin CLI runner")
		// Typed seam. run + complete use POSTNoBody, not POST: each keeps its own
		// larger body cap (64 KiB / 16 KiB vs the seam's fixed 4 KiB) AND binds
		// only AFTER requireAdmin, which is guard-stack step 3 above. See the
		// TYPED SEAM NOTE on each handler.
		apiroute.GET(e, "/api/v1/admin/cli/spec", h.CLISpec, cliCap).
			Doc("Runnable command catalog",
				"The introspected spec for every command the web CLI-runner will "+
					"accept — the full command path, short/long help, classification "+
					"(readonly | mutating | destructive, though destructive is refused at "+
					"run time), whether it supports dry-run, and every parameter's type "+
					"(bool | string | int | stringSlice | enum), default, enum values, "+
					"positional/required/secret flags and completability. A command "+
					"appears only if it is in the vetted climeta "+
					"registry AND carries the web annotation AND is available under the "+
					"current config; nothing outside that intersection is visible, let "+
					"alone runnable. The FE renders this as the command palette, so it is "+
					"the authoritative list of what /api/v1/admin/cli/run will execute. "+
					"Admin-gated. The whole /api/v1/admin/cli/* cluster is registered only "+
					"when BOOM_FEATURE_ADMIN_CLI is on — with the flag off these paths 404 "+
					"like any unknown route.")
		apiroute.POSTNoBody(e, "/api/v1/admin/cli/run", h.CLIRun, cliCap).
			Doc("Run an allowlisted command",
				"Executes ONE allowlisted command synchronously and IN-PROCESS — it "+
					"calls the same Go function the cobra RunE calls, so no subprocess is "+
					"spawned and no argv is ever built from user input — and returns the "+
					"captured output. Body is {command, flags, confirm}: `flags` carries "+
					"every parameter keyed by name, positional arguments included (the "+
					"binder routes them by the spec's Positional marker), and unknown keys "+
					"are rejected. Mutating commands DEFAULT TO DRY-RUN; actually applying "+
					"one requires `confirm` to equal the command path, otherwise 400. An "+
					"unknown, unavailable or unannotated command is indistinguishably 404. "+
					"A run is bounded at 5 minutes and its output at 64 KiB (truncation is "+
					"marked inline in `output`). A command that FAILS is still a 200: `ok` "+
					"is false and `exitError` carries the message, mirroring a non-zero "+
					"CLI exit. Request bodies are capped at 64 KiB. Admin-gated, and every "+
					"run and refusal is audit-logged with secret-marked flags masked.")
		apiroute.POSTNoBody(e, "/api/v1/admin/cli/complete", h.CLIComplete, cliCap).
			Doc("Command argument autocomplete",
				"Suggestions for one allowlisted command's positional argument or flag "+
					"value, produced by calling the registry's cobra completion functions "+
					"directly (never cobra's hidden __complete dispatch). Body is {command, "+
					"args, flag, toComplete}: when `flag` is set that flag's completer "+
					"runs, otherwise the command's positional completer runs with `args` as "+
					"the prior positional values, so contextual completers behave exactly "+
					"as they would under a shell <TAB>. Returns the suggestions (value + "+
					"optional description) plus cobra's ShellCompDirective decoded into "+
					"named booleans (noFileComp, noSpace, noSort, keepOrder, error). A parameter "+
					"with no completer yields an empty list — the spec's Completable=false "+
					"already tells the FE not to ask. Unknown or unavailable command is "+
					"404. Request bodies are capped at 16 KiB. Admin-gated; a panicking "+
					"completer is recovered and surfaces as an empty result with the error "+
					"directive.")
		// Streaming twin of /cli/run (boom-hney.5). Cookie-authed + admin-gated
		// in-handler like the other WS routes (a WS handshake can't carry the
		// cap middleware's header), so no cliCap here.
		//
		// apiroute.WebSocket records "101, no body" — documenting a handshake as
		// a JSON 200 would be wrong, not merely vague.
		apiroute.WebSocket(e, "/api/v1/admin/cli/run/ws", h.CLIRunWS).
			Doc("Streaming command run",
				"WebSocket twin of POST /api/v1/admin/cli/run: the operator WATCHES a "+
					"long command (e.g. a backfill across every user) instead of waiting "+
					"on a synchronous buffer. Answers 101 Switching Protocols with no HTTP "+
					"body. The client sends exactly ONE run-request frame — the same "+
					"{command, flags, confirm} shape as the sync route — within 10 seconds "+
					"or the socket is closed; it then receives JSON frames: `start` "+
					"(command, dryRun), any number of `output` frames carrying chunks as "+
					"the command runs, and a terminal `done` (exitError, durationMs, "+
					"truncated). A run refused by the allowlist or the confirm gate arrives "+
					"as a single `error` frame followed by a policy-violation close. Same "+
					"allowlist, dry-run/confirm gating and audit log as the sync route, and "+
					"still zero-subprocess; the only difference is the output sink. Auth is "+
					"COOKIE-based and admin-checked in-handler because a handshake cannot "+
					"carry an Authorization header — so the CapAdmin route middleware on "+
					"the sibling routes is deliberately absent here. Output is capped at 64 "+
					"KiB and a client disconnect cancels the run.")
	}
}
