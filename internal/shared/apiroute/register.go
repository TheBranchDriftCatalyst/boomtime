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

// Handler is a route that reads no request body.
type Handler[Resp any] func(c *echo.Context) (Resp, error)

// BodyHandler is a route that binds a JSON request body first.
type BodyHandler[Req, Resp any] func(c *echo.Context, req Req) (Resp, error)

// GET registers a typed read. The response type is captured for the spec.
func GET[Resp any](e *echo.Echo, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) {
	register(e, http.MethodGet, path, http.StatusOK, nil, h, mw...)
}

// DELETE registers a typed delete.
func DELETE[Resp any](e *echo.Echo, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) {
	register(e, http.MethodDelete, path, http.StatusOK, nil, h, mw...)
}

// POST registers a typed write that binds a JSON body.
func POST[Req, Resp any](e *echo.Echo, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) {
	registerBody(e, http.MethodPost, path, http.StatusOK, h, mw...)
}

// PUT registers a typed replace that binds a JSON body.
func PUT[Req, Resp any](e *echo.Echo, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) {
	registerBody(e, http.MethodPut, path, http.StatusOK, h, mw...)
}

// PATCH registers a typed partial update that binds a JSON body.
func PATCH[Req, Resp any](e *echo.Echo, path string, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) {
	registerBody(e, http.MethodPatch, path, http.StatusOK, h, mw...)
}

// POSTNoBody registers a typed write that takes no request body — a trigger or
// an action whose inputs are entirely in the path. Distinct from POST so the
// spec can say "no body" rather than documenting an empty object.
func POSTNoBody[Resp any](e *echo.Echo, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) {
	register(e, http.MethodPost, path, http.StatusOK, nil, h, mw...)
}

// Accepted marks a handler whose success code is 202 rather than 200 — every
// liberation mutation ENQUEUES rather than running inline, and a spec that
// claimed 200 would be lying about a contract clients depend on.
func Accepted[Resp any](e *echo.Echo, method, path string, h Handler[Resp], mw ...echo.MiddlewareFunc) {
	register(e, method, path, http.StatusAccepted, nil, h, mw...)
}

func register[Resp any](e *echo.Echo, method, path string, status int, reqType any, h Handler[Resp], mw ...echo.MiddlewareFunc) {
	_ = reqType
	record(Op{Method: method, Path: path, Resp: typeOf[Resp](), Status: status})
	e.Add(method, path, func(c *echo.Context) error {
		v, err := h(c)
		if err != nil {
			return respond(c, err)
		}
		return c.JSON(status, v)
	}, mw...)
}

func registerBody[Req, Resp any](e *echo.Echo, method, path string, status int, h BodyHandler[Req, Resp], mw ...echo.MiddlewareFunc) {
	record(Op{Method: method, Path: path, Req: typeOf[Req](), Resp: typeOf[Resp](), Status: status})
	e.Add(method, path, func(c *echo.Context) error {
		var req Req
		// Same body cap every hand-written handler applies. Binding here rather
		// than in each handler is half the point: the type is visible to the
		// spec AND the limit cannot be forgotten.
		if aerr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitSmall); aerr != nil {
			return apihelpers.RespondErr(c, aerr)
		}
		v, err := h(c, req)
		if err != nil {
			return respond(c, err)
		}
		return c.JSON(status, v)
	}, mw...)
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
