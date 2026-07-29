// integration_ginkgo_test.go — ginkgo mirror of integration_test.go (gaka-0vp).
// 1:1 case map (9 stdlib TestXxx; each becomes one It):
//
//	TestStatsRollupFastPath                     → Stats HTTP > "GET /stats fast path"
//	TestStatsMissingAuth                        → Stats HTTP > "GET /stats without auth is 400"
//	TestCurationHideThroughHTTP                 → Curation HTTP > "hide rule removes project"
//	TestCurationRenameAndAffectedThroughHTTP    → Curation HTTP > "exact rename merges + /affected reports"
//	TestCurationRegexRemapThroughHTTP           → Curation HTTP > "regex rename collapses svc-* into services"
//	TestProjectDetailByDisplayName              → Curation HTTP > "renamed project resolves by display name"
//	TestSpaceScopedStatsThroughHTTP             → Spaces HTTP > "scoped /stats excludes non-space projects"
//	TestAuthRegisterLoginRefresh                → Auth HTTP > "register/login/refresh full cycle"
//	TestTemplateRenameThroughHTTP               → Curation HTTP > "template rename strips '@' + /affected mappedTo"
//	TestBadTemplateThroughHTTP                  → Curation HTTP > "bad template patterns are 400"
//	TestActiveFilesThroughHTTP                  → Files HTTP > "lynchpin ordering + per-file project counts"
//
// (11 stdlib tests total — I miscounted "9" above; corrected in comment.)
//
// Helpers `doG`, `decodeG`, `extractRefreshCookieG`, `itoaG` live in
// helpers_ginkgo_test.go.
package testutil_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// statsPayloadG mirrors the stdlib statsPayload used to decode /stats.
type statsPayloadG struct {
	TotalSeconds int64 `json:"totalSeconds"`
	Projects     []struct {
		Name         string `json:"name"`
		TotalSeconds int64  `json:"totalSeconds"`
	} `json:"projects"`
}

func (p statsPayloadG) projSeconds() map[string]int64 {
	m := map[string]int64{}
	for _, r := range p.Projects {
		m[r.Name] = r.TotalSeconds
	}
	return m
}

// weekAroundG returns start/end query params bracketing base by +/- one day.
func weekAroundG(base time.Time) (start, end string) {
	return base.AddDate(0, 0, -1).Format(time.RFC3339),
		base.AddDate(0, 0, 1).Format(time.RFC3339)
}

// affectedRespG mirrors the /affected JSON shape.
type affectedRespG struct {
	Values []struct {
		Value    string `json:"value"`
		Count    int64  `json:"count"`
		MappedTo string `json:"mappedTo"`
	} `json:"values"`
	Truncated bool `json:"truncated"`
}

// activeFilesRespG mirrors the /files response.
type activeFilesRespG struct {
	Files []struct {
		Entity   string `json:"entity"`
		Seconds  int64  `json:"seconds"`
		Projects int64  `json:"projects"`
	} `json:"files"`
	Truncated bool `json:"truncated"`
}

