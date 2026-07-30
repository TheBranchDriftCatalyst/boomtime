// ctx_helpers_test.go — internal (package handler) test helpers restored
// after gaka-8tn phase 5a moved heartbeats_explore_test.go (the original
// home of ctxWithQuery) into internal/ingest/. date_defaults_test.go
// still lives here and calls ctxWithQuery — this file keeps that call
// site working without changing the test body.
package handler

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
