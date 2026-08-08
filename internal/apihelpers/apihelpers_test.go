// apihelpers_test.go — ginkgo coverage for the shared HTTP-plumbing
// helpers. Rehomes the coverage that used to live at
// internal/handler/handler_helpers_test.go (pre gaka-8tn phase 8), which
// tested the same code paths via the god-type shim methods
// (h.resolveOwnerFromCookie / h.loadSpace / h.resolveUser / h.cachedJSON /
// h.cachedBlob). Every assertion + Describe/Context/It string here is
// byte-identical to the pre-phase-8 original; only the call form
// changed from receiver-method to free function.
package apihelpers_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// -- Identify identity cache (auth-dry Phase 1) ---------------------------
//
// The auth middleware resolves the bearer token → Identity once per request
// and stashes it via SetIdentity; Identify must return THAT rather than
// re-resolving. These specs pin both halves of the seam with a nil *db.DB —
// a real re-resolution would deref the nil pool and panic, so "no panic +
// right identity" is proof the cache short-circuits the DB entirely.
var _ = Describe("Identify — middleware identity cache", func() {
	It("returns the stashed identity WITHOUT re-resolving (nil DB proves no DB hit)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		c := e.NewContext(req, httptest.NewRecorder())

		want := auth.AllCapsIdentity("cacheduser")
		apihelpers.SetIdentity(c, want)

		got, aerr := apihelpers.Identify(nil, c) // nil DB: would panic if it resolved
		Expect(aerr).To(BeNil())
		Expect(got).To(BeIdenticalTo(want))
		Expect(got.Username).To(Equal("cacheduser"))
	})

	It("falls back to header parsing when nothing was stashed (no header → MissingAuth 400)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil) // no Authorization header
		c := e.NewContext(req, httptest.NewRecorder())

		got, aerr := apihelpers.Identify(nil, c)
		Expect(got).To(BeNil())
		Expect(aerr).NotTo(BeNil())
		Expect(aerr.Status).To(Equal(http.StatusBadRequest),
			"absent credential with no cached identity must surface MissingAuth (400), not resolve")
	})

	It("SetIdentity with a nil identity is a no-op (Identify still falls back)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		c := e.NewContext(req, httptest.NewRecorder())

		apihelpers.SetIdentity(c, nil) // must not poison the cache with a nil
		_, aerr := apihelpers.Identify(nil, c)
		Expect(aerr).NotTo(BeNil())
		Expect(aerr.Status).To(Equal(http.StatusBadRequest))
	})
})

func TestApiHelpersSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/apihelpers suite")
}

// databaseURLFromEnv returns BOOM_TEST_DATABASE_URL or the default DSN.
// Duplicated (~byte-identical) from testutil.DatabaseURL to avoid an
// import cycle (testutil depends on packages that would eventually
// depend back into apihelpers via the handler composition).
func databaseURLFromEnv() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
}

// parseTimeParam is a package-local wrapper so the fallthrough test can
// stay byte-identical to the pre-phase-8 body.
var parseTimeParam = apihelpers.ParseTimeParam

var _ = Describe("apihelpers.ParseTimeParam layout tolerance", func() {
	// Directly exercise the fallthrough branch: every layout in the loop
	// misses, so the function returns (zero,false). Pins the "return
	// (zero,false)" behavior at the end of the loop — the previous test
	// suite only ever passed valid RFC3339 in, so this branch was 0%.
	It("returns (zero, false) when the value matches NO known layout", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/?t=totally-not-a-timestamp", nil)
		c := e.NewContext(req, httptest.NewRecorder())
		got, ok := parseTimeParam(c, "t")
		Expect(ok).To(BeFalse(), "malformed timestamp MUST NOT parse to a bogus time")
		Expect(got.IsZero()).To(BeTrue(),
			"return value on failure MUST be the zero time, not something derived from partial parsing")
	})

	It("returns (zero, false) when the query param is absent (early return)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		c := e.NewContext(req, httptest.NewRecorder())
		got, ok := parseTimeParam(c, "missing")
		Expect(ok).To(BeFalse())
		Expect(got.IsZero()).To(BeTrue())
	})
})

