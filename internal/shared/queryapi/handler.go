// handler.go — the POST /api/v1/query HTTP handler (boom-174.q).
//
// Flow: identify the owner → bind the (body-limited) spec → gate the reading
// domain behind BooksEnabled → map the spec to a *query.Query → Compile-validate
// (unknown domain/measure/dim/op ⇒ 400) → Run → shape the typed Result as JSON.
//
// Owner scoping is total: the resolved username is the ONLY owner the query can
// ever touch (query.Compile pins the owner-column arg to it). There is no
// cross-owner spec field.
package queryapi

import (
	"context"
	"net/http"
	"strings"

	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/query"
	"github.com/labstack/echo/v5"
)

// BodyLimit caps the query spec body. A spec is a small tree of names +
// values; 16 KiB is a generous honest ceiling (a predicate tree that needs
// more has too many nodes to be a legitimate dashboard query).
// It is EXPORTED so any router that mirrors this registration (the test
// harness) binds at the same ceiling instead of silently inheriting the seam's
// 4 KiB default.
const BodyLimit int64 = 16 * 1024

// Handler serves the query endpoint. It holds only the deps it reads: the DB
// (its pool is the query.Querier), the config (reading-domain gate), and a
// logger for the 500 path.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
}

// Register wires the query endpoint onto e. Handler must be non-nil.
//
// WHY WritesJSON RATHER THAN POSTLimit[Spec, response]. The obvious call is the
// binding registrar — it would document the REQUEST schema too. It cannot be
// used, and the reason is worth writing down because the failure is total
// rather than cosmetic: Spec is RECURSIVE (PredicateNode.Of is
// []*PredicateNode). openapi3gen answers a cycle with a component $ref whose
// Value it then nils out, and the spec builder's schemaForType hands that ref
// straight through without the Ref+Value dual-population that
// internal/shared/openapi's own register() helper performs. doc.Validate then
// rejects the WHOLE document — "schema PredicateNode: found unresolved ref" —
// and GET /api/openapi.json 500s for every route on the server, not just this
// one. Verified empirically before choosing this form.
//
// So the handler keeps its own bind (auth first, then BindJSONWithLimit at the
// 16 KiB BodyLimit — the seam's binding forms would also have shrunk that to
// 4 KiB and reordered auth behind the bind) and WritesJSON declares the
// response type, which is the half of the contract that can be generated
// safely. The request shape is described in prose below; lift it into a real
// schema once schemaForType resolves cyclic component refs.
func Register(e *echo.Echo, h *Handler) {
	apiroute.WritesJSON[response](e, http.MethodPost, "/api/v1/query", h.RunQuery).
		Doc("Run an analytics query",
			"The single generic endpoint behind every dashboard chart: a JSON spec mirroring "+
				"the internal query DSL (from·where·group·measure·over·bucket·having·sort·limit) "+
				"is validated against the domain registry, compiled to SQL and run. Only domain "+
				"and measure are required; everything else defaults to the DSL's zero behaviour "+
				"(a lifetime scalar, unfiltered, unsorted).\n\n"+
				"REQUEST BODY (not generated into a schema below — the predicate tree is "+
				"recursive): {domain, measure, where?, group?, over?, bucket?, having?, sort?, "+
				"limit?, rollups?, rows?, page?}. where is a tree of nodes "+
				"{kind: \"leaf\"|\"and\"|\"or\"|\"not\", and for a leaf dim/op/values, for a "+
				"combinator of:[...children]}, with op one of eq, neq, in, ilike. over is "+
				"{granularity: none|day|week|month, range: {lastN} or {between:{start,end}} — "+
				"the two are mutually exclusive and setting both is a 400; the unit of lastN is "+
				"the granularity, days when granularity is none}. bucket is the top-N/pin/Other "+
				"roll-up policy {topN, pin?, other?}, having is {op, value} with op one of "+
				">= <= > < == !=, sort is {field, desc?} where field is \"measure\"/\"value\", "+
				"\"bucket\", \"key\" or the group dimension, and page is {number, size} "+
				"(1-based, rows mode only).\n\n"+
				"The result is a DISCRIMINATED UNION: switch on \"kind\" and read exactly one "+
				"arm — \"scalar\" (scalar), \"series\" (series, one point per RFC3339 UTC "+
				"bucket), \"groups\" (groups; each carries count + stats when the spec asks for "+
				"rollups), or \"rows\" (rows plus total, the unpaginated row count, when the "+
				"spec sets rows:true). Missing array arms mean empty, not null.\n\n"+
				"Owner scoping is total and implicit: the caller resolved from the "+
				"Authorization header is the only owner a query can reach, and there is no "+
				"cross-owner field in the spec. Any unknown domain, measure, dimension, "+
				"operator or unsupported axis combination is rejected by the compiler as a 400 "+
				"before a single byte of SQL is built — the endpoint constructs no SQL itself "+
				"and every user value rides as a positional argument. The books-owned domains "+
				"\"reading\" and \"readingEvents\" answer 404 \"unknown domain\" when the books "+
				"feature is disabled, so a disabled domain leaks nothing. Grouped queries "+
				"transparently union the caller's saved canonical pins into the bucket policy "+
				"so pinned values always survive as their own group instead of collapsing into "+
				"\"Other\". Request bodies are capped at 16 KiB.")
}

// pointDTO / groupDTO are the JSON shapes of a series point / grouped row.
type pointDTO struct {
	Bucket string  `json:"bucket"` // RFC3339 UTC
	Value  float64 `json:"value"`
}

type groupDTO struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
	// count + stats are present only for a rollups query (Stats!=nil): count is
	// the group's row count, stats the per-measure rollups (count included).
	Count *int               `json:"count,omitempty"`
	Stats map[string]float64 `json:"stats,omitempty"`
}

