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

	"github.com/TheBranchDriftCatalyst/gakatime/internal/testutil"
)

// do issues an HTTP request against the harness router and returns the recorder.
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

// statsPayload is the subset of the /stats JSON we assert on.
type statsPayload struct {
	TotalSeconds int64 `json:"totalSeconds"`
	Projects     []struct {
		Name         string `json:"name"`
		TotalSeconds int64  `json:"totalSeconds"`
	} `json:"projects"`
}

func (p statsPayload) projSeconds() map[string]int64 {
	m := map[string]int64{}
	for _, r := range p.Projects {
		m[r.Name] = r.TotalSeconds
	}
	return m
}

// weekAround returns start/end query params bracketing base by +/- one day.
func weekAround(base time.Time) (start, end string) {
	return base.AddDate(0, 0, -1).Format(time.RFC3339), base.AddDate(0, 0, 1).Format(time.RFC3339)
}

// TestStatsRollupFastPath drives GET /stats end-to-end over HTTP: seed heartbeats,
// refresh the rollup, and assert the default (15-min) fast path returns the
// attributed per-project totals.
func TestStatsRollupFastPath(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("stats")

	base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("alpha", "beta")
	aSecs := sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 4, 120)
	bSecs := sd.Block(testutil.HB{Project: "beta", Language: "Go", Editor: "vim"}, base.Add(time.Hour), 3, 120)
	sd.RefreshRollup(base.AddDate(0, 0, -1))

	start, end := weekAround(base)
	rec := do(t, e, http.MethodGet, "/api/v1/users/current/stats?start="+url.QueryEscape(start)+"&end="+url.QueryEscape(end), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var p statsPayload
	decode(t, rec, &p)
	got := p.projSeconds()
	if got["alpha"] != aSecs {
		t.Errorf("alpha = %d, want %d", got["alpha"], aSecs)
	}
	if got["beta"] != bSecs {
		t.Errorf("beta = %d, want %d", got["beta"], bSecs)
	}
	if p.TotalSeconds != aSecs+bSecs {
		t.Errorf("total = %d, want %d", p.TotalSeconds, aSecs+bSecs)
	}
}

// TestStatsMissingAuth confirms the 400 (not 401) on a missing Authorization header.
func TestStatsMissingAuth(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	rec := do(t, e, http.MethodGet, "/api/v1/users/current/stats", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing-auth status = %d, want 400", rec.Code)
	}
}

// TestCurationHideThroughHTTP: POST a hide rule, then GET /stats and assert the
// hidden project vanished and its time is gone (query-time, non-destructive).
func TestCurationHideThroughHTTP(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("hide")

	base := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("keep", "secret")
	keepSecs := sd.Block(testutil.HB{Project: "keep", Language: "Go"}, base, 3, 120)
	sd.Block(testutil.HB{Project: "secret", Language: "Go"}, base.Add(time.Hour), 3, 120)
	sd.RefreshRollup(base.AddDate(0, 0, -1))
	start, end := weekAround(base)
	statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

	// Before hiding: both appear.
	var before statsPayload
	decode(t, do(t, e, http.MethodGet, statsURL, token, nil), &before)
	if _, ok := before.projSeconds()["secret"]; !ok {
		t.Fatal("expected 'secret' before hiding")
	}

	rec := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "hide", "matchType": "exact", "matchValue": "secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create hide status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// After hiding: 'secret' gone, 'keep' unchanged.
	var after statsPayload
	decode(t, do(t, e, http.MethodGet, statsURL, token, nil), &after)
	got := after.projSeconds()
	if _, ok := got["secret"]; ok {
		t.Error("'secret' should be hidden from /stats")
	}
	if got["keep"] != keepSecs {
		t.Errorf("keep = %d, want %d (hide must not change kept totals)", got["keep"], keepSecs)
	}
}

// TestCurationRenameAndAffectedThroughHTTP: create an EXACT rename merging two
// projects, assert /stats shows the merged bucket with conserved total, and that
// GET /curation/:id/affected reports the affected raw values.
func TestCurationRenameAndAffectedThroughHTTP(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("rename")

	base := time.Date(2025, 6, 10, 9, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("web-old", "web-new")
	oldSecs := sd.Block(testutil.HB{Project: "web-old", Language: "Go"}, base, 2, 120)
	newSecs := sd.Block(testutil.HB{Project: "web-new", Language: "Go"}, base.Add(time.Hour), 3, 120)
	sd.RefreshRollup(base.AddDate(0, 0, -1))
	start, end := weekAround(base)
	statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

	newVal := "web"
	rec := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "rename", "matchType": "exact", "matchValue": "web-old", "newValue": newVal,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create rename status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Rule struct {
			ID int `json:"id"`
		} `json:"rule"`
	}
	decode(t, rec, &created)
	if created.Rule.ID == 0 {
		t.Fatal("rename rule id should be non-zero")
	}

	// /stats: web-old is relabeled to "web"; total conserved. web-new stays separate.
	var after statsPayload
	decode(t, do(t, e, http.MethodGet, statsURL, token, nil), &after)
	got := after.projSeconds()
	if _, ok := got["web-old"]; ok {
		t.Error("'web-old' should be relabeled away")
	}
	if got["web"] != oldSecs {
		t.Errorf("merged 'web' = %d, want %d", got["web"], oldSecs)
	}
	if got["web-new"] != newSecs {
		t.Errorf("'web-new' = %d, want %d (unaffected)", got["web-new"], newSecs)
	}
	if after.TotalSeconds != oldSecs+newSecs {
		t.Errorf("total after rename = %d, want %d (conserved)", after.TotalSeconds, oldSecs+newSecs)
	}

	// /affected: the exact rule matches the raw value 'web-old'.
	arec := do(t, e, http.MethodGet, "/api/v1/users/current/curation/"+itoa(created.Rule.ID)+"/affected", token, nil)
	if arec.Code != http.StatusOK {
		t.Fatalf("affected status = %d; body=%s", arec.Code, arec.Body.String())
	}
	if !strings.Contains(arec.Body.String(), "web-old") {
		t.Errorf("affected values should mention 'web-old'; got %s", arec.Body.String())
	}
}

