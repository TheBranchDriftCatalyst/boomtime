// ratelimit_identity_test.go — who a request IS, for rate-limiting purposes.
//
// Two independent properties, both of which were broken in the deployed topology:
//
//   - The IP half: with no echo IPExtractor, c.RealIP() is the raw RemoteAddr,
//     which behind the Traefik ingress is the PROXY for every external client.
//     One `ip:<proxy>` bucket per group means one abusive client 429s /auth/login
//     for everybody. configureIPExtractor installs a trust-checked X-Forwarded-For
//     extractor; these tests pin both that it isolates real clients AND that a
//     client cannot forge its own bucket with the header.
//
//   - The user half: userCtxMiddleware already resolves the bearer token once per
//     request, so bucketKey must reuse that resolution instead of issuing its own
//     token→user DB round-trip (it used to issue one — two on the wakatime-probe
//     path, where the group branch repeated the identical lookup).
package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
	"github.com/labstack/echo/v5"
)

// loginPoster builds an echo with the rate limiter installed on /auth/login
// (groupAuthWrite: burst 10) and returns a func that fires one request from a
// given (RemoteAddr, X-Forwarded-For) pair and reports the status.
func loginPoster(t *testing.T, e *echo.Echo) func(remoteAddr, xff string) int {
	t.Helper()
	store := newRateLimitStore(silentLogger(), func(*echo.Context) string { return "" })
	t.Cleanup(func() { close(store.stop) })
	e.Use(store.middleware())
	e.POST("/auth/login", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	return func(remoteAddr, xff string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		e.ServeHTTP(rec, req)
		return rec.Code
	}
}

// TestProxiedClientsDoNotShareOneAuthBucket is the login-lockout scenario. Every
// request arrives from the ingress pod's address, so without an IP extractor all
// external callers key on `ip:10.42.0.9` — one client draining the 10/min
// auth-write burst 429s /auth/login for every other client, indefinitely.
func TestProxiedClientsDoNotShareOneAuthBucket(t *testing.T) {
	const ingress = "10.42.0.9:54321" // Traefik pod: private range → trusted hop

	e := echo.New()
	configureIPExtractor(e, silentLogger())
	post := loginPoster(t, e)

	// One abusive external client exhausts its own burst.
	var throttled int
	for i := 0; i < 12; i++ {
		if post(ingress, "203.0.113.7") == http.StatusTooManyRequests {
			throttled++
		}
	}
	if throttled == 0 {
		t.Fatal("the abusive client was never throttled — the limiter is not engaged, so this test proves nothing")
	}

	// A DIFFERENT external client, same proxy, must be unaffected.
	if code := post(ingress, "198.51.100.9"); code != http.StatusOK {
		t.Fatalf("second client got %d, want 200: every external caller shares the ingress's bucket, so one credential-stuffing client holds all logins down", code)
	}
}

// TestForgedForwardedForCannotPickABucket is the other half of the same change:
// honouring X-Forwarded-For must not hand callers a bucket-selection knob. Echo's
// extractor walks the chain right-to-left from RemoteAddr and stops at the first
// UNTRUSTED hop, so a directly-connected public client always keys on its own
// address no matter what it puts in the header.
func TestForgedForwardedForCannotPickABucket(t *testing.T) {
	e := echo.New()
	configureIPExtractor(e, silentLogger())
	post := loginPoster(t, e)

	const attacker = "203.0.113.50:5555" // public source address, untrusted hop

	var throttled int
	for i := 0; i < 12; i++ {
		// A different forged client IP on every request — if the header were
		// trusted blindly, each would land in a fresh full bucket.
		if post(attacker, fmt.Sprintf("198.51.100.%d", i+1)) == http.StatusTooManyRequests {
			throttled++
		}
	}
	if throttled == 0 {
		t.Fatal("a public-source client rotated X-Forwarded-For and was never throttled — the header is being trusted from an untrusted peer")
	}

	// Sanity: a genuinely different source address still gets its own bucket, so
	// the throttling above is bucket isolation and not a global limit.
	if code := post("203.0.113.51:5555", ""); code != http.StatusOK {
		t.Fatalf("a different source address got %d, want 200", code)
	}
}

// TestNoForwardedForKeepsRemoteAddrBucketing pins that installing the extractor
// changed NOTHING for unproxied traffic: with no X-Forwarded-For header the key
// is still the RemoteAddr host, exactly as before.
func TestNoForwardedForKeepsRemoteAddrBucketing(t *testing.T) {
	e := echo.New()
	configureIPExtractor(e, silentLogger())
	post := loginPoster(t, e)

	var throttled int
	for i := 0; i < 12; i++ {
		if post("203.0.113.90:1111", "") == http.StatusTooManyRequests {
			throttled++
		}
	}
	if throttled != 2 {
		t.Fatalf("throttled %d of 12 direct requests, want 2 (burst 10) — RemoteAddr bucketing changed", throttled)
	}
	if code := post("203.0.113.91:1111", ""); code != http.StatusOK {
		t.Fatalf("a second direct address got %d, want 200 — per-IP isolation broken", code)
	}
}

// probeRequest fires one POST at the wakatime_key endpoint (groupWakatimeProbe),
// which is the path that used to pay the token→user lookup twice.
func probeRequest(t *testing.T, e *echo.Echo) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/wakatime_key", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(echo.HeaderAuthorization, "Basic some-token")
	req.RemoteAddr = "203.0.113.5:2222"
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe request: got %d, want 200", rec.Code)
	}
}

