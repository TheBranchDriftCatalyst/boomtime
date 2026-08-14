// Package openapi builds and serves the OpenAPI 3 description of boomtime's
// HTTP API plus a self-contained interactive explorer UI.
//
// Design (see gaka-lfc):
//   - Approach B — hand-authored openapi3.T built via kin-openapi. The spec is
//     centralized in this file; response schemas are reflected from
//     internal/model/*.go via openapi3gen so schema drift is impossible unless
//     the wire struct itself changes. Path/method/tag entries are hand-listed
//     to mirror internal/server/server.go's routing tables 1:1. A drift-guard
//     test (internal/openapi/spec_test.go + a router-cross-check in the server
//     integration tests) fails the build if a registered route lacks a spec
//     path.
//   - No swaggo annotations on handlers → no codegen step, no generated files
//     in git, no cross-file bookkeeping. One dense builder is easier to review
//     than annotations sprinkled across 15 handler files.
//   - No CDN, no external assets at runtime: the UI is the reference Swagger
//     UI, vendored via the github.com/swaggo/files/v2 Go module (which embeds
//     the swagger-api/swagger-ui dist/ bundle). See ui.go.
//
// Auth model exposed to Swagger's "Try it out":
//   - bearerAuth — Authorization: Basic <base64 access token>. Boomtime
//     historically speaks the wakatime scheme (Basic-prefixed access token, not
//     RFC7617 basic-auth), so we document that as an apiKey-in-header rather
//     than HTTP-bearer to prevent Swagger UI from re-encoding the value.
//   - refreshCookie — the HttpOnly refresh_token cookie used by /auth/*,
//     /auth/users/current, and the import job WS handshake.
//
// A subset of endpoints is public (auth-less): badges, widgets/svg,
// /auth/login, /auth/register, /api/openapi.json, /api/docs. Everything else
// requires bearerAuth (or refreshCookie where applicable).
package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
	"github.com/labstack/echo/v5"
)

// Version of the OpenAPI document itself (independent of the app version).
const docVersion = "1.0.0"

const (
	tagAuth        = "Auth"
	tagHeartbeats  = "Heartbeats"
	tagExplorer    = "Heartbeats Explorer"
	tagCuration    = "Curation"
	tagSpaces      = "Spaces"
	tagStats       = "Stats"
	tagProjects    = "Projects"
	tagLeaderboard = "Leaderboards"
	tagCommits     = "Commits"
	tagBadges      = "Badges"
	tagWidgets     = "Widgets"
	tagImport      = "Import"
	tagBackup      = "Backup"
	tagLogs        = "Logs"
	tagMeta        = "Meta"
	tagDerived     = "Derived Data"
	tagSources     = "Sources"
	tagDocs        = "Docs"
	tagProfile     = "Public Profile"
	tagIntegration = "Integrations"
	tagGoals       = "Goals"
	tagAwards      = "Awards"
	tagWorkouts    = "Workouts + Health"
	tagAvatar      = "Avatar"
	tagAdmin       = "Admin"
)

var (
	specMu    sync.Mutex
	specDoc   *openapi3.T
	specJSON  []byte
	specErr   error
	specBuilt bool
	// specFor records which router produced the cached doc. When it differs
	// from routerEcho the cache is stale and Spec rebuilds. Nil is a valid
	// value (build without the auto-derive pass).
	specFor *echo.Echo
	// routerEcho is the echo instance whose registered routes feed the
	// auto-derive post-pass (see build). Set via setRouterEcho from Register;
	// nil until a router registers, in which case Spec emits the hand-authored
	// operations only.
	routerEcho *echo.Echo
)

// Spec builds and returns the OpenAPI document + its JSON encoding. The
// document is fully self-contained: no external $refs, no CDN URLs. It is safe
// to call Spec concurrently.
//
// The build is cached and single-flighted per registered router: in production
// Register wires exactly one echo instance at startup, so Spec builds once and
// every subsequent call returns the cached bytes. When the registered router
// changes (only the tests do this — each drift/handler test stands up its own
// echo) the next Spec call rebuilds so the emitted paths track the live route
// table.
func Spec() (*openapi3.T, []byte, error) {
	specMu.Lock()
	defer specMu.Unlock()
	if specBuilt && specFor == routerEcho {
		return specDoc, specJSON, specErr
	}
	doc, err := build(routerEcho)
	switch {
	case err != nil:
		specDoc, specJSON, specErr = nil, nil, err
	default:
		b, mErr := json.Marshal(doc)
		if mErr != nil {
			specDoc, specJSON, specErr = nil, nil, mErr
		} else {
			specDoc, specJSON, specErr = doc, b, nil
		}
	}
	specBuilt, specFor = true, routerEcho
	return specDoc, specJSON, specErr
}

// setRouterEcho records the echo instance whose registered routes feed the
// auto-derive pass in build, and invalidates any cached spec so the next Spec
// call reflects the new router. Called from Register.
func setRouterEcho(e *echo.Echo) {
	specMu.Lock()
	routerEcho = e
	specBuilt = false
	specMu.Unlock()
}

