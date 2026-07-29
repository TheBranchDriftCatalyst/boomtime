// backup_ginkgo_test.go — ginkgo mirror of backup_test.go.
// 1:1 case map (2 stdlib TestXxx):
//   TestDBImportGuards       → DBImport guards > "auth/confirm gating (no auth, bad token, missing confirm, garbage body)"
//   TestDBBackupRoundTripHTTP→ backup round-trip > "export → mutate → import restores state and returns summary"
package handler_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("DBImport guards", func() {
	It("gates auth, confirm, and body-shape correctly (no data touched on failure)", func() {
		// Isolated DB: the active-import rejection scans import_jobs across ALL
		// owners, so leftover jobs on the shared test DB would flip answers.
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "backupg"))
		e := hz.Router()
		_, token := hz.MintUser("backupguard_g")
		ctx := context.Background()

		// Neutralize any queued/running jobs a previously-aborted run left behind.
		_, err := hz.DB.MarkRunningJobsFailed(ctx, "backup_ginkgo cleanup")
		Expect(err).NotTo(HaveOccurred())

		// No auth header → 400 (missing auth).
		rec := doRawG(e, http.MethodPost, "/api/v1/users/current/db/import?confirm=replace-all-data", "", nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "no auth: got %d", rec.Code)

		// Bogus token → 403.
		rec = doRawG(e, http.MethodGet, "/api/v1/users/current/db/export", "bogus", nil)
		Expect(rec.Code).To(Equal(http.StatusForbidden), "bad token export: got %d", rec.Code)

		// Auth'd but missing the confirm param → 400, and nothing is truncated.
		user2, _ := hz.MintUser("backupguard2_g")
		rec = doRawG(e, http.MethodPost, "/api/v1/users/current/db/import", token, []byte("zipzip"))
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "missing confirm: got %d", rec.Code)

		var n int
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE username=$1`, user2).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1), "guarded import touched data")

		// Confirmed but not a zip → 400.
		rec = doRawG(e, http.MethodPost, "/api/v1/users/current/db/import?confirm=replace-all-data", token, []byte("not a zip"))
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "garbage archive: got %d", rec.Code)
	})
})

var _ = Describe("backup round-trip (export → mutate → import)", func() {
	It("restores state, returns a summary, and the original token still works", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "backupg"))
		e := hz.Router()
		user, token := hz.MintUser("backuprt_g")
		ctx := context.Background()

		base := time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("alpha")
		sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 3, 60)

		var beatsBefore int64
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats`).Scan(&beatsBefore)).To(Succeed())

		// Export.
		rec := doRawG(e, http.MethodGet, "/api/v1/users/current/db/export", token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "export: body=%s", rec.Body.String())
		Expect(rec.Header().Get("Content-Type")).To(Equal("application/zip"))
		Expect(rec.Header().Get("Content-Disposition")).NotTo(BeEmpty(),
			"export missing Content-Disposition")

		archive := rec.Body.Bytes()
		_, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		Expect(err).NotTo(HaveOccurred(), "export did not produce a valid zip")

		// Mutate after the export; the import must roll this back.
		sd.Seed(testutil.HB{Project: "alpha", Entity: "extra.go", TS: base.Add(48 * time.Hour)})

		// Import.
		rec = doRawG(e, http.MethodPost, "/api/v1/users/current/db/import?confirm=replace-all-data", token, archive)
		Expect(rec.Code).To(Equal(http.StatusOK), "import: body=%s", rec.Body.String())

		var summary struct {
			GooseVersion int64            `json:"gooseVersion"`
			TotalRows    int64            `json:"totalRows"`
			Tables       map[string]int64 `json:"tables"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &summary)).To(Succeed(), "body=%s", rec.Body.String())
		Expect(summary.Tables["heartbeats"]).To(Equal(beatsBefore))
		Expect(summary.GooseVersion).NotTo(BeZero(), "suspicious summary: %+v", summary)
		Expect(summary.TotalRows).NotTo(BeZero(), "suspicious summary: %+v", summary)

		var beatsAfter int64
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats`).Scan(&beatsAfter)).To(Succeed())
		Expect(beatsAfter).To(Equal(beatsBefore),
			"post-export mutation must be gone after restore")

		// The token used for the request was part of the dump, so it still works.
		rec = doRawG(e, http.MethodGet, "/api/v1/users/current/db/export", token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"token no longer valid after restoring its own backup: %d", rec.Code)
	})
})
