// standalone_identify_test.go — unit coverage for the STANDALONE catalyst-books
// single-owner short-circuit in apihelpers.Identify (boom-zp2s).
//
// These specs pin the exact seam cmd/catalyst-books relies on: with
// auth.SetStandaloneOwner pinned, Identify resolves EVERY caller to that one
// owner WITHOUT a token, cookie, or DB round-trip — and, crucially, WITHOUT the
// pin the host path is provably unchanged. A nil *db.DB is the proof harness: a
// real resolution would deref the nil pool and panic, so "no panic + fixed
// owner" is hard evidence the DB was never touched. The pin is a process-global,
// so every spec clears it in AfterEach to keep zero bleed into the host-path
// specs that share this package.
package apihelpers_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
)

var _ = Describe("Identify — standalone single-owner short-circuit (boom-zp2s)", func() {
	newCtx := func() *echo.Context {
		e := echo.New()
		// Deliberately NO Authorization header — the whole point is that the
		// standalone owner resolves without any credential.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/books/items", nil)
		return e.NewContext(req, httptest.NewRecorder())
	}

	AfterEach(func() {
		// Never leak the process-global pin into the host-path specs elsewhere
		// in this package (they assert MissingAuth / DB-resolution behavior).
		auth.SetStandaloneOwner("")
	})

	It("returns the pinned owner with NO token and NO DB lookup (nil DB proves it)", func() {
		auth.SetStandaloneOwner("owner")
		got, aerr := apihelpers.Identify(nil, newCtx()) // nil DB: any resolve panics
		Expect(aerr).To(BeNil())
		Expect(got).NotTo(BeNil())
		Expect(got.Username).To(Equal("owner"))
		Expect(got.Role).To(Equal(auth.RoleFull),
			"the standalone owner is a synthetic all-caps identity (full role, every capability)")
		Expect(got.Disabled).To(BeFalse())
	})

	It("honors a custom owner name from SetStandaloneOwner (not hardcoded 'owner')", func() {
		// BOOM_STANDALONE_OWNER is configurable; the identity must carry whatever
		// name was pinned, not a baked-in constant.
		auth.SetStandaloneOwner("librarian")
		got, aerr := apihelpers.Identify(nil, newCtx())
		Expect(aerr).To(BeNil())
		Expect(got.Username).To(Equal("librarian"))
	})

	It("short-circuit WINS over a middleware-cached identity (owner is fixed, not the cached user)", func() {
		// The standalone branch precedes the identity cache in Identify. Even a
		// stashed host identity must NOT override the pinned single owner — this
		// is the assertion that fails if the short-circuit were ever reordered
		// below the cache lookup.
		auth.SetStandaloneOwner("owner")
		c := newCtx()
		apihelpers.SetIdentity(c, auth.AllCapsIdentity("someone-else"))
		got, aerr := apihelpers.Identify(nil, c)
		Expect(aerr).To(BeNil())
		Expect(got.Username).To(Equal("owner"),
			"single-tenant is absolute — the pinned owner MUST override any cached identity")
	})

	It("does NOT short-circuit when unset — host path intact (no token → MissingAuth 400)", func() {
		// Default (host / test) state: StandaloneOwner() is false, so Identify
		// takes the normal resolution path. No credential + nothing cached is a
		// MissingAuth 400 — proving the short-circuit is GATED, not always-on. If
		// this regressed to always-on, the host binary would silently collapse
		// every request onto one owner.
		_, ok := auth.StandaloneOwner()
		Expect(ok).To(BeFalse(), "precondition: no standalone owner pinned by default")

		got, aerr := apihelpers.Identify(nil, newCtx())
		Expect(got).To(BeNil())
		Expect(aerr).NotTo(BeNil())
		Expect(aerr.Status).To(Equal(http.StatusBadRequest),
			"unset pin ⇒ host resolution ⇒ absent credential surfaces MissingAuth (400)")
	})

	It("SetStandaloneOwner(\"\") clears the pin (a blank name never activates single-owner mode)", func() {
		auth.SetStandaloneOwner("owner")
		auth.SetStandaloneOwner("") // clear it
		_, ok := auth.StandaloneOwner()
		Expect(ok).To(BeFalse())

		_, aerr := apihelpers.Identify(nil, newCtx())
		Expect(aerr).NotTo(BeNil(),
			"a cleared pin MUST fall back to the host path — never a synthetic owner")
		Expect(aerr.Status).To(Equal(http.StatusBadRequest))
	})
})