// build assembles the openapi3.T. Everything is inline here (paths + tags +
// components) so a single sweep captures the shape of the whole API.
//
// After the hand-authored operations, an auto-derive post-pass (option A,
// gaka-lfc) walks e's registered routes and stubs any (method, path) that
// isn't explicitly documented — so a new route satisfies the drift guard
// without a hand-written doc.AddOperation entry. When e is nil (Spec called
// before any router registers, e.g. schema-only unit tests) the pass is
// skipped and only the hand-authored operations are emitted.
func build(e *echo.Echo) (*openapi3.T, error) {
	doc := &openapi3.T{
		OpenAPI: "3.0.3",
		Info: &openapi3.Info{
			Title:       "boomtime API",
			Description: "Self-hosted wakatime-compatible time-tracking API. All timestamps are UTC RFC3339 unless noted. Response payloads mirror the exact hakatime wire shapes so existing wakatime tooling works unmodified.",
			Version:     docVersion,
			License:     &openapi3.License{Name: "The Unlicense (public domain)", URL: "https://unlicense.org/"},
		},
		Servers: openapi3.Servers{
			// Empty URL = "same origin as the doc"; the UI defaults to it so
			// "Try it out" hits the running instance with zero config.
			{URL: "/", Description: "This instance"},
		},
		Tags: openapi3.Tags{
			{Name: tagHeartbeats, Description: "Wakatime-compatible heartbeat ingest."},
			{Name: tagExplorer, Description: "Read-only heartbeat audit views (grouping, listing, source health)."},
			{Name: tagAuth, Description: "Login, registration, refresh, API-token management."},
			{Name: tagStats, Description: "Dashboard aggregations (stats/timeline/punchcard/sessions/momentum/statusbar)."},
			{Name: tagProjects, Description: "Per-project statistics."},
			{Name: tagLeaderboard, Description: "Global cross-user leaderboards."},
			{Name: tagCuration, Description: "Query-time hide / rename rules across the heartbeat axes."},
			{Name: tagSpaces, Description: "Named scoped dashboards (Space = axis-based inclusion rules)."},
			{Name: tagCommits, Description: "GitHub commit report annotated with attributed coding time."},
			{Name: tagBadges, Description: "Shields.io-proxied project time badges."},
			{Name: tagWidgets, Description: "Embeddable widget SVGs; authenticated link CRUD + public SVG renderer."},
			{Name: tagImport, Description: "Durable, resumable wakatime.com import jobs."},
			{Name: tagBackup, Description: "Whole-database dump + restore (destructive; single-flight)."},
			{Name: tagLogs, Description: "Server process log tail (REST + WebSocket)."},
			{Name: tagDerived, Description: "Precomputed gap_seconds / hb_rollup_daily health + rebuild."},
			{Name: tagSources, Description: "Ingestion source health (per plugin/editor/machine last check-in)."},
			{Name: tagMeta, Description: "Build/version disclosure + embedded changelog."},
			{Name: tagDocs, Description: "This document and the embedded interactive explorer."},
			{Name: tagProfile, Description: "Opt-in public read-only profile page (owner CRUD + public slug view)."},
			{Name: tagIntegration, Description: "External-service credential management (encrypted-at-rest)."},
			{Name: tagGoals, Description: "User-defined composite goals (predicate tree over time-on-axis / streak / active-days)."},
		},
	}

	comps := &openapi3.Components{
		Schemas:         openapi3.Schemas{},
		SecuritySchemes: openapi3.SecuritySchemes{},
		Responses:       openapi3.ResponseBodies{},
		Parameters:      openapi3.ParametersMap{},
	}
	doc.Components = comps

	// -- Security schemes -----------------------------------------------------
	//
	// bearerAuth models the wakatime-style "Authorization: Basic <token>"
	// header (base64(uuid) access token). We use apiKey-in-header rather than
	// http/bearer so the UI passes the token through verbatim (Basic-prefixed).
	comps.SecuritySchemes["bearerAuth"] = &openapi3.SecuritySchemeRef{
		Value: &openapi3.SecurityScheme{
			Type:        "apiKey",
			In:          "header",
			Name:        "Authorization",
			Description: "Wakatime-compatible token. Send verbatim as `Authorization: Basic <base64 access token>`. Mint via POST /auth/create_api_token or the /auth/login response.",
		},
	}
	comps.SecuritySchemes["refreshCookie"] = &openapi3.SecuritySchemeRef{
		Value: &openapi3.SecurityScheme{
			Type:        "apiKey",
			In:          "cookie",
			Name:        "refresh_token",
			Description: "HttpOnly refresh cookie set by /auth/login|register|refresh_token. Used by /auth/refresh_token, /auth/logout, /auth/users/current, and the import job WebSocket handshake.",
		},
	}

	// Default security: bearerAuth. Every operation may override with an empty
	// []SecurityRequirement{} to mark itself as public.
	doc.Security = openapi3.SecurityRequirements{{"bearerAuth": []string{}}}

	// -- Reusable schemas -----------------------------------------------------
	gen := openapi3gen.NewGenerator(openapi3gen.UseAllExportedFields())
	// Force well-known named schemas so refs read cleanly in the UI.
	register := func(name string, sample any) {
		ref, err := gen.NewSchemaRefForValue(sample, comps.Schemas)
		if err != nil {
			// A schema that fails reflection is a programming error; bake it
			// in as an empty object so the spec still validates.
			comps.Schemas[name] = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
			return
		}
		comps.Schemas[name] = ref
	}
	register("APIError", model.APIErrorData{})
	register("LoginResponse", model.LoginResponse{})
	register("AuthRequest", model.AuthRequest{})
	register("TokenResponse", model.TokenResponse{})
	register("TokenMetadata", model.TokenMetadata{})
	register("UserStatusResponse", model.UserStatusResponse{})
	register("StoredApiToken", model.StoredApiToken{})
	register("HeartbeatPayload", model.HeartbeatPayload{})
	register("BulkHeartbeatData", model.BulkHeartbeatData{})
	register("StatsPayload", model.StatsPayload{})
	register("TimelinePayload", model.TimelinePayload{})
	register("StatusBarPayload", model.StatusBarPayload{})
	register("PunchcardPayload", model.PunchcardPayload{})
	register("SessionsPayload", model.SessionsPayload{})
	register("MomentumPayload", model.MomentumPayload{})
	register("ActiveFilesPayload", model.ActiveFilesPayload{})
	register("ProjectStatistics", model.ProjectStatistics{})
	register("ProjectListPayload", model.ProjectListPayload{})
	register("LeaderboardsPayload", model.LeaderboardsPayload{})
	register("CommitReport", model.CommitReport{})
	register("BadgeResponse", model.BadgeResponse{})
	register("WidgetLinkResponse", model.WidgetLinkResponse{})
	register("ImportRequestPayload", model.ImportRequestPayload{})
	register("ImportRequestResponse", model.ImportRequestResponse{})

	// -- Reusable parameters --------------------------------------------------
	strParam := func(name, in, desc string, required bool) *openapi3.ParameterRef {
		p := &openapi3.Parameter{
			Name:        name,
			In:          in,
			Description: desc,
			Required:    required,
			Schema:      &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}
		return &openapi3.ParameterRef{Value: p}
	}
	intParam := func(name, in, desc string, required bool) *openapi3.ParameterRef {
		p := &openapi3.Parameter{
			Name:        name,
			In:          in,
			Description: desc,
			Required:    required,
			Schema:      &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
		}
		return &openapi3.ParameterRef{Value: p}
	}
	dateTimeParam := func(name, desc string) *openapi3.ParameterRef {
		p := &openapi3.Parameter{
			Name:        name,
			In:          "query",
			Description: desc,
			Schema:      &openapi3.SchemaRef{Value: openapi3.NewDateTimeSchema()},
		}
		return &openapi3.ParameterRef{Value: p}
	}

	comps.Parameters["QueryStart"] = dateTimeParam("start", "RFC3339 UTC start of the query range. Together with `end` selects the reported window; omit both for a default trailing window.")
	comps.Parameters["QueryEnd"] = dateTimeParam("end", "RFC3339 UTC end of the query range.")
	comps.Parameters["QueryTimeLimit"] = intParam("timeLimit", "query", "Gap cutoff in minutes for attributed time (default 15).", false)
	comps.Parameters["QuerySpace"] = intParam("space", "query", "Optional Space id to scope the dashboard by that Space's inclusion rules.", false)
	comps.Parameters["QueryDays"] = intParam("days", "query", "Trailing window in days (default varies per endpoint).", false)
	comps.Parameters["QueryTheme"] = strParam("theme", "query", "SVG theme (`dark`|`light`, else server default).", false)

	// -- Reusable responses ---------------------------------------------------
	//
	// Refs into components (schemas + responses) carry BOTH `Ref` (for the
	// emitted JSON — kin-openapi's marshaler prefers Ref over Value) and
	// `Value` (so doc.Validate can chase the ref without a separate loader
	// pass). The Value pointer is shared, not copied, so future schema edits
	// under `components.schemas` are visible through every ref that pointed at
	// them.
	apiErrSchemaRef := comps.Schemas["APIError"]
	if apiErrSchemaRef == nil {
		apiErrSchemaRef = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
	}
	errResp := func(desc string) *openapi3.ResponseRef {
		content := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{
			Ref:   "#/components/schemas/APIError",
			Value: apiErrSchemaRef.Value,
		})
		descPtr := desc
		return &openapi3.ResponseRef{Value: &openapi3.Response{Description: &descPtr, Content: content}}
	}
	noContent := func(desc string) *openapi3.ResponseRef {
		descPtr := desc
		return &openapi3.ResponseRef{Value: &openapi3.Response{Description: &descPtr}}
	}
	comps.Responses["ErrBadRequest"] = errResp("Bad request — malformed body or query.")
	comps.Responses["ErrUnauthorized"] = errResp("Missing Authorization header or refresh_token cookie.")
	comps.Responses["ErrForbidden"] = errResp("Invalid credentials or expired/unknown token.")
	comps.Responses["ErrNotFound"] = errResp("Resource not found or not owned by requester.")
	comps.Responses["ErrConflict"] = errResp("State conflict (name exists, restore in progress, active import).")
	comps.Responses["ErrTooLarge"] = errResp("Upload exceeds the configured maximum size.")
	comps.Responses["ErrInternal"] = errResp("Unhandled internal error.")
	comps.Responses["NoContent"] = noContent("No content.")

	// ---- Helpers to build ops -----------------------------------------------
	//
	// AddResponse takes *Response (wraps in a Value ref); we want the option of
	// $ref-ing shared responses, so we manipulate op.Responses directly via
	// .Set() and take *ResponseRef throughout. setResp guarantees op.Responses
	// is initialized before the first .Set.
	setResp := func(op *openapi3.Operation, code string, ref *openapi3.ResponseRef) {
		if op.Responses == nil {
			op.Responses = openapi3.NewResponses()
		}
		op.Responses.Set(code, ref)
	}

	// r constructs a Response whose JSON body is a $ref into
	// components.schemas. Populates both Ref and Value on the SchemaRef (see
	// the errResp note above about why we do this dual-population dance).
	r := func(desc, schemaRef string) *openapi3.ResponseRef {
		var val *openapi3.Schema
		if s, ok := comps.Schemas[schemaRef]; ok && s != nil {
			val = s.Value
		}
		content := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{
			Ref: "#/components/schemas/" + schemaRef, Value: val,
		})
		return &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc, Content: content}}
	}
	// rInline: response for an inline (non-schema-ref) object; used for the
	// handful of ad-hoc {"foo": ...} handlers that aren't a model.* type.
	rInline := func(desc string, schema *openapi3.Schema) *openapi3.ResponseRef {
		content := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Value: schema})
		return &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc, Content: content}}
	}
	// rBlob: response for a non-JSON media type (svg, changelog markdown, zip).
	rBlob := func(desc, mediaType string) *openapi3.ResponseRef {
		content := openapi3.Content{
			mediaType: &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithFormat("binary")}},
		}
		return &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc, Content: content}}
	}
	// stdErrors attaches the standard 400/401/403/500 error refs.
	// Populates Value (from comps.Responses[<key>]) so doc.Validate can chase
	// the ref without an external loader (Ref alone would fail with
	// "unresolved ref").
	stdErrors := func(op *openapi3.Operation, statuses ...string) {
		mp := map[string]string{
			"400": "ErrBadRequest",
			"401": "ErrUnauthorized",
			"403": "ErrForbidden",
			"404": "ErrNotFound",
			"409": "ErrConflict",
			"413": "ErrTooLarge",
			"500": "ErrInternal",
		}
		for _, s := range statuses {
			if key, ok := mp[s]; ok {
				var val *openapi3.Response
				if r := comps.Responses[key]; r != nil {
					val = r.Value
				}
				setResp(op, s, &openapi3.ResponseRef{
					Ref: "#/components/responses/" + key, Value: val,
				})
			}
		}
	}
	// setStatus adds a response for a given status code (int).
	setStatus := func(op *openapi3.Operation, status int, ref *openapi3.ResponseRef) {
		setResp(op, itoa(status), ref)
	}
	// refSchema returns a SchemaRef with both Ref (for serialization) and
	// Value (for validation) populated from comps.Schemas[name].
	refSchema := func(name string) *openapi3.SchemaRef {
		var val *openapi3.Schema
		if s, ok := comps.Schemas[name]; ok && s != nil {
			val = s.Value
		}
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name, Value: val}
	}
	// bodyJSON wires a request body of the given schema (component ref).
	// Same dual Ref+Value dance as errResp so validation resolves without a
	// separate loader pass.
	bodyJSON := func(op *openapi3.Operation, schemaRef, desc string, required bool) {
		var val *openapi3.Schema
		if s, ok := comps.Schemas[schemaRef]; ok && s != nil {
			val = s.Value
		}
		content := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{
			Ref: "#/components/schemas/" + schemaRef, Value: val,
		})
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required:    required,
			Description: desc,
			Content:     content,
		}}
	}
	// paramRef constructs a params reference from the reusable Parameters map.
	// Populates Value so validation can chase the ref (see errResp note).
	paramRef := func(name string) *openapi3.ParameterRef {
		var val *openapi3.Parameter
		if p := comps.Parameters[name]; p != nil {
			val = p.Value
		}
		return &openapi3.ParameterRef{Ref: "#/components/parameters/" + name, Value: val}
	}
	// noContentRef is the shared reference to the components-level NoContent
	// response (204). Same dual Ref+Value pattern as errResp.
	noContentRef := func() *openapi3.ResponseRef {
		var val *openapi3.Response
		if r := comps.Responses["NoContent"]; r != nil {
			val = r.Value
		}
		return &openapi3.ResponseRef{Ref: "#/components/responses/NoContent", Value: val}
	}
	// pathParamStr is a required string path parameter.
	pathParamStr := func(name, desc string) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name: name, In: "path", Required: true, Description: desc,
			Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}}
	}
	pathParamInt := func(name, desc string) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name: name, In: "path", Required: true, Description: desc,
			Schema: &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
		}}
	}
	// public wipes the default bearerAuth requirement for auth-less endpoints.
	public := openapi3.SecurityRequirements{}

	// Common inline schema helpers.
	mapObject := func() *openapi3.Schema { return openapi3.NewObjectSchema() }

	// ==== HEARTBEATS ==========================================================

	doc.AddOperation("/api/v1/users/current/heartbeats", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{
			Tags: []string{tagHeartbeats}, Summary: "Ingest one heartbeat",
			Description: "Wakatime-compatible single-heartbeat ingest. Enriches with editor/plugin/machine from the user-agent and X-Machine-Name headers.",
		}
		bodyJSON(op, "HeartbeatPayload", "One heartbeat.", true)
		setStatus(op, http.StatusAccepted, r("Stored heartbeat ids in the wakatime envelope.", "BulkHeartbeatData"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/heartbeats.bulk", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{
			Tags: []string{tagHeartbeats}, Summary: "Ingest a bulk batch of heartbeats",
			Description: "Wakatime-compatible bulk ingest. Body is an array of heartbeats.",
		}
		arr := openapi3.NewArraySchema()
		arr.Items = refSchema("HeartbeatPayload")
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "Array of heartbeats.",
			Content: openapi3.NewContentWithJSONSchema(arr),
		}}
		setStatus(op, http.StatusAccepted, r("Stored heartbeat ids in the wakatime envelope.", "BulkHeartbeatData"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== HEARTBEATS EXPLORER =================================================

	doc.AddOperation("/api/v1/users/current/heartbeats/group", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{
			Tags: []string{tagExplorer}, Summary: "Group heartbeats by an axis",
			Description: "Groups heartbeats by one whitelisted axis (project, language, editor, plugin, platform, machine, category, branch, entity, day) with accumulated equality filters.",
			Parameters: openapi3.Parameters{
				strParam("groupBy", "query", "Axis to group by (project|language|editor|plugin|platform|machine|category|branch|entity|day).", true),
				paramRef("QueryStart"), paramRef("QueryEnd"), paramRef("QueryTimeLimit"),
			},
		}
		setStatus(op, http.StatusOK, rInline("Groups with attributed seconds.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/heartbeats/latest", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{
			Tags: []string{tagExplorer}, Summary: "Most recent heartbeat timestamp",
			Description: "Returns the owner's latest heartbeat timestamp (RFC3339 UTC or null) and total count.",
		}
		setStatus(op, http.StatusOK, rInline("Latest heartbeat and total count.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/heartbeats", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{
			Tags: []string{tagExplorer}, Summary: "List raw heartbeats (paged)",
			Description: "Paged raw records with axis equality filters and an optional entity substring.",
			Parameters: openapi3.Parameters{
				paramRef("QueryStart"), paramRef("QueryEnd"),
				intParam("page", "query", "1-indexed page number (default 1).", false),
				intParam("limit", "query", "Page size (default 100, max 500).", false),
				strParam("entity", "query", "Substring filter on entity path.", false),
			},
		}
		setStatus(op, http.StatusOK, rInline("Paged heartbeat items with totals.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/sources/health", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{
			Tags: []string{tagSources}, Summary: "Per-source ingestion health",
			Description: "Each editor/plugin/machine source: last check-in and heartbeat count. Powers the Heartbeats \"Source health\" panel.",
		}
		setStatus(op, http.StatusOK, rInline("Sources with last-seen and count.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())

	// ==== CURATION ============================================================

	doc.AddOperation("/api/v1/users/current/curation", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "List hide / rename rules",
			Description: "Every rule the owner has authored, unfiltered."}
		setStatus(op, http.StatusOK, rInline("Curation rules.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Create a hide or rename rule",
			Description: "Body: {axis, action:'hide'|'rename', matchType:'exact'|'regex'|'template' (default 'exact'), matchValue, newValue?, applyAtIngest?}. Query-time rules are reversible (no raw data mutated); a rename with applyAtIngest also rewrites newly-ingested rows (irreversible for those rows) and is excluded from the query-time remap."}
		body := openapi3.NewObjectSchema()
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "Rule payload.",
			Content: openapi3.NewContentWithJSONSchema(body),
		}}
		setStatus(op, http.StatusOK, rInline("Created rule wrapped as {rule:...}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation/{id}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Delete a curation rule",
			Parameters: openapi3.Parameters{pathParamInt("id", "Rule id.")}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation/{id}/affected", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Values a rule matches",
			Description: "Distinct raw values (with counts) that this rule matches on its axis. Owner-scoped, unfiltered (audit view).",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Rule id.")}}
		setStatus(op, http.StatusOK, rInline("{values:[{value,count}], truncated}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation/{id}/preview", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Preview a destructive apply or purge of a curation rule",
			Description: "Dispatches on rule.action: a rename rule returns the apply-preview shape (UPDATE + rule-delete SQL, per-row before/after diff); a hide rule returns the purge-preview shape (DELETE heartbeats + rule-delete SQL, per-row 'will be deleted' info). The `action` field on the response is the discriminator. Owner-scoped; no data is mutated. The SQL strings returned here are identical to sqlRun on the apply/purge endpoint.",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Curation rule id.")}}
		setStatus(op, http.StatusOK, rInline("Discriminated union on `action`: {action:'rename', sqlPlanned, sqlUpdate, sqlDelete, affectedRows:[{id,before,after}], totalAffected, rowsShown, rule} | {action:'hide', sqlPlanned, sqlDeleteRows, sqlDeleteRule, affectedRows:[{id,deleted}], totalAffected, rowsShown, rule}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation/{id}/apply", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Destructively apply a rename rule",
			Description: "DESTRUCTIVE: rewrites the target column on every heartbeat row this rename rule matches, then deletes the rule row itself, atomically in one transaction. Idempotent-in-effect: if 0 rows match, still succeeds with rowsAffected=0 and removes the rule. Owner-scoped. Only rename rules are apply-able — hide rules return 400 (use /purge for those).",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Rename rule id.")}}
		setStatus(op, http.StatusOK, rInline("{rowsAffected, sqlRun, sqlUpdate, sqlDelete}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation/{id}/purge", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Destructively purge every row a hide rule matches",
			Description: "DESTRUCTIVE (data-obliterating): DELETEs every heartbeat row this hide rule matches, then deletes the rule row itself, atomically in one transaction. Idempotent-in-effect: if 0 rows match, still succeeds with rowsAffected=0 and removes the rule. Owner-scoped. Only hide rules are purge-able — rename rules return 400 (use /apply for those). The FE modal gates this behind a 'type rule id N to confirm' input because rewriting labels is reversible-ish but deleting raw rows is not.",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Hide rule id.")}}
		setStatus(op, http.StatusOK, rInline("{rowsAffected, sqlRun, sqlDeleteRows, sqlDeleteRule}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/curation/{id}/toggle", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCuration}, Summary: "Pause / resume a curation rule",
			Description: "Toggle a rule's enabled flag without deleting it. Applies to BOTH rename and hide rules — pausing a rename stops the label swap at query time; pausing a hide stops the rows-being-filtered-out at query time. The rule row survives in the list either way, so the UI can flip it back on with a single click. Body is optional: an empty POST flips the current value; {\"enabled\":true|false} sets an exact state. Both flip and set are idempotent — sending the same state twice still returns 200 with the current value. Owner-scoped. Enabling/disabling invalidates the owner's dashboard cache. Apply and purge endpoints return 400 for a disabled rule (enable it first).",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Curation rule id.")}}
		body := openapi3.NewObjectSchema()
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: false, Description: "Optional {enabled:bool}. Omit to flip.",
			Content: openapi3.NewContentWithJSONSchema(body),
		}}
		setStatus(op, http.StatusOK, rInline("{enabled:bool} — the new state.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())

	// ==== SPACES ==============================================================

	doc.AddOperation("/api/v1/users/current/spaces", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "List spaces",
			Description: "All named scoped dashboards for the owner."}
		setStatus(op, http.StatusOK, rInline("{spaces:[Space]}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Create a space",
			Description: "Body: {name}."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{name}.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{space:Space}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces/preview", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Preview a candidate space rule",
			Description: "Distinct raw values (with counts) that an unsaved membership rule would match on the given axis. Owner-scoped.",
			Parameters: openapi3.Parameters{
				strParam("axis", "query", "One of the whitelisted axes.", true),
				strParam("matchValue", "query", "Value / regex to match.", true),
				strParam("matchType", "query", "'exact' (default) or 'regex'.", false),
			}}
		setStatus(op, http.StatusOK, rInline("{values:[{value,count}], truncated}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces/{id}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Fetch one space + its rules",
			Parameters: openapi3.Parameters{pathParamInt("id", "Space id.")}}
		setStatus(op, http.StatusOK, rInline("{id,name,position,rules:[SpaceRule]}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces/{id}", "PATCH", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Rename or reorder a space",
			Description: "Body: {name?, position?}.",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Space id.")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: false, Description: "{name?, position?}.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces/{id}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Delete a space",
			Parameters: openapi3.Parameters{pathParamInt("id", "Space id.")}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces/{id}/rules", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Add a membership rule",
			Description: "Body: {axis, matchValue, matchType:'exact'|'regex'}. Owner-scoped.",
			Parameters:  openapi3.Parameters{pathParamInt("id", "Space id.")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{axis, matchValue, matchType}.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{rule:SpaceRule}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/spaces/{id}/rules/{rid}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagSpaces}, Summary: "Delete a membership rule",
			Parameters: openapi3.Parameters{pathParamInt("id", "Space id."), pathParamInt("rid", "Rule id.")}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())

	// ==== STATS / AGGREGATIONS ===============================================

	dashboardParams := openapi3.Parameters{
		paramRef("QueryStart"), paramRef("QueryEnd"), paramRef("QueryTimeLimit"), paramRef("QuerySpace"),
	}

	doc.AddOperation("/api/v1/users/current/derived/status", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagDerived}, Summary: "Derived-data health",
			Description: "gap_seconds + hb_rollup_daily status for the requesting user."}
		setStatus(op, http.StatusOK, rInline("Health snapshot.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/derived/resync", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagDerived}, Summary: "Rebuild gap_seconds + rollup",
			Description: "Rebuilds all derived tables for the requesting user, then returns the refreshed status."}
		setStatus(op, http.StatusOK, rInline("Refreshed health snapshot.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/db/export", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagBackup}, Summary: "Stream a whole-DB backup ZIP",
			Description: "Full logical dump of the entire application state as a ZIP attachment. Single-flighted with restore."}
		setStatus(op, http.StatusOK, rBlob("ZIP backup archive.", "application/zip"))
		stdErrors(op, "401", "403", "409", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/db/import", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagBackup}, Summary: "Restore from a backup ZIP",
			Description: "DESTRUCTIVE — replaces the entire application state. Requires `?confirm=replace-all-data`. Body is the ZIP archive.",
			Parameters:  openapi3.Parameters{strParam("confirm", "query", "Must equal 'replace-all-data'.", true)},
		}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "ZIP backup archive.",
			Content: openapi3.Content{
				"application/zip": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithFormat("binary")}},
			},
		}}
		setStatus(op, http.StatusOK, rInline("Restore summary.", mapObject()))
		stdErrors(op, "400", "401", "403", "409", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/stats", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Dashboard stats", Description: "Attributed time per project/language/editor/platform/machine/category, plus totals and daily series.",
			Parameters: dashboardParams}
		setStatus(op, http.StatusOK, r("StatsPayload.", "StatsPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/timeline", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Timeline",
			Description: "Language-broken-out session spans.", Parameters: dashboardParams}
		setStatus(op, http.StatusOK, r("TimelinePayload.", "TimelinePayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/statusbar/today", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Today's status-bar grand-total",
			Description: "Wakatime-compatible statusbar payload."}
		setStatus(op, http.StatusOK, r("StatusBarPayload.", "StatusBarPayload"))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/stats/punchcard", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Punchcard (DoW x hour intensity)",
			Description: "Day-of-week x hour-of-day activity intensity (UTC).", Parameters: dashboardParams}
		setStatus(op, http.StatusOK, r("PunchcardPayload.", "PunchcardPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/stats/sessions", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Sessions (summary + daily + histogram)",
			Parameters: dashboardParams}
		setStatus(op, http.StatusOK, r("SessionsPayload.", "SessionsPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/stats/momentum", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Top-N project momentum",
			Description: "Top-N projects' weekly time series.",
			Parameters: append(openapi3.Parameters{
				intParam("top", "query", "Top-N cutoff (default 8).", false),
			}, dashboardParams...)}
		setStatus(op, http.StatusOK, r("MomentumPayload.", "MomentumPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/files", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "Cross-project active files",
			Description: "Files touched across the owner's projects; lynchpins-first.",
			Parameters: append(openapi3.Parameters{
				intParam("limit", "query", "Top-N cutoff (default 20, max 100).", false),
			}, dashboardParams...)}
		setStatus(op, http.StatusOK, r("ActiveFilesPayload.", "ActiveFilesPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/projects/{project}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProjects}, Summary: "Per-project statistics",
			Parameters: append(openapi3.Parameters{
				pathParamStr("project", "Project DISPLAY name (rename remap is applied)."),
			}, dashboardParams...)}
		setStatus(op, http.StatusOK, r("ProjectStatistics.", "ProjectStatistics"))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/projects", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProjects}, Summary: "List projects",
			Description: "Owner's projects that have activity in the range.",
			Parameters:  dashboardParams}
		setStatus(op, http.StatusOK, r("ProjectListPayload.", "ProjectListPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== AUTH ================================================================

	doc.AddOperation("/auth/login", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Log in", Security: &public,
			Description: "Sets an HttpOnly refresh_token cookie and returns an access token."}
		bodyJSON(op, "AuthRequest", "Credentials.", true)
		setStatus(op, http.StatusOK, r("Access token + expiry.", "LoginResponse"))
		stdErrors(op, "400", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/register", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Register", Security: &public,
			Description: "Registers a user (if registration is enabled) and returns an access token."}
		bodyJSON(op, "AuthRequest", "Credentials.", true)
		setStatus(op, http.StatusOK, r("Access token + expiry.", "LoginResponse"))
		stdErrors(op, "400", "403", "409", "500")
		return op
	}())
	doc.AddOperation("/auth/refresh_token", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Refresh the access token",
			Description: "Uses the HttpOnly refresh_token cookie; rotates and returns a fresh access token.",
			Security:    &openapi3.SecurityRequirements{{"refreshCookie": []string{}}}}
		setStatus(op, http.StatusOK, r("Access token + expiry.", "LoginResponse"))
		stdErrors(op, "400", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/logout", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Log out",
			Description: "Requires both the Authorization access token and the refresh cookie; deletes both.",
			Security:    &openapi3.SecurityRequirements{{"bearerAuth": []string{}, "refreshCookie": []string{}}}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/create_api_token", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Mint a never-expiring API token",
			Description: "Returns the raw token; use base64(token) as `Authorization: Basic <b64>`."}
		setStatus(op, http.StatusOK, r("{apiToken:...}.", "TokenResponse"))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/tokens", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "List API tokens"}
		arr := openapi3.NewArraySchema()
		arr.Items = refSchema("StoredApiToken")
		setStatus(op, http.StatusOK, rInline("StoredApiToken[].", arr))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/token/{id}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Delete an API token",
			Parameters: openapi3.Parameters{pathParamStr("id", "Token id.")}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/token", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Rename an API token"}
		bodyJSON(op, "TokenMetadata", "{tokenId, tokenName}.", true)
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/auth/users/current", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Who am I (cookie-authed)",
			Description: "Reads the refresh_token cookie.",
			Security:    &openapi3.SecurityRequirements{{"refreshCookie": []string{}}}}
		setStatus(op, http.StatusOK, r("{data:{full_name,email,photo}}.", "UserStatusResponse"))
		stdErrors(op, "400", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/password", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAuth}, Summary: "Change password",
			Description: "Verifies currentPassword, enforces min-8/letter+digit on newPassword, re-hashes with argon2id, and revokes every refresh token for the owner (other sessions bounce)."}
		body := openapi3.NewObjectSchema()
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{currentPassword, newPassword}.",
			Content: openapi3.NewContentWithJSONSchema(body),
		}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== PUBLIC PROFILE (gaka-6jm.1) ========================================
	//
	// The `/api/v1/users/current/profile` pair is the owner-side toggle + slug
	// CRUD; `/api/public/profile/{slug}` is the auth-less renderer. Payload
	// shape for the public route is scrubbed through internal/widget.Scrub —
	// see internal/handler/profile.go for the exact security contract.

	// Reusable inline schema for the owner-side GET/PUT profile shape.
	profileToggleSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"enabled": &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
			"slug":    &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithMinLength(3).WithMaxLength(30)},
		}
		s.Required = []string{"enabled"}
		return s
	}

	doc.AddOperation("/api/v1/users/current/profile", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Get public-profile toggle + slug",
			Description: "Returns the caller's public-profile enabled flag and (nullable) slug. Owner-only."}
		setStatus(op, http.StatusOK, rInline("{enabled, slug|null}.", profileToggleSchema()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/profile", "PUT", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Update public-profile toggle + slug",
			Description: "Body: {enabled, slug}. Enabling requires a valid slug (3-30 chars, lowercase alphanumeric + hyphens, not reserved). Returns 409 if the slug is already taken."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{enabled, slug}.",
			Content: openapi3.NewContentWithJSONSchema(profileToggleSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("Persisted {enabled, slug|null}.", profileToggleSchema()))
		stdErrors(op, "400", "401", "403", "409", "500")
		return op
	}())
	doc.AddOperation("/api/public/profile/{slug}", "GET", func() *openapi3.Operation {
		// Public payload is a hand-tuned subset of StatsPayload — omit machines,
		// no *Count fields — scrubbed through widget.Scrub. Documented as an
		// inline object to reflect that it's intentionally NOT the wire shape
		// of StatsPayload (which would leak fields the scrubber drops).
		body := openapi3.NewObjectSchema()
		body.Properties = openapi3.Schemas{
			"username":     &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			"startDate":    &openapi3.SchemaRef{Value: openapi3.NewDateTimeSchema()},
			"endDate":      &openapi3.SchemaRef{Value: openapi3.NewDateTimeSchema()},
			"totalSeconds": &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
			"dailyAvg":     &openapi3.SchemaRef{Value: openapi3.NewFloat64Schema()},
			"dailyTotal": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()}
				return a
			}()},
			"projects": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
				return a
			}()},
			"languages": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
				return a
			}()},
			"editors": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
				return a
			}()},
			"platforms": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
				return a
			}()},
			"categories": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
				return a
			}()},
			"punchcard": refSchema("PunchcardPayload"),
			// gaka-keb: the owner's persisted dashboard layout, if any.
			// Omitted from the payload entirely when the owner never saved
			// a layout — the FE falls back to a default array.
			"layout": &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
		}
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Public profile dashboard (no auth)",
			Description: "Resolves slug -> user and returns a widget-scrubbed 60-day activity summary. Machines segment is omitted. Response is cached with must-revalidate for prompt privacy propagation when a user disables their profile.",
			Security:    &public,
			Parameters:  openapi3.Parameters{pathParamStr("slug", "Public profile slug (3-30 chars, lowercase alphanumeric + hyphens).")}}
		setStatus(op, http.StatusOK, rInline("Scrubbed activity summary.", body))
		stdErrors(op, "404", "500")
		return op
	}())

	// ==== AWARDS (gaka-mwp-streaks + gaka-hc6) ==============================
	//
	// Server-side award evaluation + streak ledger + historical backfill.
	// The own variants require a valid API token; the public variants resolve
	// the target user via the public profile slug and require no auth.
	// Response bodies are intentionally documented as open-ended objects
	// because the awards catalog + label spec shape evolves rapidly — the
	// FE tolerates unknown award kinds by design.

	doc.AddOperation("/api/v1/users/current/awards", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Server-side award evaluation (own)",
			Description: "Evaluates every label rule against the caller's last 60 days and returns firing awards. Writes the ledger as a side effect (streak walker reads the ledger)."}
		setStatus(op, http.StatusOK, rInline("Awards payload: {labels:[{id,name,kind,firing,...}], evaluatedAt}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/public/profile/{slug}/awards", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Server-side award evaluation (public)",
			Description: "Same shape as the own variant but resolved via the public slug. Does NOT write to the ledger — a profile viewer must not perturb streak state.",
			Security:    &public,
			Parameters:  openapi3.Parameters{pathParamStr("slug", "Public profile slug.")}}
		setStatus(op, http.StatusOK, rInline("Awards payload (public — no ledger side effect).", mapObject()))
		stdErrors(op, "400", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/awards/streaks", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Label streaks (own)",
			Description: "Streak walker output: {labelId -> {daily:N, weekly:N, monthly:N}} for the caller. TZ-aware via users.timezone."}
		setStatus(op, http.StatusOK, rInline("Streaks map keyed by labelId with per-period counts.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/public/profile/{slug}/awards/streaks", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Label streaks (public)",
			Description: "Same shape as the own variant; target user derived from the public slug.",
			Security:    &public,
			Parameters:  openapi3.Parameters{pathParamStr("slug", "Public profile slug.")}}
		setStatus(op, http.StatusOK, rInline("Streaks map keyed by labelId with per-period counts.", mapObject()))
		stdErrors(op, "400", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/awards/ledger", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Award ledger inspector (own)",
			Description: "Debug/admin view of the raw award_ledger rows with label name + kind joined. Cache-Control: private, max-age=30."}
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "label", In: "query", Description: "Filter to a single label id.",
				Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}}},
			{Value: &openapi3.Parameter{Name: "limit", In: "query", Description: "Row cap (default 500, max 500).",
				Schema: &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()}}},
		}
		setStatus(op, http.StatusOK, rInline("{rows:[{owner,labelId,name,kind,periodType,periodStart,evaluatedAt}], limit}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/awards/log", "POST", func() *openapi3.Operation {
		reqBody := openapi3.NewObjectSchema()
		reqBody.Properties = openapi3.Schemas{
			"items": &openapi3.SchemaRef{Value: func() *openapi3.Schema {
				a := openapi3.NewArraySchema()
				a.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
				return a
			}()},
			"at": &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithFormat("date-time")},
		}
		reqBody.Required = []string{"items"}
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Persist firing awards to the ledger (own)",
			Description: "FE evaluator POST after each evaluate() run. Upserts one row per (user, label, period_start). `at` is optional RFC3339 for historical backfill (rejected if in the future). Body cap: 128 KiB."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{items:[{labelId, periodType:'daily'|'weekly'|'monthly'}], at?}.",
			Content: openapi3.NewContentWithJSONSchema(reqBody),
		}}
		setStatus(op, http.StatusOK, rInline("{received:int, written:int}.", mapObject()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/awards/backfill", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAwards}, Summary: "Historical award replay (own)",
			Description: "Replays evaluate() day-by-day over the caller's history and writes the ledger. Unblocks the full deletion of the client-side evaluator. Long-running — response arrives when replay completes."}
		setStatus(op, http.StatusOK, rInline("{daysScanned:int, awardsWritten:int}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())

	// ==== HEALTHZ (gaka-lfc drift backfill — gaka-08m) =======================
	doc.AddOperation("/healthz", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagMeta}, Summary: "Liveness + DB reachability probe",
			Description: "Unauthenticated probe used by container orchestrators + uptime monitors. Returns {status,uptime,db:{ok,schema},build:{version,commit,branch,buildTime}}. Never returns 500 for DB unreachability — reports ok=false in the envelope so probes can distinguish 'process alive' from 'db up' via 200 body inspection.",
			Security:    &public}
		setStatus(op, http.StatusOK, rInline("Health probe envelope.", mapObject()))
		return op
	}())

	// ==== WORKOUTS + HEALTH SAMPLES (Apple Watch ingest — gaka-08m) =========
	//
	// Owner-scoped ingest endpoints for HealthKit data (workouts + raw
	// samples). Workouts flow through the heartbeats table (ty='workout') so
	// time-spent aggregations pick them up; raw samples land in health_samples.

	doc.AddOperation("/api/v1/users/current/workouts", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWorkouts}, Summary: "Ingest one workout (Apple Watch)",
			Description: "Single-workout POST from boomtime-watch. Persists a heartbeats row with ty='workout' plus a workout_details child. Body cap: 8 MiB."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Description: "Workout envelope: {startTime, endTime, activityType, ...HealthKit fields}.",
			Content:     openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema())}}
		setStatus(op, http.StatusAccepted, rInline("Accepted.", mapObject()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/workouts.bulk", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWorkouts}, Summary: "Ingest a batch of workouts",
			Description: "Bulk POST from boomtime-watch. Same shape as the single endpoint but takes an array. Body cap: 8 MiB."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Description: "Array of workout envelopes.",
			Content:     openapi3.NewContentWithJSONSchema(func() *openapi3.Schema { s := openapi3.NewArraySchema(); s.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}; return s }())}}
		setStatus(op, http.StatusAccepted, rInline("Accepted.", mapObject()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/workouts", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWorkouts}, Summary: "List workouts",
			Description: "Read-only view of the caller's workout history (heartbeats WHERE ty='workout' joined with workout_details)."}
		setStatus(op, http.StatusOK, rInline("{workouts:[...]}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/health_samples", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWorkouts}, Summary: "Ingest one health sample",
			Description: "Single HealthKit sample (steps, heart-rate, sleep, etc.). Deduped by (owner, type, startTime, endTime). Body cap: 8 MiB."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema())}}
		setStatus(op, http.StatusAccepted, rInline("Accepted.", mapObject()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/health_samples.bulk", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWorkouts}, Summary: "Ingest a batch of health samples",
			Description: "Bulk sample upsert. Same dedupe semantics as the single endpoint. Body cap: 8 MiB."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Content: openapi3.NewContentWithJSONSchema(func() *openapi3.Schema { s := openapi3.NewArraySchema(); s.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}; return s }())}}
		setStatus(op, http.StatusAccepted, rInline("Accepted.", mapObject()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/stats/health", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWorkouts}, Summary: "Health aggregations (Wellness card)",
			Description: "Aggregated health metrics for the dashboard Wellness card: sleep totals, avg heart rate, steps, workouts breakdown."}
		setStatus(op, http.StatusOK, rInline("Wellness payload.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/stats/ai", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagStats}, Summary: "AI-assisted coding activity breakdown",
			Description: "Per-day AI-vs-manual attribution derived from user_agent parsing (Copilot, Cursor, etc.)."}
		setStatus(op, http.StatusOK, rInline("AI activity payload.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())

	// ==== ENTITY EXPLORER (gaka-90x — drift backfill gaka-08m) ==============
	doc.AddOperation("/api/v1/users/current/heartbeats/entities", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagExplorer}, Summary: "List entities by axis",
			Description: "Per-type flat list of entities the caller has heartbeats for (?ty=file|project|...)."}
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "ty", In: "query", Required: true, Description: "Entity type (file, project, language, editor, machine, ...)",
				Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}}},
		}
		setStatus(op, http.StatusOK, rInline("{entities:[{value,count}]}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/heartbeats/entities/redact", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagExplorer}, Summary: "Redact an entity across all heartbeats",
			Description: "Blanks the entity column on matching heartbeat rows (rows remain, contributing to project/language/machine totals). Requires ?confirm=redact-entities as an accident guard. Body cap: 64 KiB."}
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "confirm", In: "query", Required: true, Description: "Must be 'redact-entities'.",
				Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}}},
		}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Description: "{ty, values:[...]}",
			Content:     openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema())}}
		setStatus(op, http.StatusOK, rInline("{rowsAffected:int}.", mapObject()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())

	// ==== AVATARS (gaka-9v4 — drift backfill gaka-08m) ======================
	doc.AddOperation("/api/v1/users/current/avatar/status", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAvatar}, Summary: "Own avatar status",
			Description: "Reports whether the caller has an avatar pending, ready, or absent."}
		setStatus(op, http.StatusOK, rInline("{status:'none'|'pending'|'ready', ...}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/avatar/regenerate", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAvatar}, Summary: "Regenerate own avatar",
			Description: "Enqueues a fresh avatar synthesis job for the caller. Idempotent — a queued/running job is returned instead of duplicating work."}
		setStatus(op, http.StatusAccepted, rInline("{jobId, existing:bool}.", mapObject()))
		stdErrors(op, "401", "403", "500", "503")
		return op
	}())
	doc.AddOperation("/api/v1/users/{username}/avatar", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAvatar}, Summary: "Public avatar image (PNG)",
			Description: "Public read of a user's avatar. Returns image/png bytes. Cached; falls back to the deterministic default when the user has none.",
			Security:    &public,
			Parameters:  openapi3.Parameters{pathParamStr("username", "Target username.")}}
		setStatus(op, http.StatusOK, rBlob("Avatar image.", "image/png"))
		stdErrors(op, "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/avatar/synthesize-prompt", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAvatar, tagAdmin}, Summary: "Admin-only: synthesize an avatar prompt from stats",
			Description: "Runs the LLM prompt-synthesis pass for a target user's stats snapshot. Admin-gated via BOOM_ADMIN_USERS."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Description: "{username, ...stats fields}",
			Content:     openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema())}}
		setStatus(op, http.StatusOK, rInline("{prompt:string}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== WIDGET DEFS + NAMED SVG (gaka-08m drift backfill) =================
	//
	// Per-user named widget templates + the public render endpoint for
	// resolving them.

	doc.AddOperation("/api/v1/users/current/widget-defs", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "List own widget definitions",
			Description: "Returns every saved widget-def (JSONB spec + name + createdAt) for the caller."}
		setStatus(op, http.StatusOK, rInline("{widgetDefs:[...]}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/widget-defs", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Create a named widget definition",
			Description: "Persists a widget spec under a caller-owned name. Names are unique per owner. Body cap: 64 KiB."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Description: "{name, spec}",
			Content:     openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema())}}
		setStatus(op, http.StatusOK, rInline("Created def wrapped as {widgetDef:...}.", mapObject()))
		stdErrors(op, "400", "401", "403", "409", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/widget-defs/{name}", "PATCH", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Update a named widget definition",
			Description: "Partial update of an existing widget def's spec. Body cap: 64 KiB.",
			Parameters:  openapi3.Parameters{pathParamStr("name", "Widget-def name (owner-scoped).")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true,
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema())}}
		setStatus(op, http.StatusOK, rInline("Updated def wrapped as {widgetDef:...}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/widget-defs/{name}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Delete a named widget definition",
			Description: "Removes the widget-def row. Existing widget-links that reference the same name will 404 on their public /widget/svg/:uuid/named until the def is recreated.",
			Parameters:  openapi3.Parameters{pathParamStr("name", "Widget-def name.")}}
		setStatus(op, http.StatusNoContent, &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: func() *string { s := "Deleted."; return &s }()}})
		stdErrors(op, "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/widget/svg/{uuid}/named", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Public: named widget SVG",
			Description: "Renders a widget-def SVG by name resolved through the caller's public widget-link. Public — no auth. Returns image/svg+xml.",
			Security:    &public,
			Parameters: openapi3.Parameters{
				pathParamStr("uuid", "Widget-link UUID."),
				{Value: &openapi3.Parameter{Name: "name", In: "query", Required: true, Description: "Widget-def name to render.",
					Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}}},
			}}
		setStatus(op, http.StatusOK, rBlob("Rendered SVG.", "image/svg+xml"))
		stdErrors(op, "400", "404", "500")
		return op
	}())

	// ==== ADMIN ============================================================

	doc.AddOperation("/api/v1/admin/users", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagAdmin}, Summary: "List users with roles + effective capabilities",
			Description: "Admin caps dashboard (gaka-93f.6): every user's role/tier + effective capabilities + disabled status, plus the role→capabilities legend. Admin-gated."}
		setStatus(op, http.StatusOK, rInline("{capabilities, roles, users}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())

	// NOTE: GET /api/v1/admin/metrics (gaka-metrics) is intentionally NOT
	// documented here. Like the /api/v1/admin/jobs cluster it is registered
	// conditionally (only when the admin handler has a live DB), so the
	// OpenAPI drift router — which wires a nil handler to enumerate paths —
	// never registers it. Adding a spec entry would trip the drift guard
	// (spec advertises a path the drift router can't register). Same pattern
	// as the jobs/cli conditionally-registered admin routes.

	// ==== DASHBOARD LAYOUTS (gaka-keb) =======================================
	//
	// Per-user, per-scope persisted layout JSON for the composable dashboard
	// grid. Scope is a small allowlist (public_profile today). Layout is
	// opaque JSONB — the FE renderer drops unknown widget-kind ids.

	layoutEnvelopeSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"layout": &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
		}
		s.Required = []string{"layout"}
		return s
	}

	doc.AddOperation("/api/v1/users/current/dashboard/{scope}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Get persisted dashboard layout for scope",
			Description: "Returns {layout: ...} on hit or 404 when the caller has not saved a layout (FE falls back to a default array). Scope allowlist: public_profile.",
			Parameters:  openapi3.Parameters{pathParamStr("scope", "Dashboard scope (public_profile).")}}
		setStatus(op, http.StatusOK, rInline("{layout:...}.", layoutEnvelopeSchema()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/dashboard/{scope}", "PUT", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Upsert dashboard layout for scope",
			Description: "Body: {layout: ...} (opaque, capped at 4 KiB). Returns the persisted envelope so the FE can settle its cache.",
			Parameters:  openapi3.Parameters{pathParamStr("scope", "Dashboard scope (public_profile).")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{layout:...}.",
			Content: openapi3.NewContentWithJSONSchema(layoutEnvelopeSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("Persisted {layout:...}.", layoutEnvelopeSchema()))
		stdErrors(op, "400", "401", "403", "413", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/dashboard/{scope}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Clear persisted dashboard layout for scope",
			Description: "Idempotent — 204 whether or not a row existed. FE reverts to its default layout for the scope.",
			Parameters:  openapi3.Parameters{pathParamStr("scope", "Dashboard scope (public_profile).")}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== GOALS (gaka-wpb) ===================================================
	//
	// Composite predicate-tree targets with per-goal + batched progress
	// endpoints. Spec is opaque JSONB validated server-side via
	// stats.ValidateSpec (kind / axis whitelists, depth<=5,
	// non-negative numbers). Progress cache lives on the row
	// (last_progress + last_evaluated_at); 60s stale-while-revalidate
	// per stats.GoalCacheTTL, invalidated eagerly on heartbeat ingest
	// and on spec change.

	goalObj := func() *openapi3.Schema { return openapi3.NewObjectSchema() }
	goalEnvelope := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{"goal": &openapi3.SchemaRef{Value: goalObj()}}
		s.Required = []string{"goal"}
		return s
	}
	goalsListEnvelope := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		arr := openapi3.NewArraySchema()
		arr.Items = &openapi3.SchemaRef{Value: goalObj()}
		s.Properties = openapi3.Schemas{"goals": &openapi3.SchemaRef{Value: arr}}
		s.Required = []string{"goals"}
		return s
	}
	goalProgressSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		// sub_conditions is a flat list of per-leaf detail objects
		// (kind + axis/value/op/window + current/target/progress/hit).
		// We describe items as opaque objects — the exact keys per
		// kind are documented in stats.SubCondition; the FE reads
		// discriminated fields on `kind`.
		arr := openapi3.NewArraySchema()
		arr.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
		s.Properties = openapi3.Schemas{
			"hit":            &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
			"progress":       &openapi3.SchemaRef{Value: openapi3.NewFloat64Schema()},
			"sub_conditions": &openapi3.SchemaRef{Value: arr},
		}
		s.Required = []string{"hit", "progress", "sub_conditions"}
		return s
	}
	batchProgressSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		mp := openapi3.NewObjectSchema()
		mp.AdditionalProperties = openapi3.AdditionalProperties{Schema: &openapi3.SchemaRef{Value: goalProgressSchema()}}
		s.Properties = openapi3.Schemas{"progress": &openapi3.SchemaRef{Value: mp}}
		s.Required = []string{"progress"}
		return s
	}
	goalCreateSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"name":        &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			"description": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			"spec":        &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
		}
		s.Required = []string{"name", "spec"}
		return s
	}
	goalPatchSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"name":        &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			"description": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			"spec":        &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()},
			"enabled":     &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
		}
		return s
	}
	toggleSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{"enabled": &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()}}
		return s
	}

	doc.AddOperation("/api/v1/users/current/goals", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "List the caller's goals",
			Description: "Newest first. Every goal row carries its spec (opaque JSONB), enabled flag, and last cached progress (may be null when the cache is empty)."}
		setStatus(op, http.StatusOK, rInline("{goals:[Goal]}.", goalsListEnvelope()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Create a goal",
			Description: "Body: {name, description?, spec}. Spec is validated strictly (kind/axis whitelists, non-negative numeric fields, recursion depth <= 5). Duplicate (owner, name) returns 409."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{name, description?, spec}.",
			Content: openapi3.NewContentWithJSONSchema(goalCreateSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{goal:Goal}.", goalEnvelope()))
		stdErrors(op, "400", "401", "403", "409", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals/progress", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Batched progress for every enabled goal",
			Description: "One round trip serves every dashboard tile — the FE calls this once per dashboard render. Disabled goals are omitted from the map. Each per-goal Progress respects the same 60s stale-while-revalidate cache the per-id endpoint uses."}
		setStatus(op, http.StatusOK, rInline("{progress: {id: Progress}}.", batchProgressSchema()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals/{id}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Get one goal",
			Description: "Cross-owner id returns 404 (never 403 — no oracle).",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Goal UUID.")}}
		setStatus(op, http.StatusOK, rInline("{goal:Goal}.", goalEnvelope()))
		stdErrors(op, "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals/{id}", "PATCH", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Update fields on a goal",
			Description: "Only supplied fields are written. A spec write revalidates the tree and clears the cached progress atomically. Duplicate (owner, name) on rename returns 409.",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Goal UUID.")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "Any subset of {name, description, spec, enabled}.",
			Content: openapi3.NewContentWithJSONSchema(goalPatchSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{goal:Goal}.", goalEnvelope()))
		stdErrors(op, "400", "401", "403", "404", "409", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals/{id}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Delete a goal",
			Parameters: openapi3.Parameters{pathParamStr("id", "Goal UUID.")}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals/{id}/toggle", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Pause / resume a goal",
			Description: "Body optional — omit to flip, {\"enabled\":bool} to set an exact state (idempotent).",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Goal UUID.")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: false, Description: "{enabled?}.",
			Content: openapi3.NewContentWithJSONSchema(toggleSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{enabled:bool}.", toggleSchema()))
		stdErrors(op, "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/goals/{id}/progress", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagGoals}, Summary: "Compute (or serve cached) progress for one goal",
			Description: "60s stale-while-revalidate cache. A spec change or a heartbeat ingest clears the cache eagerly so the next read is fresh.",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Goal UUID.")}}
		setStatus(op, http.StatusOK, rInline("Progress {hit, progress:0..1, sub_conditions:[...]}.", goalProgressSchema()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())

	// ==== INTEGRATIONS: WAKATIME KEY (gaka-6jm.2) ============================
	//
	// Encrypted-at-rest imported Wakatime API key. Plaintext is NEVER returned
	// on GET — the shape is metadata-only (hasSavedKey, status, checkedAt).
	// See internal/handler/wakatime_key.go for the security posture.

	wakatimeKeyGetSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"hasSavedKey": &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()},
			"keyStatus":   &openapi3.SchemaRef{Value: openapi3.NewStringSchema().WithEnum("valid", "invalid", "unknown")},
			"checkedAt":   &openapi3.SchemaRef{Value: openapi3.NewDateTimeSchema()},
		}
		s.Required = []string{"hasSavedKey"}
		return s
	}
	wakatimeKeySaveSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"key": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}
		s.Required = []string{"key"}
		return s
	}

	doc.AddOperation("/api/v1/users/current/wakatime_key", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagIntegration}, Summary: "Get saved Wakatime key metadata",
			Description: "Returns whether the caller has a saved encrypted Wakatime key on file, the last-known validity status, and the last-check timestamp. Never returns the plaintext or any prefix of it."}
		setStatus(op, http.StatusOK, rInline("{hasSavedKey, keyStatus?, checkedAt?}.", wakatimeKeyGetSchema()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/wakatime_key", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagIntegration}, Summary: "Save (and validate) a Wakatime API key",
			Description: "Probes wakatime.com with the supplied key BEFORE persisting. A conclusive 401/403 from the probe returns 400 so an obviously-bad key never survives in the DB. Network errors are tolerated: the save proceeds with keyStatus='unknown'. Encrypted at rest — plaintext never logged."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{key}.",
			Content: openapi3.NewContentWithJSONSchema(wakatimeKeySaveSchema()),
		}}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/wakatime_key", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagIntegration}, Summary: "Clear the saved Wakatime key",
			Description: "Idempotent — 204 whether or not a saved key existed."}
		setStatus(op, http.StatusNoContent, noContentRef())
		stdErrors(op, "401", "403", "500")
		return op
	}())

	// ==== USER TIMEZONE (gaka-dg7) ==========================================
	//
	// Per-user IANA timezone used by every dow/hour/date bucket the server
	// computes. GET reports both the raw stored value (empty = never picked)
	// and what the server actually resolves to via the 3-level chain (user >
	// BOOM_DEFAULT_TIMEZONE > "UTC"). PATCH validates via time.LoadLocation
	// and triggers a rollup rebuild so the Overview fast path serves
	// user-local buckets immediately.
	timezoneSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"timezone":          &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
			"effectiveTimezone": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}
		s.Required = []string{"timezone", "effectiveTimezone"}
		return s
	}
	timezoneUpdateSchema := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.Properties = openapi3.Schemas{
			"timezone": &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}
		return s
	}
	doc.AddOperation("/api/v1/users/current/timezone", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagIntegration}, Summary: "Get the caller's stored + effective IANA timezone",
			Description: "Returns {timezone, effectiveTimezone}. `timezone` is the raw stored value (empty string = user has never picked); `effectiveTimezone` is what the server actually uses after the 3-level resolution (user > BOOM_DEFAULT_TIMEZONE > 'UTC'). The FE Settings picker keys 'your choice vs server default' off the difference."}
		setStatus(op, http.StatusOK, rInline("{timezone, effectiveTimezone}.", timezoneSchema()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/timezone", "PATCH", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagIntegration}, Summary: "Set (or clear) the caller's IANA timezone",
			Description: "Body: {timezone}. Validated with time.LoadLocation — invalid names return 400. Empty string clears the explicit pick and reverts to the server default resolver. Response mirrors GET so the FE picker can round-trip through one endpoint. Also rebuilds hb_rollup_daily so the Overview fast path serves user-local buckets immediately."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "{timezone}.",
			Content: openapi3.NewContentWithJSONSchema(timezoneUpdateSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{timezone, effectiveTimezone} after write.", timezoneSchema()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== MISC (BADGES / WIDGETS / LEADERBOARDS / COMMITS) ===================

	doc.AddOperation("/badge/link/{project}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagBadges}, Summary: "Mint a badge URL",
			Parameters: openapi3.Parameters{pathParamStr("project", "Project name to badge.")}}
		setStatus(op, http.StatusOK, r("{badgeUrl}.", "BadgeResponse"))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/badge/svg/{svg}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagBadges}, Summary: "Public badge SVG (shields.io-proxied)",
			Security: &public,
			Parameters: openapi3.Parameters{
				pathParamStr("svg", "Badge uuid."),
				paramRef("QueryDays"),
			}}
		setStatus(op, http.StatusOK, rBlob("SVG badge.", "image/svg+xml"))
		stdErrors(op, "400", "404", "500")
		return op
	}())

	doc.AddOperation("/api/v1/users/current/widgets/link", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Mint / upsert a widget link",
			Parameters: openapi3.Parameters{
				strParam("scopeType", "query", "One of 'user'|'project'|'space'.", true),
				strParam("scopeRef", "query", "Project name / space id; omit or '' for user scope.", false),
			}}
		setStatus(op, http.StatusOK, r("{widgetBaseUrl, linkId}.", "WidgetLinkResponse"))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/widgets/links", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "List all widget links",
			Description: "Powers the Settings badge (hits, last-used, origins)."}
		setStatus(op, http.StatusOK, rInline("{links:[WidgetLink]}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/users/current/widgets/link/{id}/roll", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Roll a widget link's uuid",
			Description: "Old id immediately 404s (kills leaked/embedded URLs).",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Widget link uuid.")}}
		setStatus(op, http.StatusOK, r("{widgetBaseUrl, linkId}.", "WidgetLinkResponse"))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/widget/svg/{uuid}/{kind}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagWidgets}, Summary: "Public widget SVG (embed target)",
			Description: "Rendered per the owner's curation. Cached 300s.",
			Security:    &public,
			Parameters: openapi3.Parameters{
				pathParamStr("uuid", "Widget link uuid."),
				pathParamStr("kind", "Widget kind (stats-card, badge, top-langs, top-projects, ..., 'custom' for URL-inline spec)."),
				paramRef("QueryDays"), paramRef("QueryTheme"),
				strParam("title", "query", "Card title override.", false),
				strParam("spec", "query", "Base64 widget spec (custom kind only).", false),
			}}
		setStatus(op, http.StatusOK, rBlob("Rendered SVG.", "image/svg+xml"))
		stdErrors(op, "400", "404", "500")
		return op
	}())

	// gaka-myv: shared per-archetype label image bytes. PUBLIC (no auth).
	// Cache-Control is `public, max-age=31536000, immutable`; the FE appends
	// ?v=<generated_at.epoch> to bust the browser cache after a regeneration.
	// The endpoint IGNORES the ?v query param — it's a routing no-op there
	// purely for the cache-bust side effect.
	doc.AddOperation("/api/v1/labels/{id}/image", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Public label archetype image",
			Description: "Shared image bytes for a memeification label archetype (one per id, same for every user who earned it). Generated via the ComfyUI shim (gaka-myv). Response is `image/png` (or whatever mime the shim returned). Cache-Control is `public, max-age=31536000, immutable`; the FE busts the cache by appending `?v=<generated_at.epoch>` to the src on every render — the endpoint ignores that param and always serves the current bytes.",
			Security:    &public,
			Parameters: openapi3.Parameters{
				pathParamStr("id", "Label id (see internal/labelcatalog for the shipped set: late-night-coder, mac-native, vim-enjoyer, ...)."),
				strParam("v", "query", "Optional cache-bust hint (typically generated_at.epoch). Ignored server-side.", false),
			}}
		setStatus(op, http.StatusOK, rBlob("Raw image bytes.", "image/png"))
		stdErrors(op, "400", "404", "500")
		return op
	}())

	// gaka-myv: Admin tab endpoints. Both require auth + admin allowlist
	// (BOOM_ADMIN_USERS). Non-admins get 403.
	doc.AddOperation("/api/v1/admin/label-images", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: label-images feature status",
			Description: "Returns {enabled, model, shimUrl, count, baseline}. Admin-only (BOOM_ADMIN_USERS)."}
		setStatus(op, http.StatusOK, rInline("Feature status + row count.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/label-images/regenerate", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: regenerate label images via the ComfyUI shim",
			Description: "Body: {entries: [{id, prompt}, ...], ids?: [...], all?: bool, truncate?: bool}. The FE POSTs the full label catalog snapshot; the Go side does NOT need to mirror it. `all: true` regenerates every entry sent (optionally truncating first). `ids: [...]` regenerates a named subset. Returns {generated, failed, requested}."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "Regeneration request.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{generated, failed, requested}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	// gaka-8bz: durable WS stream of the in-memory image-job queue. On
	// connect the server emits {kind:"snapshot", jobs:[...]} then every
	// lifecycle event (added/updated/removed) forever. Cookie auth
	// (refresh_token) — WS handshakes cannot carry Authorization.
	doc.AddOperation("/api/v1/admin/label-images/ws", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: label-image job queue live stream (WebSocket)",
			Description: "WebSocket. Auths via the refresh_token cookie; non-admin owners get 403 pre-upgrade. On connect emits {kind:\"snapshot\", jobs:[Job]}, then a stream of {kind:\"added\"|\"updated\"|\"removed\", job:Job} events for every registry transition. In-memory only — restart of boomtime drops in-flight state (ComfyUI's own queue runs independently)."}
		setStatus(op, http.StatusSwitchingProtocols, rInline("Upgrade to WebSocket.", mapObject()))
		stdErrors(op, "401", "403", "503")
		return op
	}())

	// gaka-364.3: DB-backed labels catalog + admin CRUD.
	doc.AddOperation("/api/v1/labels/catalog", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Public labels catalog + global generation systemPrompt",
			Description: "Returns {systemPrompt: string, labels: [Label]}. Consumed by the FE evaluator on every public-profile / dashboard mount. PUBLIC — no auth required; the catalog isn't per-user.",
			Security:    &public}
		setStatus(op, http.StatusOK, rInline("{systemPrompt, labels}.", mapObject()))
		stdErrors(op, "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/labels", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: create a new label",
			Description: "Body: full LabelSpec (id, kind, label, condition required). 400 if id already exists — use PATCH to update. Admin-only."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "New label.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusCreated, rInline("Created label.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/labels/{id}", "PATCH", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: update a label (partial)",
			Description: "Body: partial LabelSpec — only present fields are overwritten. Cannot rename id (breaks label_images FK). Admin-only.",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Label id.")}}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "Partial LabelSpec.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("Updated label.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/labels/{id}", "DELETE", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: delete a label",
			Description: "Idempotent — 204 whether or not the row existed. Cascades to label_images (best-effort). Admin-only.",
			Parameters:  openapi3.Parameters{pathParamStr("id", "Label id.")}}
		setStatus(op, http.StatusNoContent, rInline("Deleted.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/label-gen-config", "PATCH", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: update global label generation systemPrompt",
			Description: "Body: {systemPrompt: string}. Empty string clears the prefix (worker sends only the per-label optimizedPrompt). Admin-only."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true, Description: "Config update.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("{systemPrompt}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/admin/labels/seed.sql", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Admin: dump DB labels catalog as a goose SQL migration body",
			Description: "Returns text/plain SQL suitable for pasting into a fresh migration file — captures the current DB state (114+ rows + systemPrompt) as reviewable code. Admin-only."}
		setStatus(op, http.StatusOK, rBlob("Goose migration SQL body.", "text/plain"))
		stdErrors(op, "401", "403", "500")
		return op
	}())

	doc.AddOperation("/api/v1/leaderboards", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagLeaderboard}, Summary: "Cross-user leaderboards",
			Parameters: dashboardParams}
		setStatus(op, http.StatusOK, r("LeaderboardsPayload.", "LeaderboardsPayload"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	doc.AddOperation("/api/v1/commits/{project}/report", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagCommits}, Summary: "GitHub commit report with attributed time",
			Description: "Requires the server to have GITHUB_TOKEN configured.",
			Parameters: openapi3.Parameters{
				pathParamStr("project", "Project name."),
				strParam("repoName", "query", "GitHub repository name.", true),
				strParam("repoOwner", "query", "GitHub repository owner.", true),
				strParam("user", "query", "GitHub user login to filter commits by.", true),
				intParam("limit", "query", "Max commits (default 40).", false),
			}}
		setStatus(op, http.StatusOK, r("CommitReport.", "CommitReport"))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())

	// ==== IMPORT ==============================================================

	doc.AddOperation("/import", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "Start a durable import job",
			Description: "Body: {apiToken?, startDate, endDate}. Returns the created (or existing running) job."}
		bodyJSON(op, "ImportRequestPayload", "Import request.", true)
		setStatus(op, http.StatusOK, rInline("{jobId, jobStatus, job}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/import/config", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "Import config",
			Description: "Reports whether the server has a Wakatime API key configured."}
		setStatus(op, http.StatusOK, rInline("{hasServerKey:bool}.", mapObject()))
		return op
	}())
	doc.AddOperation("/import/wakatime-range", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "Detect wakatime.com data range",
			Description: "Body {apiToken?}. Falls back to the server key. Snappy: 15s timeout."}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: false, Description: "{apiToken?}.",
			Content: openapi3.NewContentWithJSONSchema(openapi3.NewObjectSchema()),
		}}
		setStatus(op, http.StatusOK, rInline("Data range or {hasData:false}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/import/jobs", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "List import jobs (newest first)"}
		setStatus(op, http.StatusOK, rInline("{jobs:[...]}.", mapObject()))
		stdErrors(op, "401", "403", "500")
		return op
	}())
	doc.AddOperation("/import/jobs/{id}", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "Get one job + its logs",
			Parameters: openapi3.Parameters{pathParamInt("id", "Job id.")}}
		setStatus(op, http.StatusOK, rInline("{job, logs}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/import/jobs/{id}/cancel", "POST", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "Cancel a running job",
			Parameters: openapi3.Parameters{pathParamInt("id", "Job id.")}}
		setStatus(op, http.StatusOK, rInline("{job}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/import/jobs/{id}/logs", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "REST log tail (fallback for WS)",
			Parameters: openapi3.Parameters{
				pathParamInt("id", "Job id."),
				intParam("afterId", "query", "Return logs with id > afterId.", false),
			}}
		setStatus(op, http.StatusOK, rInline("{logs:[...]}.", mapObject()))
		stdErrors(op, "400", "401", "403", "404", "500")
		return op
	}())
	doc.AddOperation("/import/jobs/{id}/ws", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagImport}, Summary: "WebSocket log stream",
			Description: "WebSocket handshake for a job's live log stream (auth via refresh_token cookie). Not directly usable from Swagger's 'Try it out'; documented for schema completeness.",
			Security:    &openapi3.SecurityRequirements{{"refreshCookie": []string{}}},
			Parameters:  openapi3.Parameters{pathParamInt("id", "Job id.")}}
		setStatus(op, http.StatusSwitchingProtocols, rInline("WebSocket handshake ok.", openapi3.NewObjectSchema()))
		stdErrors(op, "400", "403", "404", "500")
		return op
	}())

	// ==== LOGS ================================================================

	doc.AddOperation("/api/v1/logs", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagLogs}, Summary: "Server log tail (REST)",
			Parameters: openapi3.Parameters{
				intParam("afterId", "query", "Return log entries with id > afterId.", false),
			}}
		setStatus(op, http.StatusOK, rInline("{logs:[...]}.", mapObject()))
		stdErrors(op, "400", "401", "403", "500")
		return op
	}())
	doc.AddOperation("/api/v1/logs/ws", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagLogs}, Summary: "Server log stream (WebSocket)",
			Description: "Query-token auth (browsers cannot set headers on the WS handshake). Documented for completeness.",
			Security:    &public,
			Parameters: openapi3.Parameters{
				strParam("token", "query", "base64 access token.", true),
				intParam("afterId", "query", "Return log entries with id > afterId.", false),
			}}
		setStatus(op, http.StatusSwitchingProtocols, rInline("WebSocket handshake ok.", openapi3.NewObjectSchema()))
		stdErrors(op, "400", "403", "500")
		return op
	}())

	// ==== META / DOCS =========================================================

	doc.AddOperation("/api/v1/version", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagMeta}, Summary: "Server version",
			Security: &public}
		setStatus(op, http.StatusOK, rInline("{version}.", mapObject()))
		return op
	}())
	doc.AddOperation("/api/v1/changelog", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagMeta}, Summary: "Embedded CHANGELOG.md",
			Description: "Returns the changelog markdown verbatim.",
			Security:    &public}
		setStatus(op, http.StatusOK, rBlob("Changelog markdown.", "text/markdown"))
		return op
	}())
	doc.AddOperation("/api/v1/config/public", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagMeta}, Summary: "Public client config",
			Description: "Non-sensitive flags the FE reads at boot to pick the auth + onboarding flow: " +
				"registration_enabled, auth_provider (local|oidc), oidc_enabled, billing_enabled, beta_flags.",
			Security: &public}
		setStatus(op, http.StatusOK, rInline("Client config advertisement.", mapObject()))
		return op
	}())
	doc.AddOperation("/api/openapi.json", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagDocs}, Summary: "This OpenAPI 3 document",
			Description: "Self-describing; no external $refs.",
			Security:    &public}
		setStatus(op, http.StatusOK, rInline("OpenAPI 3 spec.", openapi3.NewObjectSchema()))
		return op
	}())
	doc.AddOperation("/api/docs", "GET", func() *openapi3.Operation {
		op := &openapi3.Operation{Tags: []string{tagDocs}, Summary: "Interactive API explorer",
			Description: "Embedded HTML explorer that loads /api/openapi.json.",
			Security:    &public}
		setStatus(op, http.StatusOK, rBlob("HTML explorer.", "text/html"))
		return op
	}())

	// ==== AUTO-DERIVED STUBS (gaka-lfc, option A) ============================
	//
	// Every registered route above is hand-authored. This pass backfills a
	// MINIMAL operation for any route the router registers that isn't already
	// documented, so a brand-new handler passes the gaka-lfc drift guard with
	// ZERO edits to this file. Explicit entries always win — we never overwrite
	// an operation that already exists. Add an explicit doc.AddOperation entry
	// only when you want documented request/response body schemas; the stub is
	// intentionally schema-free (a generic 200 + the standard error set).
	//
	// The path scoping mirrors internal/server/openapi_drift_test.go EXACTLY:
	// skip the SPA catch-all ("/*") and fold the Swagger UI static tree
	// ("/api/docs/*") back onto its index path, so this pass covers precisely
	// the (method, path) set the drift guard checks.
	if e != nil {
		for _, rt := range e.Router().Routes() {
			p := echoPathToOpenAPI(rt.Path)
			if p == "/*" {
				continue
			}
			if p == "/api/docs/*" {
				p = "/api/docs"
			}
			method := rt.Method
			if item := doc.Paths.Value(p); item != nil && item.GetOperation(method) != nil {
				continue // hand-authored entry wins
			}
			op := &openapi3.Operation{
				Tags:    []string{inferAutoTag(p)},
				Summary: strings.ToUpper(method[:1]) + strings.ToLower(method[1:]) + " " + p,
				Description: "Auto-derived stub (gaka-lfc option A): this route is registered but " +
					"has no hand-written spec entry, so request/response bodies are undocumented. " +
					"Add an explicit doc.AddOperation entry in internal/openapi/spec.go to enrich it.",
			}
			for _, name := range openAPIPathParams(p) {
				op.Parameters = append(op.Parameters, pathParamStr(name, "Path parameter."))
			}
			setStatus(op, http.StatusOK, rInline("OK.", mapObject()))
			stdErrors(op, "400", "401", "403", "404", "500")
			doc.AddOperation(p, method, op)
		}
	}

	// Validate: catch any construction errors before we cache the JSON.
	if err := doc.Validate(context.Background()); err != nil {
		return nil, err
	}
	return doc, nil
}

