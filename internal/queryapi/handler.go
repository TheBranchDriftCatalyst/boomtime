// handler.go — the POST /api/v1/query HTTP handler (gaka-174.q).
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
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/query"
	"github.com/labstack/echo/v5"
	"log/slog"
)

// bodyLimit caps the query spec body. A spec is a small tree of names +
// values; 16 KiB is a generous honest ceiling (a predicate tree that needs
// more has too many nodes to be a legitimate dashboard query).
const bodyLimit int64 = 16 * 1024

// Handler serves the query endpoint. It holds only the deps it reads: the DB
// (its pool is the query.Querier), the config (reading-domain gate), and a
// logger for the 500 path.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
}

// Register wires the query endpoint onto e. Handler must be non-nil.
func Register(e *echo.Echo, h *Handler) {
	e.POST("/api/v1/query", h.RunQuery)
}

// pointDTO / groupDTO are the JSON shapes of a series point / grouped row.
type pointDTO struct {
	Bucket string  `json:"bucket"` // RFC3339 UTC
	Value  float64 `json:"value"`
}

type groupDTO struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

// response is the discriminated-union result envelope. Exactly one of
// scalar/series/groups is populated, selected by kind. Consumers switch on kind
// and default a missing array arm to empty.
type response struct {
	Kind   string     `json:"kind"`
	Scalar *float64   `json:"scalar,omitempty"`
	Series []pointDTO `json:"series,omitempty"`
	Groups []groupDTO `json:"groups,omitempty"`
}

// RunQuery: POST /api/v1/query. Auth required + owner-scoped.
func (h *Handler) RunQuery(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	var spec Spec
	if aerr := apihelpers.BindJSONWithLimit(c, &spec, bodyLimit); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	// Gate the reading domain exactly like the other books routes: when the
	// feature is off, the domain does not exist as far as the API is concerned
	// (404, no oracle). Coding is always available.
	if spec.Domain == "reading" && (h.Cfg == nil || !h.Cfg.BooksEnabled()) {
		return apihelpers.RespondErr(c, apierr.NotFound("unknown domain"))
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
	default: // ResultGroups
		groups := make([]groupDTO, 0, len(res.Groups))
		for _, g := range res.Groups {
			groups = append(groups, groupDTO{Key: g.Key, Value: g.Value})
		}
		return response{Kind: string(res.Kind), Groups: groups}
	}
}
