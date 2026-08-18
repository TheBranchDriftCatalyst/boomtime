// meta_ginkgo_test.go — ginkgo mirror of meta_test.go.
// 1:1 case map (2 stdlib TestXxx w/ subtests):
//
//	TestVersionEndpoint    → Version endpoint > 2 Its (configured version / dev fallback)
//	TestChangelogEndpoint  → Changelog endpoint > "serves embedded MD verbatim"
package meta

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	boomtime "github.com/TheBranchDriftCatalyst/boomtime"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/labstack/echo/v5"
)

// metaHandlerGinkgo — mirror of the stdlib file's metaHandler helper without
// the *testing.T parameter (ginkgo specs use GinkgoT / Expect for reporting).
func metaHandlerGinkgo(ver string) *Handler {
	return &Handler{
		Cfg:    &config.Config{Version: ver},
		Logger: slog.Default(),
		Cache:  cache.New(0),
	}
}

var _ = Describe("Version endpoint", func() {
	It("returns the configured version", func() {
		h := metaHandlerGinkgo("v1.2.3")
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		Expect(h.Version(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))

		var got versionResponse
		Expect(json.NewDecoder(rec.Body).Decode(&got)).To(Succeed())
		Expect(got.Version).To(Equal("v1.2.3"))
	})

	It("falls back to 'dev' when cfg.Version is empty", func() {
		h := metaHandlerGinkgo("")
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		Expect(h.Version(c)).To(Succeed())
		var got versionResponse
		Expect(json.NewDecoder(rec.Body).Decode(&got)).To(Succeed())
		Expect(got.Version).To(Equal("dev"))
	})
})

var _ = Describe("Changelog endpoint", func() {
	It("serves the embedded CHANGELOG.md verbatim with a text/markdown type", func() {
		Expect(len(boomtime.ChangelogMD)).NotTo(BeZero(),
			"boomtime.ChangelogMD is empty; regenerate with `task changelog`")
		Expect(strings.HasPrefix(string(boomtime.ChangelogMD), "# Changelog")).
			To(BeTrue(), "embedded CHANGELOG.md must start with '# Changelog'")

		h := metaHandlerGinkgo("v1.0.0")
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/changelog", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		Expect(h.Changelog(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))

		ct := rec.Header().Get(echo.HeaderContentType)
		Expect(strings.HasPrefix(ct, "text/markdown")).To(BeTrue(),
			"Content-Type = %q, want text/markdown*", ct)
		Expect(rec.Body.Len()).To(Equal(len(boomtime.ChangelogMD)))
	})
})
