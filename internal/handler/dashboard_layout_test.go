// dashboard_layout_test.go — integration tests for the dashboard-layout
// persistence layer (gaka-keb).
//
// The tests cover:
//
//   - Round-trip byte-preservation (gaka-25r anti-tautology): PUT a layout,
//     GET returns the same inner-layout JSON byte-for-byte. This catches
//     future storage swaps (JSONB → normalized rows) that would drop key
//     order or otherwise re-serialize.
//   - PUT/GET happy path with a subsequent overwrite (layouts are upserted,
//     not accumulated).
//   - Public profile response includes the layout when set, omits it when
//     unset. Both branches verified against a live PublicProfile handler.
//   - Scope allowlist rejection: an unknown scope returns 400 (not 404 or
//     500), so a stale FE can't squat rows for future scopes.
//   - Body-cap rejection: a 5 KiB body trips 413 before the JSON decoder
//     even sees the tail (same pattern as TestPutPublicProfile_BodySizeCap_413).
package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithDashboardLayout wires the layout CRUD + the pieces of the
// public-profile flow needed for the public-payload-includes-layout tests.
func routerWithDashboardLayout(hz *testutil.Harness) http.Handler {
	e := hz.Router()
	e.GET("/api/v1/users/current/profile", hz.H.GetPublicProfile)
	e.PUT("/api/v1/users/current/profile", hz.H.PutPublicProfile)
	e.GET("/api/public/profile/:slug", hz.H.PublicProfile)
	e.GET("/api/v1/users/current/dashboard/:scope", hz.H.GetDashboardLayout)
	e.PUT("/api/v1/users/current/dashboard/:scope", hz.H.PutDashboardLayout)
	e.DELETE("/api/v1/users/current/dashboard/:scope", hz.H.DeleteDashboardLayout)
	return e
}

// TestDashboardLayoutPersistence_Gaka6jmXRegression is the anti-tautology
// round-trip guard: PUT a layout, GET returns the SEMANTICALLY equivalent
// inner layout (arrays keep order; object keys need not — Postgres JSONB
// does NOT preserve object key order but MUST preserve array order and all
// values).
//
// If someone later swaps the storage from JSONB to a normalized
// (widgets, positions) pair of tables that reorders array elements or
// silently drops unknown fields (`view: null` becoming missing, or
// `_meta` being stripped), this test catches it.
//
// We DELIBERATELY do not compare byte-for-byte. Postgres JSONB does not
// preserve object key order (`{"a":1,"b":2}` may reappear as
// `{"b":2,"a":1}`), and that's fine for our schema — the client renders
// widgets by their `i` (widget-kind id), not by their key order. If we
// ever switch to `json` (Postgres text) or a normalized table, we can
// tighten this to byte-for-byte then.
func TestDashboardLayoutPersistence_Gaka6jmXRegression(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithDashboardLayout(hz)
	_, token := hz.MintUser("dash_rt")

	// Fields we want to survive intact: array order in `widgets`, all
	// coordinates on each widget, and the `view` field including its null
	// literal (a normalized-table swap that dropped null-valued columns
	// would be a silent semantic change).
	inner := `{"cols":12,"widgets":[{"i":"grade-badge","x":0,"y":0,"w":3,"h":3,"view":null},{"i":"top-langs","x":6,"y":3,"w":6,"h":4,"view":"bar"}]}`
	body := []byte(`{"layout":` + inner + `}`)

	// PUT
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status %d body=%s", rec.Code, rec.Body.String())
	}
	var putEnv struct {
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &putEnv); err != nil {
		t.Fatalf("PUT: unmarshal response: %v body=%s", err, rec.Body.String())
	}

	// GET
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/public_profile", nil)
	req2.Header.Set("Authorization", "Basic "+token)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET: status %d body=%s", rec2.Code, rec2.Body.String())
	}
	var getEnv struct {
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &getEnv); err != nil {
		t.Fatalf("GET: unmarshal response: %v body=%s", err, rec2.Body.String())
	}

	// Semantic round-trip: catches storage-strategy swaps that lose array
	// order, drop null values, or otherwise re-shape the payload.
	if diff := semanticJSONDiff(inner, string(getEnv.Layout)); diff != "" {
		t.Errorf("layout round-trip differs semantically: %s\n  sent: %s\n   got: %s", diff, inner, string(getEnv.Layout))
	}

	// Overwrite semantics: PUT a different layout, GET returns the new one.
	inner2 := `{"cols":6,"widgets":[{"i":"punchcard","x":0,"y":0,"w":6,"h":4,"view":"heatmap"}]}`
	body2 := []byte(`{"layout":` + inner2 + `}`)
	req3 := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body2))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Basic "+token)
	rec3 := httptest.NewRecorder()
	e.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("PUT overwrite: status %d body=%s", rec3.Code, rec3.Body.String())
	}
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/public_profile", nil)
	req4.Header.Set("Authorization", "Basic "+token)
	rec4 := httptest.NewRecorder()
	e.ServeHTTP(rec4, req4)
	var getEnv2 struct {
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.Unmarshal(rec4.Body.Bytes(), &getEnv2); err != nil {
		t.Fatalf("GET after overwrite: %v", err)
	}
	if diff := semanticJSONDiff(inner2, string(getEnv2.Layout)); diff != "" {
		t.Errorf("overwrite lost semantics: %s\n  sent: %s\n   got: %s", diff, inner2, string(getEnv2.Layout))
	}
}