// echoPathToOpenAPI converts an echo route path to the OpenAPI path template:
// `:param` → `{param}`. Mirrors internal/server/openapi_drift_test.go's
// echoPathToOpenAPI so the auto-derive pass and the drift guard agree on
// exactly which paths they're comparing.
var echoParamRe = regexp.MustCompile(`:([a-zA-Z_][a-zA-Z0-9_]*)`)

func echoPathToOpenAPI(p string) string {
	return echoParamRe.ReplaceAllStringFunc(p, func(s string) string {
		return "{" + strings.TrimPrefix(s, ":") + "}"
	})
}

// openAPIPathParams extracts the `{name}` templated segments from an OpenAPI
// path so the auto-derive pass can emit a required path parameter per segment
// (an operation with an undeclared path parameter fails doc.Validate).
var bracketParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func openAPIPathParams(p string) []string {
	matches := bracketParamRe.FindAllStringSubmatch(p, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// inferAutoTag picks a tag for an auto-derived stub from its path: anything
// under an /admin/ segment is grouped under the Admin tag (matching how the
// hand-authored admin operations are tagged); otherwise the first non-template
// path segment (after stripping the /api/v1|/api prefix) is Title-cased into a
// generic tag. A path with no usable segment falls back to the Meta tag.
func inferAutoTag(p string) string {
	if strings.Contains(p, "/admin/") {
		return tagAdmin
	}
	seg := p
	for _, prefix := range []string{"/api/v1/", "/api/", "/"} {
		if strings.HasPrefix(seg, prefix) {
			seg = seg[len(prefix):]
			break
		}
	}
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	if seg == "" || strings.HasPrefix(seg, "{") {
		return tagMeta
	}
	return strings.ToUpper(seg[:1]) + seg[1:]
}

// strPtr converts a literal string to a *string (required by openapi3 for
// Response.Description).
func strPtr(s string) *string { return &s }

// itoa formats a positive int in base 10 (used to map http.StatusXxx values
// to the "200"/"404"/... keys openapi3.Responses expects).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [8]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