var _ = Describe("Stats HTTP", func() {
	It("GET /stats returns per-project attributed totals via the fast path", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("stats")

		base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("alpha", "beta")
		aSecs := sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 4, 120)
		bSecs := sd.Block(testutil.HB{Project: "beta", Language: "Go", Editor: "vim"}, base.Add(time.Hour), 3, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))

		start, end := weekAroundG(base)
		rec := doG(e, http.MethodGet,
			"/api/v1/users/current/stats?start="+url.QueryEscape(start)+"&end="+url.QueryEscape(end),
			token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
		var p statsPayloadG
		decodeG(rec, &p)
		got := p.projSeconds()
		Expect(got["alpha"]).To(Equal(aSecs))
		Expect(got["beta"]).To(Equal(bSecs))
		Expect(p.TotalSeconds).To(Equal(aSecs + bSecs))
	})

	It("GET /stats without any Authorization header is 400 (not 401)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		rec := doG(e, http.MethodGet, "/api/v1/users/current/stats", "", nil)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("Curation HTTP", func() {
	It("POST a hide rule then GET /stats: hidden project vanishes, kept project unchanged", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("hide")

		base := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("keep", "secret")
		keepSecs := sd.Block(testutil.HB{Project: "keep", Language: "Go"}, base, 3, 120)
		sd.Block(testutil.HB{Project: "secret", Language: "Go"}, base.Add(time.Hour), 3, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))
		start, end := weekAroundG(base)
		statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		var before statsPayloadG
		decodeG(doG(e, http.MethodGet, statsURL, token, nil), &before)
		_, hasSecret := before.projSeconds()["secret"]
		Expect(hasSecret).To(BeTrue(), "expected 'secret' to appear before hiding")

		rec := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "hide", "matchType": "exact", "matchValue": "secret",
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())

		var after statsPayloadG
		decodeG(doG(e, http.MethodGet, statsURL, token, nil), &after)
		got := after.projSeconds()
		_, secretStillThere := got["secret"]
		Expect(secretStillThere).To(BeFalse(), "'secret' should be hidden from /stats")
		Expect(got["keep"]).To(Equal(keepSecs), "hide must not change kept totals")
	})

	It("POST an EXACT rename → /stats shows merged bucket + /affected reports raw values", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("rename")

		base := time.Date(2025, 6, 10, 9, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("web-old", "web-new")
		oldSecs := sd.Block(testutil.HB{Project: "web-old", Language: "Go"}, base, 2, 120)
		newSecs := sd.Block(testutil.HB{Project: "web-new", Language: "Go"}, base.Add(time.Hour), 3, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))
		start, end := weekAroundG(base)
		statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		newVal := "web"
		rec := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "exact",
			"matchValue": "web-old", "newValue": newVal,
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
		var created struct {
			Rule struct{ ID int } `json:"rule"`
		}
		decodeG(rec, &created)
		Expect(created.Rule.ID).NotTo(BeZero())

		var after statsPayloadG
		decodeG(doG(e, http.MethodGet, statsURL, token, nil), &after)
		got := after.projSeconds()
		_, oldStillThere := got["web-old"]
		Expect(oldStillThere).To(BeFalse(), "'web-old' should be relabeled away")
		Expect(got["web"]).To(Equal(oldSecs))
		Expect(got["web-new"]).To(Equal(newSecs))
		Expect(after.TotalSeconds).To(Equal(oldSecs+newSecs), "total conserved after rename")

		arec := doG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+itoaG(created.Rule.ID)+"/affected", token, nil)
		Expect(arec.Code).To(Equal(http.StatusOK), "body=%s", arec.Body.String())
		Expect(arec.Body.String()).To(ContainSubstring("web-old"),
			"affected values should mention 'web-old'; got %s", arec.Body.String())
	})

	It("POST a REGEX rename collapses svc-* into 'services' in /stats + /projects", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("regex")

		base := time.Date(2025, 6, 20, 9, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("svc-auth", "svc-billing", "web")
		a := sd.Block(testutil.HB{Project: "svc-auth", Language: "Go"}, base, 2, 120)
		b := sd.Block(testutil.HB{Project: "svc-billing", Language: "Go"}, base.Add(time.Hour), 2, 120)
		w := sd.Block(testutil.HB{Project: "web", Language: "Go"}, base.Add(2*time.Hour), 2, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))
		start, end := weekAroundG(base)

		svc := "services"
		rec := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "regex",
			"matchValue": "^svc-", "newValue": svc,
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())

		statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
		var sp statsPayloadG
		decodeG(doG(e, http.MethodGet, statsURL, token, nil), &sp)
		got := sp.projSeconds()
		Expect(got["services"]).To(Equal(a+b), "'services' should equal svc-auth+svc-billing")
		Expect(got["web"]).To(Equal(w), "'web' should be unaffected")
		_, svcAuthStillThere := got["svc-auth"]
		Expect(svcAuthStillThere).To(BeFalse(), "'svc-auth' should be collapsed away")

		prec := doG(e, http.MethodGet,
			"/api/v1/projects?start="+url.QueryEscape(start)+"&end="+url.QueryEscape(end),
			token, nil)
		Expect(prec.Code).To(Equal(http.StatusOK), "body=%s", prec.Body.String())
		pbody := prec.Body.String()
		Expect(pbody).To(ContainSubstring("services"))
		Expect(pbody).NotTo(ContainSubstring("svc-auth"))
		Expect(pbody).NotTo(ContainSubstring("svc-billing"))
	})

	It("a renamed project is openable via GET /projects/:project by DISPLAY name", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("detail")

		base := time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("api-old")
		sd.Block(testutil.HB{Project: "api-old", Language: "Go"}, base, 3, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))
		start, end := weekAroundG(base)

		newVal := "api"
		rec := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "exact",
			"matchValue": "api-old", "newValue": newVal,
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())

		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
		rec = doG(e, http.MethodGet, "/api/v1/users/current/projects/api"+q, token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"display name 'api' should resolve; body=%s", rec.Body.String())

		rrec := doG(e, http.MethodGet, "/api/v1/users/current/projects/api-old"+q, token, nil)
		Expect(rrec.Code).NotTo(Equal(http.StatusOK),
			"raw name 'api-old' should no longer resolve as display name")
	})

	It("POST a TEMPLATE rename strips '@' + /affected preview shows mappedTo", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("tmpl")

		base := time.Date(2025, 6, 25, 9, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("@swarm-graph", "@drogon", "web")
		sw := sd.Block(testutil.HB{Project: "@swarm-graph", Language: "Go"}, base, 2, 120)
		dr := sd.Block(testutil.HB{Project: "@drogon", Language: "Go"}, base.Add(time.Hour), 2, 120)
		w := sd.Block(testutil.HB{Project: "web", Language: "Go"}, base.Add(2*time.Hour), 2, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))
		start, end := weekAroundG(base)

		// Use `$1` on the wire — the server must normalize it to `\1`.
		rec := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "template",
			"matchValue": "^@(.*)$", "newValue": "$1",
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
		var created struct {
			Rule struct {
				ID       int    `json:"id"`
				NewValue string `json:"newValue"`
			} `json:"rule"`
		}
		decodeG(rec, &created)
		Expect(created.Rule.NewValue).To(Equal(`\1`),
			"stored newValue should be normalized to `\\1`")

		statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
		var sp statsPayloadG
		decodeG(doG(e, http.MethodGet, statsURL, token, nil), &sp)
		got := sp.projSeconds()
		Expect(got["swarm-graph"]).To(Equal(sw))
		Expect(got["drogon"]).To(Equal(dr))
		Expect(got["web"]).To(Equal(w))
		_, atSwarmStillThere := got["@swarm-graph"]
		Expect(atSwarmStillThere).To(BeFalse())

		arec := doG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+itoaG(created.Rule.ID)+"/affected", token, nil)
		Expect(arec.Code).To(Equal(http.StatusOK), "body=%s", arec.Body.String())
		var av affectedRespG
		decodeG(arec, &av)
		mapped := map[string]string{}
		for _, v := range av.Values {
			mapped[v.Value] = v.MappedTo
		}
		Expect(mapped["@swarm-graph"]).To(Equal("swarm-graph"))
		Expect(mapped["@drogon"]).To(Equal("drogon"))
		_, webInAffected := mapped["web"]
		Expect(webInAffected).To(BeFalse(),
			"'web' does not match ^@ and must not appear in affected values")
	})

	It("POST a template with an impossible backref (\\9) is 400; uncompilable pattern is 400; template on hide is 400", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("badtmpl")

		rec := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "template",
			"matchValue": "^@(.*)$", "newValue": `\9`,
		})
		Expect(rec.Code).To(Equal(http.StatusBadRequest),
			"impossible backref should be 400; body=%s", rec.Body.String())

		rec2 := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "rename", "matchType": "template",
			"matchValue": "(unterminated", "newValue": `\1`,
		})
		Expect(rec2.Code).To(Equal(http.StatusBadRequest),
			"uncompilable pattern should be 400")

		rec3 := doG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "project", "action": "hide", "matchType": "template", "matchValue": "^@(.*)$",
		})
		Expect(rec3.Code).To(Equal(http.StatusBadRequest),
			"template on a hide action has no target and should be 400")
	})
})