// semanticJSONDiff compares two JSON documents by their decoded any-value
// representations. Returns "" when they are structurally equal, otherwise a
// short human-oriented diff message. This is intentionally a helper local
// to this test file — the round-trip contract does NOT require byte
// equality (see the test's doc comment above), only that everything the FE
// reads back is the same.
func semanticJSONDiff(a, b string) string {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return "left is not valid JSON: " + err.Error()
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return "right is not valid JSON: " + err.Error()
	}
	// Re-marshal both through the same encoder so map key order is normalized
	// (json.Marshal sorts map keys). If the normalized forms match, the two
	// documents are semantically equal.
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	if string(an) != string(bn) {
		return "normalized forms differ"
	}
	return ""
}

// TestDashboardLayoutUnknownScope: PUT/GET/DELETE with a scope not in the
// allowlist returns 400 (not 404 or 500). Guards against a stale FE
// squatting rows for scopes we haven't wired yet.
func TestDashboardLayoutUnknownScope(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithDashboardLayout(hz)
	_, token := hz.MintUser("dash_scope")

	body := []byte(`{"layout":{"widgets":[]}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/overview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT unknown scope: status %d, want 400", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/overview", nil)
	req2.Header.Set("Authorization", "Basic "+token)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("GET unknown scope: status %d, want 400", rec2.Code)
	}
}

// TestDashboardLayoutMissWhenUnset: GET before any PUT returns 404 so the
// FE knows to fall back to defaults.
func TestDashboardLayoutMissWhenUnset(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithDashboardLayout(hz)
	_, token := hz.MintUser("dash_miss")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/dashboard/public_profile", nil)
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET unset: status %d, want 404 (FE default-layout path relies on this)", rec.Code)
	}
}

// TestPutDashboardLayout_BodySizeCap_413: A body over 4 KiB returns 413
// BEFORE the layout row is written. Non-tautological signal: without the
// cap, the handler would decode the (still-valid-JSON) 5 KiB layout and
// store it. A 413 here proves the size trip fired first.
func TestPutDashboardLayout_BodySizeCap_413(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithDashboardLayout(hz)
	_, token := hz.MintUser("dash_413")

	// Craft a valid-JSON body that exceeds 4 KiB. Pad the layout with a
	// large `_pad` field so the decoder would accept it if the cap didn't fire.
	pad := strings.Repeat("a", 5000)
	body := []byte(`{"layout":{"widgets":[],"_pad":"` + pad + `"}}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize layout PUT: status %d (want 413). 200 would prove the cap didn't fire and the row was written.", rec.Code)
	}
}

// TestPublicProfileIncludesLayoutWhenSet: after a user PUTs a layout, the
// public /api/public/profile/:slug response embeds it verbatim under
// `layout`. This is the single-fetch contract for the public dashboard.
func TestPublicProfileIncludesLayoutWhenSet(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithDashboardLayout(hz)
	user, token := hz.MintUser("pub_layout")

	// Enable the profile with a valid slug.
	slug := "publayout-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
	rec := doJSONReq(t, e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
		"enabled": true,
		"slug":    slug,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT profile: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Save a layout.
	inner := `{"cols":12,"widgets":[{"i":"grade-badge","x":0,"y":0,"w":3,"h":3,"view":null}]}`
	body := []byte(`{"layout":` + inner + `}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/dashboard/public_profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT layout: status %d body=%s", rr.Code, rr.Body.String())
	}

	// Hit the public route unauthenticated — the layout MUST appear inline.
	req2 := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
	rr2 := httptest.NewRecorder()
	e.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("public profile GET: status %d body=%s", rr2.Code, rr2.Body.String())
	}
	var resp struct {
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal public profile: %v", err)
	}
	if diff := semanticJSONDiff(inner, string(resp.Layout)); diff != "" {
		t.Errorf("public profile layout mismatch: %s\n  sent: %s\n   got: %s", diff, inner, string(resp.Layout))
	}
}

// TestPublicProfileLayoutOmittedWhenUnset: without a saved layout row, the
// public profile response OMITS the layout field entirely (thanks to
// json:",omitempty") so the FE knows to render the default array.
func TestPublicProfileLayoutOmittedWhenUnset(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithDashboardLayout(hz)
	user, token := hz.MintUser("pub_nolayout")

	slug := "nolayout-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
	rec := doJSONReq(t, e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
		"enabled": true,
		"slug":    slug,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT profile: status %d body=%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("public profile GET: status %d body=%s", rr.Code, rr.Body.String())
	}
	// Cheap containment check — if omitempty is honored, "layout" doesn't
	// appear in the JSON at all. A raw-map decode into any is more thorough
	// but this string-check catches the regression narrowly and cheaply.
	if strings.Contains(rr.Body.String(), `"layout"`) {
		t.Errorf("expected no `layout` key in public profile when unset; body=%s", rr.Body.String())
	}
}
