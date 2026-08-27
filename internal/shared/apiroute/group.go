package apiroute

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
)

// Group registrars, for routes mounted on an *echo.Group rather than the root
// Echo — the per-domain admin seam (catalyst.Module.RegisterAdminRoutes) and the
// portable jobs plugin both register that way.
//
// THE PREFIX PROBLEM, and why this is not just a cast. echo v5 keeps both the
// prefix and the parent Echo unexported on Group, so a registrar cannot compose
// the full path itself, and the spec is keyed by full path. But Group.Add
// RETURNS a RouteInfo carrying the resolved path — so the group reports its own
// prefix, and nothing has to be passed in and kept in sync.
//
// Without these, ~14 routes (the whole /api/v1/admin/jobs cluster plus the books
// admin diagnostics and reading-monitor endpoints) can never leave the generic
// stub, no matter how they are written.

// GGET registers a typed read on a group.
func GGET[Resp any](g *echo.Group, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return groupJSON(g, http.MethodGet, path, http.StatusOK, h, mw...)
}

// GDELETE registers a typed delete on a group.
func GDELETE[Resp any](g *echo.Group, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return groupJSON(g, http.MethodDelete, path, http.StatusOK, h, mw...)
}

// GPOSTNoBody registers a typed write that takes no request body.
func GPOSTNoBody[Resp any](g *echo.Group, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return groupJSON(g, http.MethodPost, path, http.StatusOK, h, mw...)
}

// GPOST registers a typed write that binds a JSON body.
func GPOST[Req, Resp any](g *echo.Group, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return groupJSONBody[Req, Resp](g, http.MethodPost, path, http.StatusOK, h, mw...)
}

// GPUT registers a typed replace that binds a JSON body.
func GPUT[Req, Resp any](g *echo.Group, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return groupJSONBody[Req, Resp](g, http.MethodPut, path, http.StatusOK, h, mw...)
}

// GNoContent registers a 204 on a group.
func GNoContent(g *echo.Group, method, path string, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	ri := g.Add(method, path, func(c *echo.Context) error {
		if err := h(c); err != nil {
			return respond(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}, mw...)
	record(Op{Method: method, Path: ri.Path, Status: http.StatusNoContent})
	return &Route{method: method, path: ri.Path}
}

// GWebSocket registers a handshake endpoint on a group.
func GWebSocket(g *echo.Group, path string, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	ri := g.Add(http.MethodGet, path, h, mw...)
	record(Op{Method: http.MethodGet, Path: ri.Path, Status: http.StatusSwitchingProtocols, NoBodyResponse: true})
	return &Route{method: http.MethodGet, path: ri.Path}
}

// GWritesJSON is WritesJSON for a group-mounted route: the handler owns the
// write, the type is declared.
func GWritesJSON[Resp any](g *echo.Group, method, path string, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	ri := g.Add(method, path, h, mw...)
	record(Op{Method: method, Path: ri.Path, Resp: typeOf[Resp](), Status: http.StatusOK})
	return &Route{method: method, path: ri.Path}
}

func groupJSON[Resp any](g *echo.Group, method, path string, status int, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	ri := g.Add(method, path, func(c *echo.Context) error {
		v, err := h(c)
		if err != nil {
			return respond(c, err)
		}
		return c.JSON(status, v)
	}, mw...)
	// ri.Path is the FULL path with the group prefix resolved; recording the
	// caller's relative path would key the spec by something no request matches.
	record(Op{Method: method, Path: ri.Path, Resp: typeOf[Resp](), Status: status})
	return &Route{method: method, path: ri.Path}
}

func groupJSONBody[Req, Resp any](g *echo.Group, method, path string, status int, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	ri := g.Add(method, path, func(c *echo.Context) error {
		var req Req
		if aerr := bindBody(c, &req, defaultBodyLimit); aerr != nil {
			return apihelpers.RespondErr(c, aerr)
		}
		v, err := h(c, req)
		if err != nil {
			return respond(c, err)
		}
		return c.JSON(status, v)
	}, mw...)
	record(Op{Method: method, Path: ri.Path, Req: typeOf[Req](), Resp: typeOf[Resp](), Status: status})
	return &Route{method: method, path: ri.Path}
}
