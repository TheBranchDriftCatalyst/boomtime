// Package admin is the boomtime (code/wakatime) domain's ADMIN + operator HTTP
// surface (boom-zp2s), extracted from the central internal/admin god-package so
// the boomtime domain owns its own operator endpoints — the peer of internal/books/
// admin. It carries the label-image regeneration cluster (+ the public label-image
// GET that pairs with it) and the durable wakatime.com import-job cluster.
//
// Its route prefixes are mixed (/api/v1/admin/label-images/*, the public
// /api/v1/labels/:id/image, and /import/*), so it is mounted via
// boomtime.Module.RegisterRoutes(e, deps) on the full echo instance rather than the
// /api/v1/admin group. Route strings + middleware are byte-identical to the pre-move
// registrations that lived in internal/admin/routes.go.
package admin

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Handler bundles the deps the boomtime admin endpoints read. Core deps (DB/Cfg/
// Logger) come from the Module Deps at construction; the workers/queues/job store are
// wired AFTER construction (by cmd/boomtime, via the boomtime.Module setters) once the
// features initialize — nil is a supported "feature disabled" configuration, and the
// handlers detect nil and return 503.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	// wakatime.com import cluster: the durable import-job worker + fan-out hub.
	Worker *importer.Worker
	Hub    *importer.Hub
	// label-images regeneration.
	LabelImagesWorker *labelimages.Worker
	// catalyst-go-jobs store + enqueuer — read by AdminLabelImagesStatus (the
	// BOOM_JOBS_UNIFIED per-label status poll). nil = jobs subsystem not wired.
	JobStore    *jobs.Store
	JobEnqueuer jobs.Enqueuer
}

// New constructs a boomtime-admin Handler with the shared core deps.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger) *Handler {
	return &Handler{DB: database, Cfg: cfg, Logger: logger}
}

// SetImportWorker wires the wakatime.com import-job worker + hub after construction.
func (h *Handler) SetImportWorker(w *importer.Worker, hub *importer.Hub) {
	h.Worker = w
	h.Hub = hub
}

// SetLabelImagesWorker wires the label-images worker after construction. nil is fine
// when the feature is disabled — handlers detect the nil worker and return 503.
func (h *Handler) SetLabelImagesWorker(w *labelimages.Worker) { h.LabelImagesWorker = w }

// SetJobs wires the catalyst-go-jobs Store + Enqueuer (read by the per-label
// BOOM_JOBS_UNIFIED status poll). Nil = jobs not wired; the poll degrades.
func (h *Handler) SetJobs(store *jobs.Store, enq jobs.Enqueuer) {
	h.JobStore = store
	h.JobEnqueuer = enq
}

// requireAdmin: 401 without a token, 403 when not on the admin allowlist. Returns the
// resolved owner on success. Byte-identical to the copy on the other per-domain admin
// handlers — a shared helper would need DI scaffolding bigger than the 8-line body.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}