// TestBucketKeyReusesTheStashedIdentity: when userCtxMiddleware has already
// resolved this request's owner, the limiter must key on it WITHOUT a lookup of
// its own. Before, every authenticated request paid two identity round-trips.
func TestBucketKeyReusesTheStashedIdentity(t *testing.T) {
	var lookups atomic.Int32
	store := newRateLimitStore(silentLogger(), func(*echo.Context) string {
		lookups.Add(1)
		return "resolved-again"
	})
	t.Cleanup(func() { close(store.stop) })

	e := echo.New()
	// Stand-in for userCtxMiddleware's successful resolution.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			stashOwner(c, "ada")
			return next(c)
		}
	})
	e.Use(store.middleware())
	e.POST("/api/v1/users/current/wakatime_key", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	probeRequest(t, e)

	if n := lookups.Load(); n != 0 {
		t.Errorf("bucketKey issued %d token→user DB lookups for a request whose identity was already resolved upstream", n)
	}
	if _, ok := store.buckets[groupWakatimeProbe].Load("user:ada"); !ok {
		t.Error("request was not bucketed on the stashed owner (user:ada)")
	}
}

// TestBucketKeyResolvesAtMostOnceWithoutAStash pins the duplicate the wakatime
// branch used to introduce: bucketKey called s.userLookup for the group, then
// fell through and called the identical lookup a second time.
func TestBucketKeyResolvesAtMostOnceWithoutAStash(t *testing.T) {
	var lookups atomic.Int32
	store := newRateLimitStore(silentLogger(), func(*echo.Context) string {
		lookups.Add(1)
		return "" // unresolvable token — the fall-through case
	})
	t.Cleanup(func() { close(store.stop) })

	e := echo.New()
	e.Use(store.middleware())
	e.POST("/api/v1/users/current/wakatime_key", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	probeRequest(t, e)

	if n := lookups.Load(); n != 1 {
		t.Errorf("bucketKey issued %d token→user lookups for one wakatime-probe request, want 1 — the group branch repeats the identical query", n)
	}
}

// TestRateLimiterReusesUserCtxResolution composes the two REAL middlewares in
// production order against a real DB and a real token: userCtxMiddleware resolves
// the bearer once, and the limiter buckets on that owner without touching the DB.
// If the two were installed the other way round (the pre-fix order) the limiter
// would run first, find no stash, and pay for its own resolution — which is what
// the lookup counter here would report.
func TestRateLimiterReusesUserCtxResolution(t *testing.T) {
	database := testutil.OpenDB(t)
	alice, aliceTok := mintUserAndToken(t, database, "alice")

	var lookups atomic.Int32
	dbLookup := userLookupFromDB(database)
	store := newRateLimitStore(silentLogger(), func(c *echo.Context) string {
		lookups.Add(1)
		return dbLookup(c)
	})
	t.Cleanup(func() { close(store.stop) })

	e := echo.New()
	e.Use(userCtxMiddleware(database))
	e.Use(store.middleware())
	e.POST("/api/v1/users/current/wakatime_key", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/wakatime_key", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(echo.HeaderAuthorization, "Basic "+aliceTok)
	req.RemoteAddr = "203.0.113.6:3333"
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authed probe: got %d, want 200", rec.Code)
	}

	if n := lookups.Load(); n != 0 {
		t.Errorf("the limiter issued %d identity lookups behind userCtxMiddleware; the resolution it needs was already on the request", n)
	}
	if _, ok := store.buckets[groupWakatimeProbe].Load("user:" + alice); !ok {
		t.Errorf("authenticated request was not bucketed on user:%s — the reused identity is wrong or missing", alice)
	}
}
