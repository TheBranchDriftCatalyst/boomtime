// Package apiroute is the typed route-registration seam: the one place where a
// handler's request and response TYPES are captured, so the OpenAPI spec can be
// generated from Go types instead of a hand-maintained parallel document.
//
// THE PROBLEM IT SOLVES. Walking the router gives paths and methods and nothing
// else — echo's RouteInfo deliberately omits the handler, and every handler is
// `func(*echo.Context) error`, so the payload types are locals inside the
// function body where no amount of reflection can reach them. That is why the
// existing auto-derive pass can only emit `{"type":"object"}` for every body.
//
// Registering through this package instead captures both types at the call site.
// Drift is impossible by construction: the doc and the route are THE SAME CALL,
// so a deleted route takes its documentation with it and a route that was never
// registered was never documented. There is no second file to forget.
//
// It deliberately depends on echo and reflect only — no kin-openapi — so domains
// can register routes without importing the spec builder, and so this package
// cannot participate in an import cycle with it.
package apiroute

import (
	"reflect"
	"sync"
)

// Op is what one registration knows about itself. Types are nil where they do
// not apply (a GET has no request body).
type Op struct {
	Method string
	Path   string
	// Req and Resp are the Go types bound from the body and encoded to it.
	Req  reflect.Type
	Resp reflect.Type
	// Status is the success code written on a nil error.
	Status int

	// Summary and Description are the human documentation. They live HERE, at
	// the registration, rather than in a spec file, for the same reason the
	// types do: a description that sits next to the route cannot outlive it, and
	// deleting the route deletes its prose. A separate doc file is exactly the
	// parallel document this package exists to abolish.
	Summary     string
	Description string
	// Tag overrides the tag inferred from the path prefix.
	Tag string
	// ContentType declares a non-JSON success body (image/png, image/svg+xml,
	// text/markdown, application/zip). Empty means application/json.
	ContentType string
	// NoBodyResponse marks a success that carries no body at all — a 204, a 302
	// redirect, or a 101 WebSocket handshake. Distinct from "body we did not
	// document": the spec must say there is nothing rather than imply an
	// undocumented object.
	NoBodyResponse bool
}

// Route is the handle a registrar returns so documentation can be attached at
// the call site:
//
//	apiroute.GET(e, "/api/v1/thing", h.GetThing).
//		Doc("Get a thing", "Returns the caller's thing, or 404 when absent.")
//
// Ignoring the return value is valid — the route is already registered.
type Route struct {
	method string
	path   string
}

// Doc attaches the summary and description shown in Swagger UI.
func (r *Route) Doc(summary, description string) *Route {
	if r == nil {
		return r
	}
	mu.Lock()
	if op, ok := ops[key(r.method, r.path)]; ok {
		op.Summary, op.Description = summary, description
		ops[key(r.method, r.path)] = op
	}
	mu.Unlock()
	return r
}

// Tag overrides the tag the spec would otherwise infer from the path.
func (r *Route) Tag(tag string) *Route {
	if r == nil {
		return r
	}
	mu.Lock()
	if op, ok := ops[key(r.method, r.path)]; ok {
		op.Tag = tag
		ops[key(r.method, r.path)] = op
	}
	mu.Unlock()
	return r
}

// Produces declares a non-JSON success media type (e.g. "image/svg+xml").
func (r *Route) Produces(contentType string) *Route {
	if r == nil {
		return r
	}
	mu.Lock()
	if op, ok := ops[key(r.method, r.path)]; ok {
		op.ContentType = contentType
		ops[key(r.method, r.path)] = op
	}
	mu.Unlock()
	return r
}

var (
	mu  sync.RWMutex
	ops = map[string]Op{}
)

func key(method, path string) string { return method + " " + path }

// record stores an operation. Last write wins, which matters only in tests that
// register the same route twice; production registers each route once.
func record(op Op) {
	mu.Lock()
	ops[key(op.Method, op.Path)] = op
	mu.Unlock()
}

// Lookup returns the recorded operation for a route, if it was registered
// through this package. The spec builder uses this to attach real schemas,
// falling back to a stub for routes still registered with plain echo.
func Lookup(method, path string) (Op, bool) {
	mu.RLock()
	defer mu.RUnlock()
	op, ok := ops[key(method, path)]
	return op, ok
}

// Ops returns a snapshot of every typed registration.
func Ops() []Op {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		out = append(out, op)
	}
	return out
}

// Reset clears the registry. Tests only — production registers once at startup.
func Reset() {
	mu.Lock()
	ops = map[string]Op{}
	mu.Unlock()
}

// typeOf returns the reflect.Type for T, or nil for the NoBody sentinel so the
// spec emits no schema rather than an empty one.
func typeOf[T any]() reflect.Type {
	t := reflect.TypeFor[T]()
	if t == reflect.TypeFor[NoBody]() {
		return nil
	}
	return t
}

// NoBody marks an operation that takes no request body. A distinct type rather
// than `any` or `struct{}` so it reads at the call site and so the spec can tell
// "no body" apart from "an empty object body".
type NoBody struct{}
