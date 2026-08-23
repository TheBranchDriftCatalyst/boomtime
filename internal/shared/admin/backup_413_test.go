// backup_413_test.go — boom-d6x.handler: exercise the DBImport oversize
// upload branch (>BOOM_RESTORE_MAX_BYTES) → 413 + env-var hint. Kept as
// its own file (external package) so os.Setenv shenanigans stay isolated.
//
// Named invariant:
//
//	"oversize archive → 413 with 'BOOM_RESTORE_MAX_BYTES' hint" — a body
//	exceeding the configured cap is rejected before validation runs; the
//	response body names the env var so the operator can tune it. Uses a
//	tiny cap (16 bytes) to keep the test fast and memory-safe.
package admin_test

import (
	"context"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("DBImport oversize upload (boom-d6x.handler)", func() {
	It("upload > BOOM_RESTORE_MAX_BYTES → 413 with env-var hint", func() {
		prev, had := os.LookupEnv("BOOM_RESTORE_MAX_BYTES")
		Expect(os.Setenv("BOOM_RESTORE_MAX_BYTES", "16")).To(Succeed())
		DeferCleanup(func() {
			if had {
				_ = os.Setenv("BOOM_RESTORE_MAX_BYTES", prev)
			} else {
				_ = os.Unsetenv("BOOM_RESTORE_MAX_BYTES")
			}
		})
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "backup413"))
		e := hz.Router()
		_, token := hz.MintUser("dbimp_big_g")

		// 100 bytes >> 16-byte cap.
		big := make([]byte, 100)
		rec := doRawG(e, http.MethodPost,
			"/api/v1/users/current/db/import?confirm=replace-all-data",
			token, big)
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"expected 413 for oversize upload; got %d body=%s", rec.Code, rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("BOOM_RESTORE_MAX_BYTES"),
			"error should hint the env var; got %s", rec.Body.String())
	})

	It("garbage (non-zip) upload of legal size → 400 with 'not a valid backup archive' hint", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "backup400"))
		e := hz.Router()
		_, token := hz.MintUser("dbimp_notzip_g")

		// Ensure no leftover running jobs from other tests short-circuit us.
		_, err := hz.DB.MarkRunningJobsFailed(context.Background(), "backup_413 cleanup")
		Expect(err).NotTo(HaveOccurred())

		rec := doRawG(e, http.MethodPost,
			"/api/v1/users/current/db/import?confirm=replace-all-data",
			token, []byte("this is not a zip file"))
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"body=%s", rec.Body.String())
	})
})
