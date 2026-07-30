// auth_seams_test.go — INTERNAL-PACKAGE test helpers exposing the
// package-level `httpClient` var so external `package handler_test` specs can
// swap it for a stub (e.g. an httptest.Server backing the wakatime.com probe).
//
// Belongs in `package handler` (not handler_test) because the var is
// lowercase-unexported and cannot be reached from outside the package.
// Only test files are compiled into the test binary, so this cannot leak
// into production callers.
package handler

import "net/http"

// SwapHTTPClientForTest replaces the package-level httpClient with the given
// client and returns a restore func. Intended solely for use from tests that
// need to intercept outbound calls the handlers make (currently: the wakatime
// probe in wakatime_key.go). Caller MUST defer the returned restore func.
func SwapHTTPClientForTest(c *http.Client) func() {
	prev := httpClient
	httpClient = c
	return func() { httpClient = prev }
}