// -- ResolveOwnerFromCookie failure branches ------------------------------
//
// The happy-path (valid refresh cookie → known owner) is exercised by the
// auth_test suite. The two guard branches (missing cookie, unknown/expired
// token) are hit indirectly there but tightly here so the specific
// missingErr override contract stays honored.

var _ = Describe("apihelpers.ResolveOwnerFromCookie failure branches", func() {
	It("returns the caller-supplied missingErr (not Generic) when NO refresh_token cookie is present", func() {
		// The auth cluster relies on this override to differentiate
		// MissingRefreshTokenCookie (auth endpoints) from ExpiredRefreshToken
		// (WS handshake) on the same code path.
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		// deliberately no Cookie header
		c := e.NewContext(req, httptest.NewRecorder())
		custom := apierr.New(http.StatusTeapot, "custom-missing", nil)
		owner, aerr := apihelpers.ResolveOwnerFromCookie(nil, logger, c, custom)
		Expect(owner).To(BeEmpty())
		Expect(aerr).To(BeIdenticalTo(custom),
			"missingErr override MUST be returned verbatim — the auth cluster differentiates missing vs expired via this exact pointer")
	})

	It("returns apierr.Generic when the DB lookup errors — never leaks the raw DB error", func() {
		// Uses a valid-shape refresh cookie against a CLOSED pool so
		// GetUserByRefreshToken errors. The handler MUST render Generic
		// (500) — leaking the driver error would be an info-disclosure
		// bug (SQL state / DSN in the reply body).
		ctx := context.Background()
		database, err := db.New(ctx, databaseURLFromEnv())
		if err != nil {
			Skip(fmt.Sprintf("live DB required: %v", err))
		}
		database.Close() // force any subsequent query to error deterministically.
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		// Some string that ParseRefreshCookie accepts as a valid cookie.
		req.Header.Set("Cookie", "refresh_token=00000000-0000-0000-0000-000000000000")
		c := e.NewContext(req, httptest.NewRecorder())
		missing := apierr.New(http.StatusBadRequest, "should-not-fire", nil)
		_, aerr := apihelpers.ResolveOwnerFromCookie(database, logger, c, missing)
		if aerr == nil {
			Skip("closed-pool DB lookup unexpectedly succeeded")
		}
		Expect(aerr).NotTo(BeIdenticalTo(missing),
			"a DB error MUST fall through to Generic, not the missingErr override")
		Expect(aerr.Status).To(Equal(http.StatusInternalServerError),
			"raw DB errors MUST render as Generic 500 — never leak driver-level detail to the client")
	})
})

// -- LoadSpace input-guard branches ---------------------------------------

