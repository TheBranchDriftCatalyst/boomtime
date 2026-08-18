// import_ginkgo_test.go — ginkgo mirror of import_test.go (gaka-6jm.8).
// 1:1 case map (1 stdlib TestXxx):
//
//	TestImportRequestDoesNotEagerlySaveTypedKey → ImportRequest > "does not eagerly persist typed apiToken; save is deferred to worker"
package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
	"github.com/labstack/echo/v5"
)

var _ = Describe("ImportRequest (gaka-6jm.8)", func() {
	It("does not eagerly persist the typed apiToken; save is deferred to worker terminal success", func() {
		hz := testutil.NewHarness(GinkgoT())
		user, token := hz.MintUser("import_noneager")

		// Build our own Handler with a REAL Worker (default harness passes nil
		// for the worker, and ImportRequest calls Worker.StartJob synchronously
		// before returning).
		workerCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		silent := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
		hub := importer.NewHub()
		worker := importer.NewWorker(workerCtx, hz.DB, silent, hub)
		cfg := &config.Config{Port: 8080, EnableRegistration: true, SessionExpiry: 24}
		h := handler.New(hz.DB, cfg, silent, worker, hub, nil)

		e := echo.New()
		e.POST("/auth/login", h.Identity.Login) // route table shim so echo doesn't 404 on middleware (gaka-8tn phase 4a)
		e.POST("/api/v1/users/current/import", h.Admin.ImportRequest)

		// Baseline: no saved key.
		_, has, err := hz.DB.GetEncryptedWakatimeKey(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(has).To(BeFalse(), "baseline: expected no saved key")

		// Build a well-formed import payload with a typed key.
		now := time.Now().UTC()
		body := map[string]any{
			"apiToken":  "waka_never_persist_me_eagerly",
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/import", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "import submit: body=%s", rec.Body.String())

		// LOAD-BEARING: the handler must NOT have written the ciphertext eagerly.
		_, has, err = hz.DB.GetEncryptedWakatimeKey(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(has).To(BeFalse(),
			"gaka-6jm.8 violated: handler eagerly persisted the typed key on submit")

		// Best-effort: cancel any running job before DB teardown so the worker's
		// terminal DB write doesn't race the test cleanup.
		_, _ = hz.DB.MarkRunningJobsFailed(context.Background(), "import_ginkgo cleanup")
	})
})
