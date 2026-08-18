// logs_ginkgo_test.go — ginkgo mirror of logs_test.go (gaka-awh.2).
// 1:1 case map (3 stdlib TestXxx):
//
//	TestServerLogs_OwnerFilterIsWired       → ServerLogs > "owner filter is wired (A sees own+server, B messages absent)"
//	TestServerLogs_UnauthenticatedIsRejected→ ServerLogs > "unauthenticated → 4xx (fail-closed)"
//	TestServerLogs_EmptyHubYieldsEmptyArray → ServerLogs > "empty hub → {logs:[]} not null"
package meta_test

import (
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// routerWithLogsG — mirror of the stdlib routerWithLogs.
// publishFixtureG — mirror of the stdlib publishFixture.
func publishFixtureG(hub *logging.LogHub, userA, userB string) (aMsgs, bMsgs, serverMsgs []string) {
	aMsgs = []string{"wakatime key saved (Ag-1)", "wakatime key cleared (Ag-2)", "password changed (Ag-3)"}
	bMsgs = []string{"wakatime key saved (Bg-1)", "wakatime key cleared (Bg-2)", "password changed (Bg-3)"}
	serverMsgs = []string{"healthz served (g)", "migrations up (g)", "server started (g)"}
	for _, m := range aMsgs {
		hub.Publish(logging.LogEntry{Time: time.Now(), Level: "INFO", Msg: m, Attrs: map[string]string{logging.OwnerAttrKey: userA}})
	}
	for _, m := range bMsgs {
		hub.Publish(logging.LogEntry{Time: time.Now(), Level: "INFO", Msg: m, Attrs: map[string]string{logging.OwnerAttrKey: userB}})
	}
	for _, m := range serverMsgs {
		hub.Publish(logging.LogEntry{Time: time.Now(), Level: "INFO", Msg: m})
	}
	return
}

// decodeLogsResponseG — mirror of the stdlib helper.
func decodeLogsResponseG(body []byte) []string {
	var env struct {
		Logs []logging.LogEntry `json:"logs"`
	}
	Expect(json.Unmarshal(body, &env)).To(Succeed(), "decode: body=%s", string(body))
	out := make([]string, 0, len(env.Logs))
	for _, e := range env.Logs {
		out = append(out, e.Msg)
	}
	return out
}

var _ = Describe("ServerLogs owner-scope filter (gaka-awh.2)", func() {
	It("filters cross-tenant records: A sees own+server-scope, never B's", func() {
		hz := testutil.NewHarness(GinkgoT())
		hub := logging.NewLogHub(64)
		hz.H.Meta.LogHub = hub // override the nil harness default
		e := hz.Router()

		userA, tokenA := hz.MintUser("awhg_A")
		userB, _ := hz.MintUser("awhg_B")

		aMsgs, bMsgs, serverMsgs := publishFixtureG(hub, userA, userB)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/logs?limit=200", tokenA, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "GET /logs: body=%s", rec.Body.String())

		got := decodeLogsResponseG(rec.Body.Bytes())
		Expect(got).To(HaveLen(len(aMsgs) + len(serverMsgs)))
		for _, m := range aMsgs {
			Expect(got).To(ContainElement(m), "viewA missing own message %q", m)
		}
		for _, m := range serverMsgs {
			Expect(got).To(ContainElement(m), "viewA missing server-scope message %q", m)
		}
		// LOAD-BEARING: none of user B's records may appear.
		for _, m := range bMsgs {
			Expect(got).NotTo(ContainElement(m),
				"cross-tenant leak: viewA saw user-B message %q", m)
		}
	})

	It("rejects unauthenticated calls with 4xx (fail-closed, not silent partial data)", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.H.Meta.LogHub = logging.NewLogHub(8)
		e := hz.Router()

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/logs", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500),
			"unauthenticated /api/v1/logs: got %d, want 4xx body=%s", rec.Code, rec.Body.String())
	})

	It("empty hub returns {logs:[]} — never null (FE contract)", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.H.Meta.LogHub = logging.NewLogHub(8)
		e := hz.Router()
		_, token := hz.MintUser("awhg_empty")

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "empty hub GET /logs: body=%s", rec.Body.String())

		body := rec.Body.String()
		Expect(body).NotTo(BeEmpty())
		Expect(body).NotTo(Or(Equal("{\"logs\":null}\n"), Equal("{\"logs\":null}")),
			"empty hub returned null-shaped logs: body=%q", body)
	})
})
