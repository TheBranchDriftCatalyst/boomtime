// books_monitor_settings_http_test.go — HTTP coverage for the persistent
// reading-monitor control surface: GET/PUT /api/v1/admin/books/reading-monitor.
// Named invariants:
//
//   - admin gate: unauth'd ⇒ 4xx, non-admin ⇒ 403 (no allowlist leak);
//   - GET defaults: a fresh admin reads {enabled:false, mode:"debounced",
//     activeBooks:0, lastPingAt:null};
//   - PUT toggles enabled + mode and the change round-trips on the next GET;
//   - a PARTIAL PUT (enabled only) leaves mode untouched;
//   - an invalid mode ⇒ 400.
package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"

	booksadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/books/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

type readingMonitorViewT struct {
	Enabled        bool    `json:"enabled"`
	Mode           string  `json:"mode"`
	ActiveBooks    int     `json:"activeBooks"`
	LastPingAt     *string `json:"lastPingAt"`
	Recommendation *struct {
		DetectSecs        int `json:"detectSecs"`
		CaptureSecs       int `json:"captureSecs"`
		IdleSecs          int `json:"idleSecs"`
		MedianAdvanceSecs int `json:"medianAdvanceSecs"`
		P90AdvanceSecs    int `json:"p90AdvanceSecs"`
		SampleCount       int `json:"sampleCount"`
	} `json:"recommendation"`
}

// booksMonitorRouter builds a router with BOOM_FEATURE_BOOKS on so the
// reading-monitor routes register (they 404 when the feature is off).
func booksMonitorRouter(hz *testutil.Harness) *echo.Echo {
	hz.Cfg.FeatureBooks = true
	e := echo.New()
	g := e.Group("/api/v1/admin")
	booksadmin.Register(g, booksadmin.New(hz.DB, hz.Cfg, slog.New(slog.NewTextHandler(io.Discard, nil))))
	return e
}

var _ = Describe("Admin reading-monitor settings: auth gates", func() {
	It("rejects unauth'd (4xx) and non-admin (403) callers", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := booksMonitorRouter(hz)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/books/reading-monitor", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400), "unauth'd must be rejected")

		nonAdmin, nonAdminToken := hz.MintUser("rm_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-dave": {}}
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/admin/books/reading-monitor", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).NotTo(ContainSubstring(nonAdmin))
	})
})

var _ = Describe("Admin reading-monitor settings: GET/PUT toggle", func() {
	It("defaults off/debounced and round-trips enable + mode changes", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := booksMonitorRouter(hz)
		user, token := hz.MintUser("rm_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// Fresh admin: disabled, debounced, no active books, never pinged.
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/books/reading-monitor", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var v readingMonitorViewT
		Expect(json.Unmarshal(rec.Body.Bytes(), &v)).To(Succeed())
		Expect(v.Enabled).To(BeFalse())
		Expect(v.Mode).To(Equal("debounced"))
		Expect(v.ActiveBooks).To(Equal(0))
		Expect(v.LastPingAt).To(BeNil())
		Expect(v.Recommendation).To(BeNil(), "no advances observed yet → recommendation null")

		// Enable + switch to verbose.
		rec = doJSONReqG(e, http.MethodPut, "/api/v1/admin/books/reading-monitor", token,
			map[string]any{"enabled": true, "mode": "verbose"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &v)).To(Succeed())
		Expect(v.Enabled).To(BeTrue())
		Expect(v.Mode).To(Equal("verbose"))

		// Round-trips on the next GET.
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/admin/books/reading-monitor", token, nil)
		Expect(json.Unmarshal(rec.Body.Bytes(), &v)).To(Succeed())
		Expect(v.Enabled).To(BeTrue())
		Expect(v.Mode).To(Equal("verbose"))

		// Partial PUT (enabled only) leaves mode = verbose untouched.
		rec = doJSONReqG(e, http.MethodPut, "/api/v1/admin/books/reading-monitor", token,
			map[string]any{"enabled": false})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &v)).To(Succeed())
		Expect(v.Enabled).To(BeFalse())
		Expect(v.Mode).To(Equal("verbose"), "partial update must not reset mode")
	})

	It("derives a recommendation from observed advance samples", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := booksMonitorRouter(hz)
		user, token := hz.MintUser("rm_admin_reco")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// Seed 5 observed advance intervals of 40s into the rolling window → p50=40,
		// so captureSecs floors at 60 and sampleCount=5.
		ctx := context.Background()
		base := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
		for i := 0; i < 5; i++ {
			at := base.Add(time.Duration(i) * time.Minute)
			Expect(hz.DB.InsertReadingMonitorAdvance(ctx, user, "kindle", 40, 50, at)).To(Succeed())
		}

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/books/reading-monitor", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var v readingMonitorViewT
		Expect(json.Unmarshal(rec.Body.Bytes(), &v)).To(Succeed())

		Expect(v.Recommendation).NotTo(BeNil(), "5 advance samples ≥ min → a recommendation")
		Expect(v.Recommendation.SampleCount).To(Equal(5))
		Expect(v.Recommendation.MedianAdvanceSecs).To(Equal(40))
		Expect(v.Recommendation.P90AdvanceSecs).To(Equal(40))
		Expect(v.Recommendation.CaptureSecs).To(Equal(60), "capture floors at the 60s fidelity floor")
		Expect(v.Recommendation.IdleSecs).To(Equal(180), "idle floors at 180s")
		Expect(v.Recommendation.DetectSecs).To(Equal(120), "detect = max(2*capture, p90*3)")
	})

	It("rejects an invalid mode with 400", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := booksMonitorRouter(hz)
		user, token := hz.MintUser("rm_admin_badmode")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		rec := doJSONReqG(e, http.MethodPut, "/api/v1/admin/books/reading-monitor", token,
			map[string]any{"mode": "firehose"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})
})