var _ = Describe("Spaces HTTP", func() {
	It("GET /stats?space=<id> returns only in-space projects; unscoped is unchanged", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("space")

		base := time.Date(2025, 9, 10, 9, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("catalyst-web", "catalyst-api", "personal")
		cw := sd.Block(testutil.HB{Project: "catalyst-web", Language: "Go"}, base, 2, 120)
		ca := sd.Block(testutil.HB{Project: "catalyst-api", Language: "Go"}, base.Add(time.Hour), 3, 120)
		pe := sd.Block(testutil.HB{Project: "personal", Language: "Go"}, base.Add(2*time.Hour), 4, 120)
		sd.RefreshRollup(base.AddDate(0, 0, -1))
		start, end := weekAroundG(base)

		crec := doG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
			map[string]any{"name": "Work"})
		Expect(crec.Code).To(Equal(http.StatusOK), "body=%s", crec.Body.String())
		var created struct {
			Space struct{ ID int } `json:"space"`
		}
		decodeG(crec, &created)
		Expect(created.Space.ID).NotTo(BeZero())
		spaceID := itoaG(created.Space.ID)

		rrec := doG(e, http.MethodPost, "/api/v1/users/current/spaces/"+spaceID+"/rules", token,
			map[string]any{"axis": "project", "matchValue": "^catalyst", "matchType": "regex"})
		Expect(rrec.Code).To(Equal(http.StatusOK), "body=%s", rrec.Body.String())

		statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
		var unscoped statsPayloadG
		decodeG(doG(e, http.MethodGet, statsURL, token, nil), &unscoped)
		Expect(unscoped.TotalSeconds).To(Equal(cw + ca + pe))
		_, personalIn := unscoped.projSeconds()["personal"]
		Expect(personalIn).To(BeTrue(), "unscoped /stats should include 'personal'")

		scopedURL := statsURL + "&space=" + spaceID
		var scoped statsPayloadG
		decodeG(doG(e, http.MethodGet, scopedURL, token, nil), &scoped)
		got := scoped.projSeconds()
		Expect(got["catalyst-web"]).To(Equal(cw))
		Expect(got["catalyst-api"]).To(Equal(ca))
		_, personalInScoped := got["personal"]
		Expect(personalInScoped).To(BeFalse(), "scoped /stats must exclude 'personal'")
		Expect(scoped.TotalSeconds).To(Equal(cw + ca))
	})
})