// Register mounts the boomtime admin/operator endpoints onto e (the full echo — the
// prefixes are mixed). Route strings + middleware are byte-identical to the pre-move
// registrations in internal/admin/routes.go.
//
// Every route goes through the typed apiroute seam, so both the Go types and the
// prose land in the OpenAPI spec. Four of them use a non-"200 JSON" registrar
// because that is genuinely what they do:
//   - GET /api/v1/labels/:id/image      — Raw: c.Blob of stored image bytes.
//   - POST /api/v1/admin/label-images/regenerate — Accepted (202) with the handler
//     keeping its OWN 256 KiB bind; the seam's binding registrars cap at
//     BodyLimitSmall (4 KiB), which would 413 the FE's catalog snapshot.
//   - POST /import/wakatime-range       — WritesJSON: answers {hasData:false} on the
//     no-key/lookup-failure paths and the full 5-field AllTimeRange on success; one
//     (Resp, error) signature cannot express both without changing the bytes on one
//     of them, so the handler keeps the write and only DECLARES the type.
//   - GET /import/jobs/:id/ws           — WebSocket: 101 handshake, no body.
func Register(e *echo.Echo, h *Handler) {
	// boom-myv: PUBLIC label image bytes (no auth) — label content is fixed catalog
	// data, not per-user data. Reads do NOT check the feature flag so already-generated
	// images keep serving after a flag flip.
	apiroute.Raw(e, http.MethodGet, "/api/v1/labels/:id/image", "image/png", http.StatusOK, h.LabelImage).
		Doc("Public label archetype image",
			"Raw image bytes for one memeification label archetype. Label art is shared "+
				"catalog data — identical for every user who earns it — so this route is "+
				"PUBLIC: no token, no owner scoping. The media type served is whatever the "+
				"ComfyUI shim produced and was stored alongside the row (image/png in "+
				"practice). Cache-Control is `public, max-age=31536000, immutable`, which is "+
				"safe because the bytes for an id never change in place: the FE busts the "+
				"browser cache by appending ?v=<generated_at epoch>, and this endpoint "+
				"IGNORES that query parameter entirely. Reads deliberately skip the "+
				"BOOM_FEATURE_LABEL_IMAGES gate so images already generated keep serving "+
				"after the flag is switched off — the gate only guards writes. 400 on an "+
				"empty id, 404 when no image row exists for it.")

	// boom-myv / boom-8bz: label-images admin cluster — authed AND admin-gated
	// (requireAdmin, in-handler). Info + Regenerate + the per-label DB-queue status
	// poll (BOOM_JOBS_UNIFIED). The WS auths via the refresh_token cookie in-handler.
	apiroute.GET(e, "/api/v1/admin/label-images", h.AdminLabelImagesInfo).
		Doc("Label-image admin dashboard",
			"Feature status, the ComfyUI model + shim URL in effect, and the per-label "+
				"metadata rows behind the Admin tab's table. Carries NO image bytes — the FE "+
				"fetches those one at a time from /api/v1/labels/:id/image. `baseline` is "+
				"every label id in the DB catalog (falling back to the compiled labelcatalog "+
				"set on a brand-new DB before migrations land), so the table can render a row "+
				"per catalog id whether or not an image exists yet, and `count` is how many of "+
				"them actually have one. `broker` is always the literal \"jobs\": regeneration "+
				"runs on the catalyst-go-jobs DB queue since the RabbitMQ transport was retired "+
				"(queue depth now lives on the jobs_queue_depth{kind=\"label-image\"} metric). "+
				"Admin-only: 401 without a token, 403 when the caller is not in BOOM_ADMIN_USERS.")
	apiroute.Accepted(e, http.MethodPost, "/api/v1/admin/label-images/regenerate", h.AdminLabelImagesRegenerate).
		Doc("Enqueue label-image regeneration",
			"Queues one catalyst-go-jobs label-image job per requested entry and answers 202 "+
				"with the resulting handles — nothing is rendered inline; watch progress via "+
				"/api/v1/admin/label-images/status. Body: {entries:[{id, prompt, model?, size?, "+
				"seed?}], ids?:[...], all?:bool, truncate?:bool}. `entries` is the FE's full "+
				"catalog snapshot and is REQUIRED; either `all:true` or a non-empty `ids` array "+
				"then selects which of those entries to run (400 when neither is given, or when "+
				"the selection resolves to nothing). The body binds at 256 KiB rather than the "+
				"seam's 4 KiB default precisely because that snapshot is large. Idempotent per "+
				"label: an id that already has a queued or running job comes back with THAT "+
				"job's id and existing=true instead of double-firing ComfyUI. DESTRUCTIVE when "+
				"`all:true` AND `truncate:true` — label_images is TRUNCATEd before enqueuing, so "+
				"labels the operator deleted in the FE also drop out of the DB. Each entry's "+
				"narrative description is re-read from the DB label row at enqueue time (the "+
				"Admin tab saves before regenerating), never taken from the wire body. 503 when "+
				"the feature is off or the jobs subsystem is unwired. Admin-only.")
	apiroute.GET(e, "/api/v1/admin/label-images/status", h.AdminLabelImagesStatus).
		Doc("Per-label regeneration status",
			"The latest label-image job per label from the catalyst-go-jobs queue, mapped to "+
				"the vocabulary the Admin tab renders: status is one of queued | running | done "+
				"| error (the queue's 'failed' is reported as 'error'). The Admin tab polls this "+
				"while a regeneration is in flight. Finished jobs AGE OUT of the list so a badge "+
				"clears instead of showing forever on every label ever regenerated — done "+
				"disappears after 5 minutes, error after 15; queued/running are always listed. "+
				"Returns {jobs: []} rather than an error when the jobs subsystem is not wired, so "+
				"the FE degrades to \"nothing in flight\". Admin-only.")

	// Durable, resumable wakatime.com import jobs. auth-dry Phase 2: starting an import
	// is gated by CapImport route middleware (importCap); the other endpoints use the
	// shared bearer-token flow, and the WS uses the refresh_token cookie.
	// BodyLimitNone: this bound with plain c.Bind before the seam, and
	// import_cluster_test pins that deliberately so adding a cap stays an
	// explicit decision with its own test, not a refactor side effect.
	apiroute.POSTLimit(e, "/import", apiroute.BodyLimitNone, h.ImportRequest, importCap(h)...).
		Doc("Start a wakatime.com import",
			"Creates and immediately starts a durable, resumable import job for the caller and "+
				"returns {jobId, jobStatus, job} — bind a WebSocket to /import/jobs/{id}/ws with "+
				"that jobId to watch it run. ONE ACTIVE JOB PER OWNER: when a job is already "+
				"queued or running for this user, THAT job is returned (same 200, same shape) "+
				"rather than a second one being started. Body: {apiToken?, startDate, endDate}. "+
				"apiToken is the RAW wakatime.com api_key exactly as copied from wakatime.com — "+
				"the server does the single Basic base64 encode, so a client that pre-encodes it "+
				"causes an upstream 401. When it is blank the server falls back first to the "+
				"caller's previously-saved encrypted key and then to the server-wide key. A typed "+
				"key is NOT persisted here: it travels with the job and is only saved once the run "+
				"finishes without seeing a wakatime.com 401 (boom-6jm.8), so a mistyped key never "+
				"reaches disk. The request body is deliberately UNCAPPED — that predates the typed "+
				"seam and is pinned by its own test. Requires the `import` capability.")
	apiroute.GET(e, "/import/config", h.ImportConfig).
		Doc("Import key availability",
			"Reports {hasServerKey} — whether this server has a wakatime.com API key configured. "+
				"The import form uses it to decide whether the token field may be left blank. The "+
				"key itself is never returned, and nothing beyond this boolean hints at its value.")
	apiroute.WritesJSON[importer.AllTimeRange](e, http.MethodPost, "/import/wakatime-range", h.WakatimeRange).
		Doc("wakatime.com data-range probe",
			"Discovers how far back the caller's wakatime.com history goes so the import form can "+
				"pre-fill its date range. The body {apiToken?} is OPTIONAL and a malformed body is "+
				"deliberately IGNORED rather than rejected; a blank or absent token falls back to "+
				"the server-configured key. TWO SHAPES, both 200: on success the full range "+
				"documented here, and when there is no effective key OR the upstream lookup fails, "+
				"the degenerate {\"hasData\": false} and nothing else. Those two failure paths are "+
				"deliberately indistinguishable and never surface an error status — any other shape "+
				"would reveal whether a server key exists. Upstream is called ONCE with a 15-second "+
				"timeout to keep the form responsive.")
	apiroute.GET(e, "/import/jobs", h.ImportJobs).
		Doc("Import job history",
			"Every import job belonging to the caller as {jobs}, newest first (descending id). "+
				"Owner-scoped — another user's jobs are never returned. Unpaginated: the full "+
				"history comes back in one response.")
	apiroute.GET(e, "/import/jobs/:id", h.ImportJob).
		Doc("Import job with log snapshot",
			"One job plus the first 1000 lines of its log as {job, logs} — the page-load "+
				"counterpart to the WebSocket stream. Owner-scoped, and a job id that does not "+
				"exist and one belonging to another user are BOTH a 404, so the endpoint never "+
				"reveals that someone else's job exists; a non-integer id is a 400. The 1000-line "+
				"cap is a snapshot rather than a page — tail past it with "+
				"/import/jobs/{id}/logs?afterId=.")
	apiroute.POSTNoBody(e, "/import/jobs/:id/cancel", h.ImportJobCancel).
		Doc("Cancel an import job",
			"Stops a running import and returns {job} as it stands AFTER the cancellation is "+
				"durable. Takes no request body. When the job is running on this instance the call "+
				"waits for the worker's terminal DB write so the returned state is never stale, "+
				"bounded at 5 seconds so a wedged goroutine cannot hold the response open "+
				"(client disconnect still wins). When it is merely queued, or already finished, the "+
				"cancel is applied straight to the DB and returns at once — cancelling a job that "+
				"already completed is a harmless no-op that simply echoes its current state. "+
				"Owner-scoped: 404 for an unknown or foreign id, 400 for a non-integer one.")
	apiroute.GET(e, "/import/jobs/:id/logs", h.ImportJobLogs).
		Doc("Import job log tail",
			"The REST fallback for the WebSocket stream: up to 1000 log lines for one job as "+
				"{logs}. `afterId` (query, default 0) returns only lines with a greater id, so a "+
				"poller advances by passing back the last id it saw; ids are monotonic, which is "+
				"also how the WS client de-duplicates its live tail against its snapshot. "+
				"Owner-scoped: 404 for an unknown or foreign job id, 400 for a non-integer one.")
	apiroute.WebSocket(e, "/import/jobs/:id/ws", h.ImportJobWS).
		Doc("Live import job stream",
			"Upgrades to a WebSocket carrying one job's progress — 101 Switching Protocols with "+
				"no HTTP body, so Swagger's Try-it-out cannot exercise it. Auth is the HttpOnly "+
				"refresh_token COOKIE, not the Authorization header, because a browser cannot set "+
				"headers on a WS handshake; an absent cookie is reported exactly like an expired "+
				"one. On connect the server writes a single {type:\"snapshot\", job, logs} frame "+
				"(up to 1000 lines read from the DB — this is what makes reload and resume work) "+
				"and then streams live frames whose type is \"log\", \"progress\" or \"state\". "+
				"The subscription is opened BEFORE the snapshot is read so no event can be lost "+
				"between them; the client de-duplicates on the monotonic log-line id. The server "+
				"closes as soon as the job reaches a terminal state (completed/failed/cancelled), "+
				"and closes immediately after the snapshot when the job was already terminal. "+
				"Owner-scoped: 404 for an unknown or foreign job id.")
}

// importCap returns the CapImport route middleware, or nil when h is nil. The nil case
// exists only for the OpenAPI drift router, which registers routes with a zero handler
// to enumerate paths and never serves them — so h.DB must not be dereferenced then.
func importCap(h *Handler) []echo.MiddlewareFunc {
	if h == nil {
		return nil
	}
	return []echo.MiddlewareFunc{apihelpers.RequireCap(h.DB, auth.CapImport, "import")}
}
