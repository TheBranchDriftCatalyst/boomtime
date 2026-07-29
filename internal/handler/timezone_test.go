package handler_test

// timezone_test.go (gaka-dg7): PATCH/GET endpoint tests + the current-user
// payload extension. Non-tautological: PATCH with an invalid IANA name must
// 400 (proving Go's time.LoadLocation gate ran BEFORE any DB write), and a
// successful PATCH must round-trip through GET.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// routerWithTimezone: harness Router() doesn't wire the timezone endpoints
// by default (added post-hoc); wire them here so the test drives the real
// handler.
func routerWithTimezone(hz *testutil.Harness) *echo.Echo {
	e := hz.Router()
	e.GET("/api/v1/users/current/timezone", hz.H.GetTimezone)
	e.PATCH("/api/v1/users/current/timezone", hz.H.UpdateTimezone)
	return e
}

func doJSON(t *testing.T, e *echo.Echo, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestUpdateTimezone_RejectsInvalidIANA: bogus name -> 400 with no DB write.
// Non-tautological: a follow-up GET must still report the pre-PATCH value.
func TestUpdateTimezone_RejectsInvalidIANA(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithTimezone(hz)
	_, token := hz.MintUser("tz_invalid")

	// Baseline: user has never picked a tz.
	rec := doJSON(t, e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET baseline: status %d body=%s", rec.Code, rec.Body.String())
	}
	var baseline struct {
		Timezone          string `json:"timezone"`
		EffectiveTimezone string `json:"effectiveTimezone"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &baseline); err != nil {
		t.Fatalf("unmarshal baseline: %v", err)
	}
	if baseline.Timezone != "" {
		t.Fatalf("baseline timezone = %q, want empty (never picked)", baseline.Timezone)
	}
	if baseline.EffectiveTimezone != "UTC" {
		t.Fatalf("baseline effectiveTimezone = %q, want UTC (no env default in test harness)",
			baseline.EffectiveTimezone)
	}

	// PATCH bogus name -> 400.
	rec = doJSON(t, e, http.MethodPatch, "/api/v1/users/current/timezone", token,
		map[string]string{"timezone": "Mars/Olympus"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid IANA: status %d, want 400. body=%s",
			rec.Code, rec.Body.String())
	}

	// GET must still show empty — proves no DB write happened.
	rec = doJSON(t, e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET post-reject: status %d", rec.Code)
	}
	var after struct{ Timezone string }
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if after.Timezone != "" {
		t.Fatalf("post-reject timezone = %q, want empty — the invalid PATCH "+
			"should have failed BEFORE any DB write. Non-tautological: "+
			"without the LoadLocation gate the bogus string would sit in "+
			"users.timezone until the next AT TIME ZONE query erroredmid-flight.",
			after.Timezone)
	}
}

// TestUpdateTimezone_ValidRoundtrips: PATCH valid IANA -> 200 with the new
// value in the response body AND surfaced by a follow-up GET.
func TestUpdateTimezone_ValidRoundtrips(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithTimezone(hz)
	_, token := hz.MintUser("tz_valid")

	// PATCH a valid IANA name.
	rec := doJSON(t, e, http.MethodPatch, "/api/v1/users/current/timezone", token,
		map[string]string{"timezone": "America/Los_Angeles"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH valid: status %d body=%s", rec.Code, rec.Body.String())
	}
	var patched struct {
		Timezone          string `json:"timezone"`
		EffectiveTimezone string `json:"effectiveTimezone"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("unmarshal PATCH resp: %v", err)
	}
	if patched.Timezone != "America/Los_Angeles" {
		t.Fatalf("PATCH resp.timezone = %q, want America/Los_Angeles", patched.Timezone)
	}
	if patched.EffectiveTimezone != "America/Los_Angeles" {
		t.Fatalf("PATCH resp.effectiveTimezone = %q, want America/Los_Angeles "+
			"(user pick MUST win the 3-level chain over any env default)",
			patched.EffectiveTimezone)
	}

	// GET must show the same.
	rec = doJSON(t, e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET post-patch: status %d", rec.Code)
	}
	var got struct{ Timezone, EffectiveTimezone string }
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Timezone != "America/Los_Angeles" || got.EffectiveTimezone != "America/Los_Angeles" {
		t.Fatalf("GET post-patch: %+v, want both America/Los_Angeles", got)
	}

	// PATCH with empty clears the pick.
	rec = doJSON(t, e, http.MethodPatch, "/api/v1/users/current/timezone", token,
		map[string]string{"timezone": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH empty: status %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, "/api/v1/users/current/timezone", token, nil)
	var cleared struct{ Timezone, EffectiveTimezone string }
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if cleared.Timezone != "" {
		t.Fatalf("post-clear timezone = %q, want empty", cleared.Timezone)
	}
	if cleared.EffectiveTimezone != "UTC" {
		t.Fatalf("post-clear effectiveTimezone = %q, want UTC (fallback)",
			cleared.EffectiveTimezone)
	}
}
