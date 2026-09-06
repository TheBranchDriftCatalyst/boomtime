// initializer_secrets_test.go — regression test for the Swagger-UI
// response-history secret scrub (audit 2026-09-06, api-plumbing low;
// internal/shared/openapi/initializer.js:934).
//
// The finding: SECRET_KEYS matched api_token/password/refresh_token/… but NOT
// the bare "token" key. POST /auth/login and POST /auth/refresh_token answer
// {"token":"<live access token>","tokenExpiry":…}, so with the opt-in history
// toggle on, scrubBody passed those bodies straight through and writeHistory
// persisted a WORKING credential into localStorage['boom-response-history'] —
// surviving tab close, readable by any later XSS or shared-machine user, and
// contradicting ui.go's "Access tokens live in memory only" posture.
//
// Rather than grepping the file for the word "token" (which would pass on a
// comment), this extracts the actual SECRET_KEYS pattern from the served JS,
// compiles it as a Go regexp — the pattern uses only alternation and an
// optional '?', so RE2 semantics match JS here — and runs REAL response bodies
// through it.
package openapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/openapi"
)

// secretKeysLine pulls `const SECRET_KEYS = /.../i;` out of the served JS and
// captures the pattern source.
var secretKeysLine = regexp.MustCompile(`const SECRET_KEYS = /(.*)/i;`)

func servedInitializer() string {
	h := openapi.UIHandler("/api/docs")
	req := httptest.NewRequest(http.MethodGet, "/api/docs/swagger-initializer.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	Expect(rec.Code).To(Equal(http.StatusOK))
	body, err := io.ReadAll(rec.Result().Body)
	Expect(err).NotTo(HaveOccurred())
	return string(body)
}

var _ = Describe("Swagger-UI response-history secret scrub (audit 2026-09-06)", func() {
	var secretKeys *regexp.Regexp

	BeforeEach(func() {
		m := secretKeysLine.FindStringSubmatch(servedInitializer())
		Expect(m).To(HaveLen(2),
			"could not locate `const SECRET_KEYS = /.../i;` in the served initializer — "+
				"if the scrub was renamed or removed, this test must be updated, not deleted")
		re, err := regexp.Compile("(?i)" + m[1])
		Expect(err).NotTo(HaveOccurred(), "SECRET_KEYS pattern is not RE2-compatible: %s", m[1])
		secretKeys = re
	})

	It("redacts the /auth/login and /auth/refresh_token response bodies (bare \"token\" key)", func() {
		// The exact shape of model.LoginResponse.
		login := `{"token":"eyJhbGciOiJIUzI1NiJ9.live-access-token","tokenExpiry":"2026-09-07T00:00:00Z","tokenUsername":"panda"}`
		Expect(secretKeys.MatchString(login)).To(BeTrue(),
			"a login/refresh body carrying a LIVE access token under the bare \"token\" key was not "+
				"recognised as secret — the opt-in history would persist it to localStorage")
	})

	It("still redacts every key it recognised before (no regression in the widened pattern)", func() {
		for _, body := range []string{
			`{"apiToken":"boom_live_abc"}`,
			`{"api_token":"boom_live_abc"}`,
			`{"password":"hunter2"}`,
			`{"refresh_token":"rt_abc"}`,
			`{"secret":"s"}`,
			`{"session":"sid"}`,
			`{"cookie":"c=1"}`,
			`{"authorization":"Bearer x"}`,
		} {
			Expect(secretKeys.MatchString(body)).To(BeTrue(), "no longer redacted: %s", body)
		}
	})

	It("also covers the adjacent token spellings a future endpoint could use", func() {
		for _, body := range []string{
			`{"access_token":"at_abc"}`,
			`{"accessToken":"at_abc"}`,
			`{"jwt":"eyJ..."}`,
		} {
			Expect(secretKeys.MatchString(body)).To(BeTrue(), "not redacted: %s", body)
		}
	})

	// The pattern is quote-delimited but not key-position aware (it never was —
	// `"password"` appearing as a VALUE has always matched too), so the bar here
	// is "the history feature still keeps ordinary bodies", not "zero false
	// positives". Over-redaction costs a history entry; under-redaction costs a
	// live credential in localStorage.
	It("leaves ordinary response bodies alone (the history feature still works)", func() {
		for _, body := range []string{
			`{"status":"ok","version":"v1.2.3"}`,
			`{"kind":"groups","groups":[{"key":"boomtime","value":3600}]}`,
			`{"username":"panda","role":"admin"}`,
			`{"kind":"rows","rows":[{"title":"Dune","finishedAt":"2026-01-01T00:00:00Z"}],"total":1}`,
		} {
			Expect(secretKeys.MatchString(body)).To(BeFalse(), "over-redacted an ordinary body: %s", body)
		}
	})
})
