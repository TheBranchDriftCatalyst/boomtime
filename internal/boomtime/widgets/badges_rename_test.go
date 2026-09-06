// badges_rename_test.go — boom-l827 (audit 2026-09-06) regression: BadgeSvg
// summed only `project = <badge subject>` on the raw heartbeats, with no
// rename-target expansion, so a badge minted for a MERGED project name
// reported only the fraction of time stored under that exact raw name while
// the equivalent widget link (expanded in boom-xuc via
// db.ProjectMemberSetWithRenames) showed the real total.
//
// Named invariants:
//
//	"merged badge counts every renamed source" — with "alpha-old" → "alpha",
//	the /badge/svg message covers alpha + alpha-old, not alpha alone.
//
//	"a hidden source stays hidden" — the boom-6jm.3 contract must survive the
//	expansion: hiding a source project must not let its time reappear in a
//	PUBLIC badge through a rename rule.
//
//	"no rename rules → unchanged" — the non-renamed badge total is untouched.
//
// The assertion is on the shields.io `message` the handler builds (captured
// from the stub upstream), i.e. the number a reader of the README actually
// sees — not on an internal helper.
package widgets_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// badgeMessageStub is a shields.io stand-in that records the last `message`
// query param it was asked to render.
type badgeMessageStub struct {
	srv  *httptest.Server
	last atomic.Value // string
}

func newBadgeMessageStub() *badgeMessageStub {
	s := &badgeMessageStub{}
	s.last.Store("")
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.last.Store(r.URL.Query().Get("message"))
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte("<svg>ok</svg>"))
	}))
	return s
}

func (s *badgeMessageStub) message() string { return s.last.Load().(string) }

// seedBadgeProjects seeds 3660 attributed seconds on `alpha` ("1 hrs" once
// CompoundDuration drops the trailing minutes) and 3600 on `alpha-old`, both
// inside the badge's default 7-day window. Together they render "2 hrs".
func seedBadgeProjects(hz *testutil.Harness, user string) {
	start := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Minute)
	sd := hz.Seeder(user).Projects("alpha", "alpha-old")
	sd.Block(testutil.HB{
		Project: "alpha", Language: "Go", Editor: "vim",
		Platform: "linux", Category: "coding", Entity: "alpha.go",
	}, start, 4, 900)
	// One extra minute so the total is 1h01m — CompoundDuration drops the
	// SMALLEST unit, so a bare 3600 would render as "" and assert nothing.
	sd.Seed(testutil.HB{
		Project: "alpha", Language: "Go", Editor: "vim",
		Platform: "linux", Category: "coding", Entity: "alpha.go",
		TS: start.Add(90 * time.Minute), Gap: 60,
	})
	sd.Block(testutil.HB{
		Project: "alpha-old", Language: "Go", Editor: "vim",
		Platform: "linux", Category: "coding", Entity: "old.go",
	}, start.Add(3*time.Hour), 4, 900)
}

// mintBadge returns the badge uuid for a project.
func mintBadge(e http.Handler, token, project string) string {
	GinkgoHelper()
	rec := doJSONReqG(e, http.MethodGet, "/badge/link/"+project, token, nil)
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "mint badge: %s", rec.Body.String())
	var env struct {
		BadgeURL string `json:"badgeUrl"`
	}
	Expect(decodeJSONBody(rec.Body.Bytes(), &env)).To(Succeed())
	id := lastSegment(env.BadgeURL)
	Expect(id).NotTo(BeEmpty())
	return id
}

func fetchBadge(e http.Handler, id string) *httptest.ResponseRecorder {
	GinkgoHelper()
	req := httptest.NewRequest(http.MethodGet, "/badge/svg/"+id+"?days=7", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("BadgeSvg rename-target expansion (boom-l827)", func() {
	It("counts every project an exact rename rule merges into the badge subject", func() {
		stub := newBadgeMessageStub()
		DeferCleanup(stub.srv.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "http://ignored.example"
		hz.Cfg.ShieldsIOURL = stub.srv.URL
		e := hz.Router()
		user, token := hz.MintUser("badge_rename_ok")
		seedBadgeProjects(hz, user)

		id := mintBadge(e, token, "alpha")

		// Baseline: no rename rule yet → only alpha's own 1h01m.
		Expect(fetchBadge(e, id)).To(testutil.HaveStatus(http.StatusOK))
		Expect(stub.message()).To(Equal("1 hrs"),
			"pre-rename baseline changed; fixture no longer renders 1 hrs (got %q)", stub.message())

		// Merge alpha-old into alpha. Query-time only: every heartbeat still
		// says "alpha-old", but the projects list, dashboards and widget links
		// now all show one project called "alpha".
		newVal := "alpha"
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "exact",
			"matchValue": "alpha-old", "newValue": newVal,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create rename: %s", rec.Body.String())

		Expect(fetchBadge(e, id)).To(testutil.HaveStatus(http.StatusOK))
		Expect(stub.message()).To(Equal("2 hrs"),
			"badge still reports only the raw subject's time (%q) — the rename-target expansion is missing, so a merged project's badge under-reports while its widget link shows the real total",
			stub.message())
	})

	It("does NOT let a hidden source project leak time into a public badge", func() {
		stub := newBadgeMessageStub()
		DeferCleanup(stub.srv.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "http://ignored.example"
		hz.Cfg.ShieldsIOURL = stub.srv.URL
		e := hz.Router()
		user, token := hz.MintUser("badge_rename_hidden")
		seedBadgeProjects(hz, user)

		newVal := "alpha"
		Expect(doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "exact",
			"matchValue": "alpha-old", "newValue": newVal,
		})).To(testutil.HaveStatus(http.StatusOK))
		// The owner ALSO hides the source project.
		Expect(doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "hide", "matchType": "exact", "matchValue": "alpha-old",
		})).To(testutil.HaveStatus(http.StatusOK))

		id := mintBadge(e, token, "alpha")
		Expect(fetchBadge(e, id)).To(testutil.HaveStatus(http.StatusOK))
		Expect(stub.message()).To(Equal("1 hrs"),
			"a HIDDEN source project contributed to a public badge (%q) — curation must outrank the rename expansion (boom-6jm.3)",
			stub.message())
	})

	It("leaves a badge with no rename rules exactly as it was", func() {
		stub := newBadgeMessageStub()
		DeferCleanup(stub.srv.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "http://ignored.example"
		hz.Cfg.ShieldsIOURL = stub.srv.URL
		e := hz.Router()
		user, token := hz.MintUser("badge_rename_none")
		seedBadgeProjects(hz, user)

		id := mintBadge(e, token, "alpha")
		Expect(fetchBadge(e, id)).To(testutil.HaveStatus(http.StatusOK))
		Expect(stub.message()).To(Equal("1 hrs"))
	})
})
