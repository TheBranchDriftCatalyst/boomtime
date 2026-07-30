// wakatime_key_ginkgo_test.go — ginkgo mirror of wakatime_key_test.go (gaka-bi2).
// 1:1 case map (1 stdlib TestXxx):
//
//	TestSaveWakatimeKey_BodySizeCap_413 → SaveWakatimeKey body-size cap > "5 KiB body → 413, no probe"
package identity_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("SaveWakatimeKey body-size cap (gaka-bi2)", func() {
	It("rejects a 5 KiB body with 413 before probeWakatimeKey runs", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("wkkey_413")

		big := strings.Repeat("a", 5000)
		body := []byte(`{"key":"` + big + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/wakatime_key", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"oversize wakatime_key POST: any other status would prove the outbound probe ran on the payload — this cap closes that amplifier. body=%s",
			rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("payload too large"))
	})
})
