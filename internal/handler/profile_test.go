// profile_test.go — regression tests for the public-profile handler
// (gaka-6jm.1 / gaka-6jm.12).
//
// gaka-6jm.12 (cache leak): the public profile route previously sent
// `Cache-Control: public, max-age=300, s-maxage=300`, which meant disabling a
// profile could leave stale cached copies serving for up to 5 minutes at both
// browser and CDN layers. We now send `public, max-age=60, must-revalidate`
// with an ETag so revalidation is cheap. These tests are the guard rails
// against a regression to the wider cache window.
package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithPublicProfile wires just the auth'd owner CRUD + the public route.
// Handler.Router() doesn't register these by default (harness is minimal).
func routerWithPublicProfile(hz *testutil.Harness) http.Handler {
	e := hz.Router()
	e.GET("/api/v1/users/current/profile", hz.H.GetPublicProfile)
	e.PUT("/api/v1/users/current/profile", hz.H.PutPublicProfile)
	e.GET("/api/public/profile/:slug", hz.H.PublicProfile)
	return e
}

// TestPublicProfileCacheHeadersTightPolicy: gaka-6jm.12 regression.
//
// A publicly-enabled profile's GET response MUST advertise the tightened
// cache policy: `max-age=60`, `must-revalidate`, and (crucially) NO
// `s-maxage=` clause that a CDN would honor beyond the 60s window.
// It MUST also include an ETag so revalidation is cheap.
func TestPublicProfileCacheHeadersTightPolicy(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithPublicProfile(hz)
	user, token := hz.MintUser("cache_hdr")

	// Enable the profile with a valid slug.
	slug := "cachehdr-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
	rec := doJSONReq(t, e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
		"enabled": true,
		"slug":    slug,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT profile: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Hit the public route.
	req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("public profile GET: status %d body=%s", rr.Code, rr.Body.String())
	}

	cc := rr.Header().Get("Cache-Control")
	// Must contain must-revalidate.
	if !strings.Contains(cc, "must-revalidate") {
		t.Errorf("Cache-Control missing must-revalidate: %q", cc)
	}
	// Must contain max-age=60 (tightened from 300).
	if !strings.Contains(cc, "max-age=60") {
		t.Errorf("Cache-Control missing max-age=60: %q", cc)
	}
	// Must NOT contain s-maxage — that's the CDN knob we deliberately dropped
	// so CDNs revert to max-age and don't hold a longer copy.
	if strings.Contains(cc, "s-maxage") {
		t.Errorf("Cache-Control still advertises s-maxage (cache leak regression): %q", cc)
	}
	// Must NOT still advertise the old 5-minute window.
	if strings.Contains(cc, "max-age=300") {
		t.Errorf("Cache-Control still advertises max-age=300 (cache leak regression): %q", cc)
	}

	// Payload-hash ETag so revalidation is cheap. The value must be quoted per
	// RFC 7232 (weak/strong indicator + opaque quoted string).
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag header (revalidation would be full-body every time)")
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Errorf("ETag not quoted per RFC 7232: %q", etag)
	}

	// The payload contains time.Now()-derived StartDate/EndDate, so back-to-back
	// requests produce different bodies and different ETags. We can't assert
	// 304 revalidation deterministically without freezing the clock — but we
	// CAN prove the If-None-Match branch triggers 304 by sending a mismatched
	// ETag and confirming we still get 200 (i.e., the branch respects the
	// header and doesn't return 304 on every request).
	req2 := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
	req2.Header.Set("If-None-Match", `"deadbeefdeadbeef"`)
	rr2 := httptest.NewRecorder()
	e.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("If-None-Match with wrong ETag: status %d, want 200 (must not blanket-304)", rr2.Code)
	}
}

// TestPutPublicProfile_BodySizeCap_413: gaka-bi2. PUT profile MUST reject a
// 5 KiB body with 413 before slug regex + SetPublicProfile run.
//
// Non-tautological signal: without the cap, the handler parses the oversize
// slug, publicProfileSlugRe rejects it as too long (>30 chars), and the
// response is 400 with "slug must be 3-30 characters...". A 413 here proves
// the size trip fired ahead of the format check.
func TestPutPublicProfile_BodySizeCap_413(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := routerWithPublicProfile(hz)
	_, token := hz.MintUser("prof_413")

	big := strings.Repeat("a", 5000)
	body := []byte(`{"enabled":true,"slug":"` + big + `"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize profile PUT: status %d (want 413). 400 would prove the slug regex ran on the payload — proving cap didn't fire first. body=%s",
			rec.Code, rec.Body.String())
	}
}
