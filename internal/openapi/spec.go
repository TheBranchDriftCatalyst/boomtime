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
	"sync"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
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
)

var (
	buildOnce sync.Once
	buildErr  error
	specJSON  []byte
	specDoc   *openapi3.T
)

// Spec builds (once) and returns the OpenAPI document + its JSON encoding.
// The document is fully self-contained: no external $refs, no CDN URLs. It is
// safe to call Spec concurrently.
func Spec() (*openapi3.T, []byte, error) {
	buildOnce.Do(func() {
		doc, err := build()
		if err != nil {
			buildErr = err
			return
		}
		specDoc = doc
		b, err := json.Marshal(doc)
		if err != nil {
			buildErr = err
			return
		}
		specJSON = b
	})
	return specDoc, specJSON, buildErr
}

// build assembles the openapi3.T. Everything is inline here (paths + tags +
// components) so a single sweep captures the shape of the whole API.
func build() (*openapi3.T, error) {
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
			Description: "Body: {axis, action:'hide'|'rename', matchType:'exact'|'regex'|'template' (default 'exact'), matchValue, newValue?}. Both hide and rename are query-time and reversible: no raw data is mutated."}
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
		}
		op := &openapi3.Operation{Tags: []string{tagProfile}, Summary: "Public profile dashboard (no auth)",
			Description: "Resolves slug -> user and returns a widget-scrubbed 60-day activity summary. Machines segment is omitted. Response is cached with must-revalidate for prompt privacy propagation when a user disables their profile.",
			Security:    &public,
			Parameters:  openapi3.Parameters{pathParamStr("slug", "Public profile slug (3-30 chars, lowercase alphanumeric + hyphens).")}}
		setStatus(op, http.StatusOK, rInline("Scrubbed activity summary.", body))
		stdErrors(op, "404", "500")
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

	// Validate: catch any construction errors before we cache the JSON.
	if err := doc.Validate(context.Background()); err != nil {
		return nil, err
	}
	return doc, nil
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
