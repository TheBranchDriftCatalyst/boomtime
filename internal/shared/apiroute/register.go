package apiroute

import (
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
)

// BodyLimitNone disables the seam's request-body cap for one route, matching a
// handler that used plain c.Bind. Capping such an endpoint is a real behaviour
// change and must be a deliberate decision, not a side effect of moving it onto
// the seam — internal/boomtime/admin has a test that exists solely to make that
// choice explicit.
const BodyLimitNone int64 = 0

// defaultBodyLimit is what the plain POST/PUT/PATCH forms bind at. Small (4 KiB)
// suits the majority — a JSON control payload — but NOT every route: ingest
// takes 8 MiB of heartbeats and imports were historically unbounded. Those must
// use the *Limit forms, or the seam silently shrinks their contract.
var defaultBodyLimit = apihelpers.BodyLimitSmall

// Handler is a route that reads no request body.
type Handler[Resp any] func(c *echo.Context) (Resp, error)

// BodyHandler is a route that binds a JSON request body first.
type BodyHandler[Req, Resp any] func(c *echo.Context, req Req) (Resp, error)

// GET registers a typed read. The response type is captured for the spec.
func GET[Resp any](e *echo.Echo, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return register(e, http.MethodGet, path, http.StatusOK, nil, h, mw...)
}

// DELETE registers a typed delete.
func DELETE[Resp any](e *echo.Echo, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return register(e, http.MethodDelete, path, http.StatusOK, nil, h, mw...)
}

// POST registers a typed write that binds a JSON body.
func POST[Req, Resp any](e *echo.Echo, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return registerBody(e, http.MethodPost, path, http.StatusOK, defaultBodyLimit, h, mw...)
}

// POSTLimit is POST with an explicit request-body cap. Use BodyLimitNone to
// preserve an endpoint that previously bound without one.
func POSTLimit[Req, Resp any](e *echo.Echo, path string, limit int64, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return registerBody(e, http.MethodPost, path, http.StatusOK, limit, h, mw...)
}

// PUT registers a typed replace that binds a JSON body.
func PUT[Req, Resp any](e *echo.Echo, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return registerBody(e, http.MethodPut, path, http.StatusOK, defaultBodyLimit, h, mw...)
}

// PATCH registers a typed partial update that binds a JSON body.
func PATCH[Req, Resp any](e *echo.Echo, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return registerBody(e, http.MethodPatch, path, http.StatusOK, defaultBodyLimit, h, mw...)
}

// POSTNoBody registers a typed write that takes no request body — a trigger or
// an action whose inputs are entirely in the path. Distinct from POST so the
// spec can say "no body" rather than documenting an empty object.
func POSTNoBody[Resp any](e *echo.Echo, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return register(e, http.MethodPost, path, http.StatusOK, nil, h, mw...)
}

// Accepted marks a handler whose success code is 202 rather than 200 — every
// liberation mutation ENQUEUES rather than running inline, and a spec that
// claimed 200 would be lying about a contract clients depend on.
func Accepted[Resp any](e *echo.Echo, method, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	return register(e, method, path, http.StatusAccepted, nil, h, mw...)
}

// AcceptedBody registers a typed write that binds a JSON body AND answers 202.
// Separate from POST because 22 routes enqueue rather than act inline, and a
// spec claiming 200 for them would misstate the contract.
func AcceptedBody[Req, Resp any](e *echo.Echo, method, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	return registerBody(e, method, path, http.StatusAccepted, defaultBodyLimit, h, mw...)
}