// response is the discriminated-union result envelope. Exactly one of
// scalar/series/groups/rows is populated, selected by kind. Consumers switch on
// kind and default a missing array arm to empty.
type response struct {
	Kind   string           `json:"kind"`
	Scalar *float64         `json:"scalar,omitempty"`
	Series []pointDTO       `json:"series,omitempty"`
	Groups []groupDTO       `json:"groups,omitempty"`
	Rows   []map[string]any `json:"rows,omitempty"`
	Total  *int             `json:"total,omitempty"` // rows mode: unpaginated count
}

// RunQuery: POST /api/v1/query. Auth required + owner-scoped. It owns its own
// bind and its own write — see Register for why the binding registrars are not
// usable here — so the `response` type it encodes is DECLARED at the
// registration rather than enforced by the seam. Keep the two in step.
func (h *Handler) RunQuery(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	var spec Spec
	if aerr := apihelpers.BindJSONWithLimit(c, &spec, BodyLimit); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	// Gate the books-owned domains exactly like the other books routes: when the
	// feature is off, the domain does not exist as far as the API is concerned
	// (404, no oracle). Both the reading library ("reading") and the reading-events
	// table ("readingEvents") are books surfaces. Coding is always available.
	if (spec.Domain == "reading" || spec.Domain == "readingEvents") &&
		(h.Cfg == nil || !h.Cfg.BooksEnabled()) {
		return apihelpers.RespondErr(c, apierr.NotFound("unknown domain"))
	}

	// Canonical entities: transparently merge the caller's persisted pins for
	// the grouped axis into the spec's bucket policy so their canonical values
	// always survive as their own group (never rolled into "Other"). No-op when
	// the spec is ungrouped or the caller has no pins — behavior is identical to
	// before for anyone who never pinned anything.
	if err := h.applyCanonicalPins(c.Request().Context(), owner, &spec); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "load canonical pins failed", err)
	}

	q, err := spec.toQuery()
	if err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest(err.Error()))
	}

	// Compile is the whitelist authority: an unknown domain/measure/dimension/op
	// or an unsupported axis combination fails here with a "query: ..." error →
	// 400. Running only proceeds against a spec the registry vouched for, so any
	// error from Run below is a genuine DB/internal fault → 500.
	if _, _, err := query.Compile(owner, q); err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest(err.Error()))
	}

	res, err := query.Run(c.Request().Context(), h.DB.Pool, owner, q)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "query run failed", err)
	}

	return c.JSON(http.StatusOK, shape(res))
}

// applyCanonicalPins merges the caller's persisted canonical pins (curation
// rules with action="pin") for the grouped axis into the spec's bucket policy,
// so a grouped query ALWAYS keeps those values as their own group and never
// rolls them into "Other" regardless of share (canonical entities).
//
// It is a no-op unless the spec groups by a dimension AND the caller has at
// least one enabled pin on that axis — so the result is byte-identical to the
// pre-feature behavior for anyone who never pinned anything. When pins exist
// but the spec carries no bucket policy, one is synthesized (TopN 0 = keep all
// non-pinned rows, no Other), which changes nothing on its own but lets the
// pins ride through query.Run's applyBucketPolicy. Explicit spec pins are
// preserved and unioned with the persisted ones (case-insensitive dedupe).
func (h *Handler) applyCanonicalPins(ctx context.Context, owner string, spec *Spec) error {
	if spec.Group == "" {
		return nil
	}
	pins, err := h.DB.LoadPinnedSet(ctx, owner, spec.Group)
	if err != nil {
		return err
	}
	if len(pins) == 0 {
		return nil
	}
	if spec.Bucket == nil {
		spec.Bucket = &BucketSpec{Pin: pins}
		return nil
	}
	spec.Bucket.Pin = unionCI(spec.Bucket.Pin, pins)
	return nil
}

// unionCI returns the union of base and add, de-duplicated case-insensitively
// (the bucket policy matches pins case-insensitively, so a case-variant
// duplicate would be redundant). base order is preserved; new values from add
// are appended in order. The stored casing of the first occurrence wins.
func unionCI(base, add []string) []string {
	seen := make(map[string]bool, len(base)+len(add))
	out := make([]string, 0, len(base)+len(add))
	for _, v := range append(append([]string{}, base...), add...) {
		lk := strings.ToLower(v)
		if seen[lk] {
			continue
		}
		seen[lk] = true
		out = append(out, v)
	}
	return out
}

// shape maps a query.Result onto the JSON response envelope.
func shape(res query.Result) response {
	switch res.Kind {
	case query.ResultScalar:
		v := res.Scalar
		return response{Kind: string(res.Kind), Scalar: &v}
	case query.ResultSeries:
		pts := make([]pointDTO, 0, len(res.Series))
		for _, p := range res.Series {
			pts = append(pts, pointDTO{Bucket: p.Bucket.UTC().Format("2006-01-02T15:04:05Z07:00"), Value: p.Value})
		}
		return response{Kind: string(res.Kind), Series: pts}
	case query.ResultRows:
		rows := res.Rows
		if rows == nil {
			rows = []map[string]any{}
		}
		total := res.Total
		return response{Kind: string(res.Kind), Rows: rows, Total: &total}
	default: // ResultGroups
		groups := make([]groupDTO, 0, len(res.Groups))
		for _, g := range res.Groups {
			dto := groupDTO{Key: g.Key, Value: g.Value}
			if g.Stats != nil {
				dto.Stats = g.Stats
				c := int(g.Stats["count"])
				dto.Count = &c
			}
			groups = append(groups, dto)
		}
		return response{Kind: string(res.Kind), Groups: groups}
	}
}
