// Tests for GET /api/v1/config/public (boom-93f.1.1). Internal package test
// so it can construct a *Handler with an explicit *config.Config and assert
// the response faithfully mirrors the flags — no DB, no auth.
package meta

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/labstack/echo/v5"
)

// callPublicConfig invokes the handler with the given Config and returns the
// typed response. The handler is on the apiroute seam, so it returns the value
// rather than writing it — the recorder is still threaded through to prove the
// handler itself touches neither the status code nor the body.
func callPublicConfig(cfg *config.Config) (*httptest.ResponseRecorder, PublicConfigResponse) {
	h := &Handler{Cfg: cfg}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/public", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	got, err := h.PublicConfig(c)
	Expect(err).NotTo(HaveOccurred())
	return rec, got
}

var _ = Describe("PublicConfig endpoint", func() {
	It("advertises today's defaults (local auth, registration on, billing off, beta on)", func() {
		// config.Load() with an unset env is the production default path; this
		// guards the Load() wiring, not just the handler mapping.
		rec, got := callPublicConfig(config.Load())
		// The seam owns the write now: the handler must not have touched the
		// response itself (a stray write would double-encode the body).
		Expect(rec.Body.Len()).To(BeZero())
		Expect(got.AuthProvider).To(Equal("local"))
		Expect(got.OIDCEnabled).To(BeFalse())
		Expect(got.RegistrationEnabled).To(BeTrue())
		Expect(got.BillingEnabled).To(BeFalse())
		Expect(got.BetaFlags).To(HaveKeyWithValue("user_registration", true))
	})

	It("reflects the OIDC provider (auth_provider=oidc → oidc_enabled)", func() {
		_, got := callPublicConfig(&config.Config{AuthProvider: "oidc", EnableRegistration: true, BetaUserRegistration: true})
		Expect(got.AuthProvider).To(Equal("oidc"))
		Expect(got.OIDCEnabled).To(BeTrue())
	})

	It("reflects registration disabled", func() {
		_, got := callPublicConfig(&config.Config{AuthProvider: "local", EnableRegistration: false, BetaUserRegistration: true})
		Expect(got.RegistrationEnabled).To(BeFalse())
	})

	It("reflects the billing switch", func() {
		_, got := callPublicConfig(&config.Config{AuthProvider: "local", FeatureBilling: true, BetaUserRegistration: true})
		Expect(got.BillingEnabled).To(BeTrue())
	})

	It("honors the server-side beta kill switch (beta_flags.user_registration=false)", func() {
		_, got := callPublicConfig(&config.Config{AuthProvider: "local", EnableRegistration: true, BetaUserRegistration: false})
		Expect(got.BetaFlags).To(HaveKeyWithValue("user_registration", false))
	})
})