// TestCurationRegexRemapThroughHTTP: create a REGEX rename that collapses a family
// of project names, then assert the merge appears in BOTH /stats and /projects.
func TestCurationRegexRemapThroughHTTP(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("regex")

	base := time.Date(2025, 6, 20, 9, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("svc-auth", "svc-billing", "web")
	a := sd.Block(testutil.HB{Project: "svc-auth", Language: "Go"}, base, 2, 120)
	b := sd.Block(testutil.HB{Project: "svc-billing", Language: "Go"}, base.Add(time.Hour), 2, 120)
	w := sd.Block(testutil.HB{Project: "web", Language: "Go"}, base.Add(2*time.Hour), 2, 120)
	sd.RefreshRollup(base.AddDate(0, 0, -1))
	start, end := weekAround(base)

	svc := "services"
	rec := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "rename", "matchType": "regex", "matchValue": "^svc-", "newValue": svc,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create regex rename status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// /stats: both svc-* collapse into "services"; total conserved.
	statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
	var sp statsPayload
	decode(t, do(t, e, http.MethodGet, statsURL, token, nil), &sp)
	got := sp.projSeconds()
	if got["services"] != a+b {
		t.Errorf("'services' = %d, want %d (svc-auth+svc-billing)", got["services"], a+b)
	}
	if got["web"] != w {
		t.Errorf("'web' = %d, want %d (unaffected)", got["web"], w)
	}
	if _, ok := got["svc-auth"]; ok {
		t.Error("'svc-auth' should be collapsed")
	}

	// /projects: the merged name replaces the raw svc-* names.
	prec := do(t, e, http.MethodGet, "/api/v1/projects?start="+url.QueryEscape(start)+"&end="+url.QueryEscape(end), token, nil)
	if prec.Code != http.StatusOK {
		t.Fatalf("projects status = %d; body=%s", prec.Code, prec.Body.String())
	}
	pbody := prec.Body.String()
	if !strings.Contains(pbody, "services") {
		t.Errorf("/projects should list merged 'services'; got %s", pbody)
	}
	if strings.Contains(pbody, "svc-auth") || strings.Contains(pbody, "svc-billing") {
		t.Errorf("/projects should not list raw svc-* names; got %s", pbody)
	}
}

// TestProjectDetailByDisplayName: a renamed (merged) project must be openable by
// its DISPLAY name via GET /projects/:project (the CheckProjectDisplayOwner path),
// not 404.
func TestProjectDetailByDisplayName(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("detail")

	base := time.Date(2025, 7, 1, 9, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("api-old")
	sd.Block(testutil.HB{Project: "api-old", Language: "Go"}, base, 3, 120)
	sd.RefreshRollup(base.AddDate(0, 0, -1))
	start, end := weekAround(base)

	// Rename api-old -> api.
	newVal := "api"
	if rec := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "rename", "matchType": "exact", "matchValue": "api-old", "newValue": newVal,
	}); rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The display name "api" must resolve (not 404).
	q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
	rec := do(t, e, http.MethodGet, "/api/v1/users/current/projects/api"+q, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("project detail by display name = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The raw name should now 404 (it was relabeled away and no longer owns rows).
	rrec := do(t, e, http.MethodGet, "/api/v1/users/current/projects/api-old"+q, token, nil)
	if rrec.Code == http.StatusOK {
		t.Errorf("raw name 'api-old' should no longer resolve as a display name; got 200")
	}
}

// TestAuthRegisterLoginRefresh drives the full auth cycle over HTTP.
func TestAuthRegisterLoginRefresh(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	username := "authuser_" + time.Now().Format("150405.000000000")
	hz.Cleanup(username)
	password := "s3cret-" + username

	// Register → 200 + token + refresh cookie.
	rec := do(t, e, http.MethodPost, "/auth/register", "", map[string]any{"username": username, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d; body=%s", rec.Code, rec.Body.String())
	}
	refreshCookie := extractRefreshCookie(rec)
	if refreshCookie == "" {
		t.Fatal("register should set a refresh_token cookie")
	}

	// Duplicate register → 409.
	dup := do(t, e, http.MethodPost, "/auth/register", "", map[string]any{"username": username, "password": password})
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate register = %d, want 409", dup.Code)
	}

	// Login with correct creds → 200; wrong password → 403.
	if rec := do(t, e, http.MethodPost, "/auth/login", "", map[string]any{"username": username, "password": password}); rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(t, e, http.MethodPost, "/auth/login", "", map[string]any{"username": username, "password": "wrong"}); rec.Code != http.StatusForbidden {
		t.Errorf("bad login = %d, want 403", rec.Code)
	}

	// Refresh with the cookie → 200.
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh_token", nil)
	req.Header.Set("Cookie", "refresh_token="+refreshCookie)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("refresh status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
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

// ---- template rename (capture/replace groups) over HTTP ----

// affectedResp is the /affected JSON shape (now with mappedTo per value).
type affectedResp struct {
	Values []struct {
		Value    string `json:"value"`
		Count    int64  `json:"count"`
		MappedTo string `json:"mappedTo"`
	} `json:"values"`
	Truncated bool `json:"truncated"`
}

// TestTemplateRenameThroughHTTP: POST a template rule (`^@(.*)$` -> `$1`, using the
// shell-style `$1` to also exercise normalization), then assert /stats strips the
// '@' and merges the buckets, and /affected previews value -> mappedTo.
func TestTemplateRenameThroughHTTP(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("tmpl")

	base := time.Date(2025, 6, 25, 9, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("@swarm-graph", "@drogon", "web")
	sw := sd.Block(testutil.HB{Project: "@swarm-graph", Language: "Go"}, base, 2, 120)
	dr := sd.Block(testutil.HB{Project: "@drogon", Language: "Go"}, base.Add(time.Hour), 2, 120)
	w := sd.Block(testutil.HB{Project: "web", Language: "Go"}, base.Add(2*time.Hour), 2, 120)
	sd.RefreshRollup(base.AddDate(0, 0, -1))
	start, end := weekAround(base)

	// Use `$1` on the wire — the server must normalize it to `\1`.
	rec := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "rename", "matchType": "template", "matchValue": "^@(.*)$", "newValue": "$1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Rule struct {
			ID       int    `json:"id"`
			NewValue string `json:"newValue"`
		} `json:"rule"`
	}
	decode(t, rec, &created)
	if created.Rule.NewValue != `\1` {
		t.Errorf("stored newValue = %q, want normalized %q", created.Rule.NewValue, `\1`)
	}

	// /stats: '@' stripped, totals conserved, 'web' untouched.
	statsURL := "/api/v1/users/current/stats?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)
	var sp statsPayload
	decode(t, do(t, e, http.MethodGet, statsURL, token, nil), &sp)
	got := sp.projSeconds()
	if got["swarm-graph"] != sw {
		t.Errorf("'swarm-graph' = %d, want %d", got["swarm-graph"], sw)
	}
	if got["drogon"] != dr {
		t.Errorf("'drogon' = %d, want %d", got["drogon"], dr)
	}
	if got["web"] != w {
		t.Errorf("'web' = %d, want %d (unaffected)", got["web"], w)
	}
	if _, ok := got["@swarm-graph"]; ok {
		t.Error("'@swarm-graph' should be relabeled away in /stats")
	}

	// /affected: value -> mappedTo preview.
	arec := do(t, e, http.MethodGet, "/api/v1/users/current/curation/"+itoa(created.Rule.ID)+"/affected", token, nil)
	if arec.Code != http.StatusOK {
		t.Fatalf("affected status = %d; body=%s", arec.Code, arec.Body.String())
	}
	var av affectedResp
	decode(t, arec, &av)
	mapped := map[string]string{}
	for _, v := range av.Values {
		mapped[v.Value] = v.MappedTo
	}
	if mapped["@swarm-graph"] != "swarm-graph" || mapped["@drogon"] != "drogon" {
		t.Errorf("affected mappedTo = %+v, want @swarm-graph->swarm-graph, @drogon->drogon", mapped)
	}
	if _, ok := mapped["web"]; ok {
		t.Error("'web' does not match ^@ and must not appear in affected values")
	}
}

// TestBadTemplateThroughHTTP: a template with a backref the pattern can't satisfy
// (`\9` for a single-group pattern) is rejected with 400.
func TestBadTemplateThroughHTTP(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("badtmpl")

	rec := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "rename", "matchType": "template", "matchValue": "^@(.*)$", "newValue": `\9`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad template status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// An uncompilable pattern is also 400.
	rec2 := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "rename", "matchType": "template", "matchValue": "(unterminated", "newValue": `\1`,
	})
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("uncompilable pattern status = %d, want 400", rec2.Code)
	}

	// template on a hide action is rejected (no target).
	rec3 := do(t, e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "project", "action": "hide", "matchType": "template", "matchValue": "^@(.*)$",
	})
	if rec3.Code != http.StatusBadRequest {
		t.Errorf("template+hide status = %d, want 400", rec3.Code)
	}
}