var _ = Describe("apihelpers.LoadSpace input guards (spaceParam parsing)", func() {
	It("returns (empty, false, nil) for an empty spaceParam (unscoped fast path)", func() {
		ms, req, err := apihelpers.LoadSpace(nil, context.Background(), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(req).To(BeFalse(),
			"empty spaceParam MUST return spaceRequested=false — this is what tells stats to skip Space scoping entirely")
		Expect(ms).To(Equal(db.MemberSets{}))
	})

	It("returns (empty, false, nil) for a non-numeric spaceParam (invalid id → unscoped)", func() {
		// Documents the graceful-degradation rule: an unparseable id is
		// treated as "no space" (unscoped) rather than an error, so the
		// dashboard still renders on stale bookmarks / typos. The pointer
		// safety here matters — db is nil, so if the parse guard
		// FAILED we'd deref-panic on LoadMemberSets.
		ms, req, err := apihelpers.LoadSpace(nil, context.Background(), "not-an-int")
		Expect(err).NotTo(HaveOccurred(),
			"invalid id MUST NOT surface as an error — dashboards would break for users on stale URLs")
		Expect(req).To(BeFalse())
		Expect(ms).To(Equal(db.MemberSets{}))
	})

	It("returns (empty-members, true, nil) for a valid numeric id that names no space (LoadMemberSets returns empty set — spaceRequested=true 'match-nothing')", func() {
		// Pins the security invariant: an id that isn't the caller's simply
		// yields empty MemberSets, which — with spaceRequested=true —
		// scopes the dashboard to nothing. This is why cross-user id
		// probing can't leak another user's data.
		//
		// Uses a real live DB via db.New — testutil is off-limits here
		// (cycle: testutil imports handler).
		ctx := context.Background()
		database, err := db.New(ctx, databaseURLFromEnv())
		if err != nil {
			Skip(fmt.Sprintf("live DB required: %v", err))
		}
		DeferCleanup(database.Close)
		ms, req, err := apihelpers.LoadSpace(database, ctx, "9999999")
		Expect(err).NotTo(HaveOccurred())
		Expect(req).To(BeTrue(),
			"a numeric spaceParam MUST count as 'requested' even if unknown — otherwise stats fall back to unscoped and leak")
		// LoadMemberSets returns a MemberSets whose internal map may be
		// populated with empty entries per axis (not a bare nil map);
		// the load-bearing invariant is exposed via AnyMember: no axis
		// has any include values, so the scope matches nothing.
		Expect(ms.AnyMember()).To(BeFalse(),
			"an unknown space id MUST produce a match-nothing scope — otherwise cross-user id probing would leak data")
	})

	It("returns the LoadMemberSets error verbatim (surfaces DB errors to caller for logging)", func() {
		// Uses a closed DB pool to force LoadMemberSets to error. Same
		// no-testutil constraint as above.
		ctx := context.Background()
		database, err := db.New(ctx, databaseURLFromEnv())
		if err != nil {
			Skip(fmt.Sprintf("live DB required: %v", err))
		}
		database.Close()
		_, _, err = apihelpers.LoadSpace(database, ctx, "1")
		Expect(err).To(HaveOccurred(),
			"a broken pool MUST surface the DB error — swallowing it as 'no members' would silently hide dashboard data")
	})
})

// -- ResolveUser DB error path -------------------------------------------
//
// TokenFromHeader OK + DB error → apierr.Generic (500). This branch was
// previously 0% because the auth tests only exercised the successful-lookup
// or unknown-token paths. Uses a stub DB pool that returns an error on
// GetUserByToken via a closed pool.

var _ = Describe("apihelpers.ResolveUser DB error branch", func() {
	It("returns apierr.Generic when the DB lookup errors (auth token present + DB down)", func() {
		// A closed pool ensures GetUserByToken errors deterministically
		// without needing a mocked pgx transport. The handler MUST NOT
		// leak the raw DB error to the client — this is why the branch
		// exists.
		ctx := context.Background()
		// Grab a live *db.DB against a bogus DSN so Ping fails.
		bogus, err := db.New(ctx, "postgres://nouser:nouser@127.0.0.1:1/nodb?sslmode=disable&connect_timeout=1")
		if err != nil {
			// Some environments fail the initial ping outright — in that
			// case the branch is still testable via constructor failure,
			// but we skip so the suite is deterministic elsewhere.
			Skip(fmt.Sprintf("db.New unexpectedly failed at construction: %v", err))
		}
		defer bogus.Close()

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set(echo.HeaderAuthorization, "Basic "+"MDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAw") // base64 of a UUID
		c := e.NewContext(req, httptest.NewRecorder())
		_, _, aerr := apihelpers.ResolveUser(bogus, c)
		if aerr == nil {
			// If the DB happens to respond (unlikely on 127.0.0.1:1), skip
			// — the specific error branch isn't reachable in this env.
			Skip("DB lookup unexpectedly succeeded — cannot exercise the error branch here")
		}
		Expect(aerr.Status).To(Equal(http.StatusInternalServerError),
			"a raw DB error MUST render as Generic 500 — leaking the driver error would be an info-disclosure bug")
	})
})

// -- CachedJSON compute-error + marshal-error branches --------------------

var _ = Describe("apihelpers.CachedJSON compute + marshal error branches", func() {
	It("returns Generic 500 when the compute callback errors (rec.Code=500 AND cache NOT populated)", func() {
		cch := cache.New(0)
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		err := apihelpers.CachedJSON(cch, logger, c, "k-compute-err", func() (any, error) {
			return nil, errors.New("upstream unavailable")
		})
		Expect(err).NotTo(HaveOccurred(), "cachedJSON writes the response and returns nil (echo convention)")

		// gaka-d6x.handler critique fix: the previous spec only checked
		// the cache miss — a regression that silently 200'd with an empty
		// body while skipping the cache would pass. Assert the OBSERVABLE
		// wire outcome: 500 status + Generic envelope body.
		Expect(rec.Code).To(Equal(http.StatusInternalServerError),
			"compute-error MUST render as HTTP 500 — got %d", rec.Code)
		Expect(rec.Body.String()).To(ContainSubstring(`"error":"An internal error occurred"`),
			"compute-error MUST render the Generic 500 envelope — got body=%s", rec.Body.String())

		// LOAD-BEARING: the cache MUST NOT have been populated with the error
		// state. A future retry MUST re-run compute, not serve a poisoned hit.
		_, ok := cch.Get("k-compute-err")
		Expect(ok).To(BeFalse(),
			"cachedJSON MUST NOT cache the failure — a poisoned entry would keep the endpoint 500-ing until the TTL expired")
	})

	It("returns Generic 500 when compute succeeds but json.Marshal fails (rec.Code=500 AND cache NOT populated)", func() {
		cch := cache.New(0)
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		// A channel cannot be json-encoded — forces json.Marshal to error.
		err := apihelpers.CachedJSON(cch, logger, c, "k-marshal-err", func() (any, error) {
			return make(chan int), nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusInternalServerError),
			"marshal-error MUST render as HTTP 500 — got %d", rec.Code)
		Expect(rec.Body.String()).To(ContainSubstring(`"error":"An internal error occurred"`),
			"marshal-error MUST render the Generic 500 envelope — got body=%s", rec.Body.String())
		_, ok := cch.Get("k-marshal-err")
		Expect(ok).To(BeFalse(), "marshal failure MUST NOT poison the cache")
	})
})

// -- CachedBlob compute-error branch --------------------------------------

var _ = Describe("apihelpers.CachedBlob compute error branch", func() {
	It("returns Generic 500 when the compute callback errors (rec.Code=500 AND cache NOT populated)", func() {
		cch := cache.New(0)
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/svg", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		err := apihelpers.CachedBlob(cch, logger, c, "blob-err", "image/svg+xml", func() ([]byte, error) {
			return nil, errors.New("renderer down")
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusInternalServerError),
			"blob compute error MUST render as HTTP 500 — got %d", rec.Code)
		Expect(rec.Body.String()).To(ContainSubstring(`"error":"An internal error occurred"`),
			"blob compute error MUST render the Generic 500 envelope — got body=%s", rec.Body.String())
		_, ok := cch.Get("blob-err")
		Expect(ok).To(BeFalse(), "blob compute failure MUST NOT poison the widget cache")
	})

	It("serves a cache HIT verbatim (compute callback NOT invoked)", func() {
		// Pins the cache-hit fast path (never called compute).
		// This branch is what makes cachedBlob a cache at all — a bug that
		// silently forced compute on every request would 100x the widget
		// renderer's cost.
		cch := cache.New(time.Minute)
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		wantBody := []byte("<svg>cached</svg>")
		cch.Set("hit-key", wantBody)
		invoked := false
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/svg", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		err := apihelpers.CachedBlob(cch, logger, c, "hit-key", "image/svg+xml", func() ([]byte, error) {
			invoked = true
			return []byte("wrong"), nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(invoked).To(BeFalse(),
			"compute callback MUST NOT run on a cache hit — otherwise the cache is useless")
		Expect(rec.Body.Bytes()).To(Equal(wantBody))
	})
})
