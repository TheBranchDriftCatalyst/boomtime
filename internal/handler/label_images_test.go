// label_images_ginkgo_test.go — ginkgo mirror of label_images_test.go (gaka-myv).
// 1:1 case map (3 stdlib TestXxx):
//
//	TestLabelImage_Served                   → LabelImage > "serves bytes + Cache-Control verbatim"
//	TestLabelImage_NotFound                 → LabelImage > "unknown id → 404"
//	TestLabelImage_IgnoresCacheBustParam    → LabelImage > "?v= cache-bust param is ignored"
package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithLabelImagesGinkgo — mirror of the stdlib file's helper.
// Distinct name avoids duplicate-symbol collision.
var _ = Describe("LabelImage (gaka-myv)", func() {
	It("serves saved bytes with the exact Cache-Control envelope", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		id := "test-served-late-night-coder-g"
		want := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 'x', 'y', 'z'}
		Expect(hz.DB.SaveLabelImage(context.Background(), id, want, "image/png", "flux_schnell_fast", "test prompt", nil)).To(Succeed())
		DeferCleanup(func() { _ = hz.DB.DeleteLabelImage(context.Background(), id) })

		req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/"+id+"/image", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("public, max-age=31536000, immutable"),
			"regenerate cache-bust contract expects the exact envelope")
		got, _ := io.ReadAll(rec.Body)
		Expect(got).To(Equal(want))
	})

	It("returns 404 for an unknown id (public endpoint, no auth leakage)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/no-such-label-xyz-g/image", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("ignores the ?v=<epoch> cache-bust parameter — same bytes served either way", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		id := "test-bust-param-g"
		body := []byte("fake-png-bytes")
		Expect(hz.DB.SaveLabelImage(context.Background(), id, body, "image/png", "", "", nil)).To(Succeed())
		DeferCleanup(func() { _ = hz.DB.DeleteLabelImage(context.Background(), id) })

		for _, v := range []string{"", "?v=1", "?v=999999999"} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/"+id+"/image"+v, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "v=%q body=%s", v, rec.Body.String())
			got, _ := io.ReadAll(rec.Body)
			Expect(got).To(Equal(body), "v=%q bytes changed", v)
		}
	})
})