// NoContent registers a route that answers 204 with an empty body. 27 routes do
// this; without a dedicated form they would either stay off the seam entirely or
// be forced to invent a response type that the handler never writes.
//
// The spec records no response schema for these, which is the truth: a 204 body
// is empty by definition.
func NoContent(e *echo.Echo, method, path string, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	record(Op{Method: method, Path: path, Status: http.StatusNoContent})
	e.Add(method, path, func(c *echo.Context) error {
		if err := h(c); err != nil {
			return respond(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}, mw...)
	return &Route{method: method, path: path}
}

// NoContentBody is NoContent for a route that binds a JSON body first.
func NoContentBody[Req any](e *echo.Echo, method, path string, h func(c *echo.Context, req Req) error, mw ...echo.MiddlewareFunc) *Route {
	return noContentBody(e, method, path, defaultBodyLimit, h, mw...)
}

// NoContentBodyLimit is NoContentBody with an explicit cap.
func NoContentBodyLimit[Req any](e *echo.Echo, method, path string, limit int64, h func(c *echo.Context, req Req) error, mw ...echo.MiddlewareFunc) *Route {
	return noContentBody(e, method, path, limit, h, mw...)
}

func noContentBody[Req any](e *echo.Echo, method, path string, limit int64, h func(c *echo.Context, req Req) error, mw ...echo.MiddlewareFunc) *Route {
	record(Op{Method: method, Path: path, Req: typeOf[Req](), Status: http.StatusNoContent})
	e.Add(method, path, func(c *echo.Context) error {
		var req Req
		if aerr := bindBody(c, &req, limit); aerr != nil {
			return apihelpers.RespondErr(c, aerr)
		}
		if err := h(c, req); err != nil {
			return respond(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}, mw...)
	return &Route{method: method, path: path}
}

func register[Resp any](e *echo.Echo, method, path string, status int, reqType any, h Handler[Resp], mw ...echo.MiddlewareFunc) *Route {
	_ = reqType
	record(Op{Method: method, Path: path, Resp: typeOf[Resp](), Status: status})
	e.Add(method, path, func(c *echo.Context) error {
		v, err := h(c)
		if err != nil {
			return respond(c, err)
		}
		return c.JSON(status, v)
	}, mw...)
	return &Route{method: method, path: path}
}

func registerBody[Req, Resp any](e *echo.Echo, method, path string, status int, limit int64, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) *Route {
	record(Op{Method: method, Path: path, Req: typeOf[Req](), Resp: typeOf[Resp](), Status: status})
	e.Add(method, path, func(c *echo.Context) error {
		var req Req
		if aerr := bindBody(c, &req, limit); aerr != nil {
			return apihelpers.RespondErr(c, aerr)
		}
		v, err := h(c, req)
		if err != nil {
			return respond(c, err)
		}
		return c.JSON(status, v)
	}, mw...)
	return &Route{method: method, path: path}
}

// respond preserves the existing error contract exactly: an *apierr.Error keeps
// its status and message, anything else becomes the generic 500 envelope.
// Handlers that already call apihelpers.RespondErr and return nil are
// unaffected — they never reach here with a non-nil error.
//
// The unexpected branch LOGS, matching apihelpers.InternalErr. Without it,
// moving a handler onto this seam would silently convert logged 500s into
// invisible ones — a real loss of operability disguised as a refactor.
func respond(c *echo.Context, err error) error {
	var aerr *apierr.Error
	if errors.As(err, &aerr) {
		return apihelpers.RespondErr(c, aerr)
	}
	logger().Error("unhandled handler error",
		"method", c.Request().Method, "path", c.Request().URL.Path, "err", err)
	return apihelpers.RespondErr(c, apierr.Generic())
}

// pkgLogger is where unexpected handler errors go. Defaults to slog.Default()
// so the seam is usable without wiring; SetLogger points it at the server's
// logger at startup.
var pkgLogger atomic.Pointer[slog.Logger]

// SetLogger directs unexpected-error logging. Called once from the composition
// root.
func SetLogger(l *slog.Logger) {
	if l != nil {
		pkgLogger.Store(l)
	}
}

func logger() *slog.Logger {
	if l := pkgLogger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// bindBody applies the route's cap, or binds uncapped when limit is
// BodyLimitNone. Uncapped is only correct where the endpoint was already
// uncapped; the seam must not quietly tighten a contract.
func bindBody(c *echo.Context, dst any, limit int64) *apierr.Error {
	if limit == BodyLimitNone {
		if err := c.Bind(dst); err != nil {
			return apierr.BadRequest("Invalid request body")
		}
		return nil
	}
	return apihelpers.BindJSONWithLimit(c, dst, limit)
}

// --- routes whose success is not a JSON body ---------------------------------
//
// These exist so a non-JSON route can still be DOCUMENTED precisely. The
// alternative — leaving them on plain echo — makes the spec fall back to a
// generic object, which claims a JSON body that will never arrive. Saying
// "101, no body" or "302 to an external identity provider" is both true and
// more useful.
//
// The handler keeps full control of the response; the seam only records what
// the route produces. It writes nothing itself.

// Raw registers a route that writes its own non-JSON success body (an image, an
// SVG, a zip, markdown). contentType is what the spec will advertise.
func Raw(e *echo.Echo, method, path, contentType string, status int, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	if status == 0 {
		status = http.StatusOK
	}
	record(Op{Method: method, Path: path, Status: status, ContentType: contentType})
	e.Add(method, path, h, mw...)
	return &Route{method: method, path: path}
}

// Redirect registers a route whose success is a 3xx Location redirect — the
// OAuth entry and callback endpoints. Documenting these as a JSON 200 is simply
// wrong, and it is what the generic stub does today.
func Redirect(e *echo.Echo, method, path string, status int, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	if status == 0 {
		status = http.StatusFound
	}
	record(Op{Method: method, Path: path, Status: status, NoBodyResponse: true})
	e.Add(method, path, h, mw...)
	return &Route{method: method, path: path}
}

// WebSocket registers a handshake endpoint. The documented success is 101
// Switching Protocols with no body — Swagger cannot exercise it, but the spec
// should still say the route exists and what it upgrades to.
func WebSocket(e *echo.Echo, path string, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	record(Op{Method: http.MethodGet, Path: path, Status: http.StatusSwitchingProtocols, NoBodyResponse: true})
	e.Add(http.MethodGet, path, h, mw...)
	return &Route{method: http.MethodGet, path: path}
}

// WritesJSON registers a route whose handler writes its own JSON body, while
// still DECLARING the Go type of that body for the spec.
//
// WHY THIS EXISTS, and why it is not the default. The normal registrars own the
// encoding, so the declared type and the written bytes cannot disagree. A
// handful of endpoints cannot work that way: apihelpers.CachedJSON serves
// PRE-MARSHALLED bytes straight from the TTL cache, and that is the entire point
// of it — these are the heaviest dashboard aggregations in the app. Forcing them
// through a (Resp, error) signature would add an unmarshal on every cache hit
// plus a re-marshal on the way out, taxing the hot path to satisfy the docs.
//
// THE TRADE, stated plainly: here the type is DECLARED rather than ENFORCED.
// Nothing stops the handler writing a different shape. That is strictly worse
// than the normal path and strictly better than the generic object these routes
// document today — but reach for it only when the handler genuinely must own the
// write, never to avoid naming a response type.
func WritesJSON[Resp any](e *echo.Echo, method, path string, h func(c *echo.Context) error, mw ...echo.MiddlewareFunc) *Route {
	record(Op{Method: method, Path: path, Resp: typeOf[Resp](), Status: http.StatusOK})
	e.Add(method, path, h, mw...)
	return &Route{method: method, path: path}
}
