// ctx_helpers_test.go — external (package handler_test) test helpers
// used by date_defaults_test.go. gaka-8tn phase 8 flipped date_defaults
// to the external package so it could import internal/apihelpers via
// the qualified name; ctxWithQuery moves with it.
package handler_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v5"
)

// ctxWithQuery builds an echo context bound to a GET with the given raw
// query string. Byte-identical to the copy that lived in the pre-refactor
// heartbeats_explore_test.go (now in internal/ingest/explore_test.go).
func ctxWithQuery(rawQuery string) *echo.Context {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec)
}
