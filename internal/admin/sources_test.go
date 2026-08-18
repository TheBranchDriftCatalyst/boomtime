// sources_test.go — gaka-d6x.handler: cover SourceHealth.
// Named invariants:
//
//	"unauth → 4xx" — the endpoint MUST require a token; a missing token
//	returns 4xx BEFORE the DB query runs.
//
//	"per-user scoping: bob's sources never appear in alice's list" — the
//	ListSourceHealth query is owner-scoped. Both users insert (plugin, machine)
//	pairs; alice's response never mentions bob's plugin/machine names.
//
//	"empty list → {sources: []} (never null)" — a brand-new user with no
//	heartbeats returns an empty array. The FE contract requires an array,
//	not JSON null.
package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("SourceHealth (gaka-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx (no DB touch)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/sources/health", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("empty user returns a JSON array, not null", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("srcH_empty")

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/sources/health", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var env struct {
			Sources []map[string]any `json:"sources"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed(),
			"decode: %s", rec.Body.String())
		Expect(env.Sources).NotTo(BeNil(),
			"sources must be [] not null: body=%s", rec.Body.String())
		Expect(env.Sources).To(BeEmpty(),
			"fresh user should have zero sources; got %+v", env.Sources)
	})

	It("bob's plugins/machines never leak into alice's list", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("srcH_a")
		bobUser, _ := hz.MintUser("srcH_b")

		base := time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
		hz.Seeder(aliceUser).Projects("alpha").Seed(testutil.HB{
			Project: "alpha", TS: base, Entity: "a.go", Ty: "file",
			Plugin: "alice-vim-plugin", Machine: "alice-mac", Editor: "vim",
		})
		hz.Seeder(bobUser).Projects("beta").Seed(testutil.HB{
			Project: "beta", TS: base, Entity: "b.go", Ty: "file",
			Plugin: "bob-vscode-plugin", Machine: "bob-linux", Editor: "vscode",
		})

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/sources/health", aliceTok, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("alice-vim-plugin"),
			"alice should see her own plugin: %s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("bob-vscode-plugin"),
			"alice leaked bob's plugin: %s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("bob-linux"),
			"alice leaked bob's machine: %s", rec.Body.String())
	})
})