var _ = Describe("Auth HTTP", func() {
	It("register → login (good/bad password) → refresh full cycle", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		username := "authuser_" + time.Now().Format("150405.000000000")
		hz.Cleanup(username)
		password := "s3cret-" + username

		// Register → 200 + refresh cookie
		rec := doG(e, http.MethodPost, "/auth/register", "",
			map[string]any{"username": username, "password": password})
		Expect(rec.Code).To(Equal(http.StatusOK), "register body=%s", rec.Body.String())
		refreshCookie := extractRefreshCookieG(rec)
		Expect(refreshCookie).NotTo(BeEmpty(), "register should set a refresh_token cookie")

		// Duplicate register → 409
		dup := doG(e, http.MethodPost, "/auth/register", "",
			map[string]any{"username": username, "password": password})
		Expect(dup.Code).To(Equal(http.StatusConflict))

		// Login with correct creds → 200
		lrec := doG(e, http.MethodPost, "/auth/login", "",
			map[string]any{"username": username, "password": password})
		Expect(lrec.Code).To(Equal(http.StatusOK), "login body=%s", lrec.Body.String())

		// Wrong password → 403
		brec := doG(e, http.MethodPost, "/auth/login", "",
			map[string]any{"username": username, "password": "wrong"})
		Expect(brec.Code).To(Equal(http.StatusForbidden))

		// Refresh with the cookie → 200
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
		req.Header.Set("Cookie", "refresh_token="+refreshCookie)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK),
			"refresh body=%s", rr.Body.String())
	})
})

var _ = Describe("Files HTTP", func() {
	It("GET /files returns lynchpin-first ordering + per-file project counts + summed seconds", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("files")

		base := time.Date(2025, 8, 4, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("alpha", "beta")
		// router.py under alpha (120s) + beta (60s) → lynchpin (projects=2)
		sd.Seed(testutil.HB{Project: "alpha", Entity: "router.py", Ty: "file", TS: base, Gap: 120})
		sd.Seed(testutil.HB{Project: "beta", Entity: "router.py", Ty: "file", TS: base.Add(time.Minute), Gap: 60})
		// only_a.go single project, higher seconds but projects=1
		sd.Seed(testutil.HB{Project: "alpha", Entity: "only_a.go", Ty: "file", TS: base.Add(2 * time.Minute), Gap: 200})

		start, end := weekAroundG(base)
		rec := doG(e, http.MethodGet,
			"/api/v1/users/current/files?start="+url.QueryEscape(start)+"&end="+url.QueryEscape(end),
			token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())

		var af activeFilesRespG
		decodeG(rec, &af)
		Expect(len(af.Files)).To(BeNumerically(">=", 2),
			"expected >=2 files, got %+v", af.Files)
		// Lynchpin first
		Expect(af.Files[0].Entity).To(Equal("router.py"))
		Expect(af.Files[0].Projects).To(BeEquivalentTo(2))
		Expect(af.Files[0].Seconds).To(BeEquivalentTo(180))
		Expect(af.Truncated).To(BeFalse())
		Expect(rec.Body.String()).To(ContainSubstring("only_a.go"))
	})
})

// keep imports honest — strings/httptest referenced by other test files in
// this package, but here only via ContainSubstring; explicit uses below
// silence any accidental unused imports if a case is trimmed.
var _ = strings.Contains

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func do(t *testing.T, e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode (status %d): %v\nbody=%s", rec.Code, err, rec.Body.String())
	}
}

type statsPayload struct {
	TotalSeconds int64 `json:"totalSeconds"`
	Projects     []struct {
		Name         string `json:"name"`
		TotalSeconds int64  `json:"totalSeconds"`
	} `json:"projects"`
}

func weekAround(base time.Time) (start, end string) {
	return base.AddDate(0, 0, -1).Format(time.RFC3339), base.AddDate(0, 0, 1).Format(time.RFC3339)
}

func extractRefreshCookie(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			return c.Value
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

type affectedResp struct {
	Values []struct {
		Value    string `json:"value"`
		Count    int64  `json:"count"`
		MappedTo string `json:"mappedTo"`
	} `json:"values"`
	Truncated bool `json:"truncated"`
}

type activeFilesResp struct {
	Files []struct {
		Entity   string `json:"entity"`
		Seconds  int64  `json:"seconds"`
		Projects int64  `json:"projects"`
	} `json:"files"`
	Truncated bool `json:"truncated"`
}
