package testutil_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

type widgetLinkResp struct {
	WidgetBaseURL string `json:"widgetBaseUrl"`
	LinkID        string `json:"linkId"`
}

// mintWidgetLink is the shared happy-path mint.
func mintWidgetLink(t *testing.T, e http.Handler, token, scopeType, scopeRef string) widgetLinkResp {
	t.Helper()
	rec := do(t, e, "GET",
		fmt.Sprintf("/api/v1/users/current/widgets/link?scopeType=%s&scopeRef=%s", scopeType, scopeRef),
		token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint widget link: status %d body=%s", rec.Code, rec.Body.String())
	}
	var out widgetLinkResp
	decode(t, rec, &out)
	return out
}

func TestWidgetLinkMintIsIdempotent(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("widget_mint")

	a := mintWidgetLink(t, e, token, "user", "")
	b := mintWidgetLink(t, e, token, "user", "")
	if a.LinkID != b.LinkID {
		t.Errorf("re-mint changed the uuid: %s vs %s (embeds must stay stable)", a.LinkID, b.LinkID)
	}
	if !strings.Contains(a.WidgetBaseURL, "/widget/svg/"+a.LinkID) {
		t.Errorf("widgetBaseUrl %q does not embed the link id", a.WidgetBaseURL)
	}
}

func TestWidgetLinkScopeOwnership(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	userA, tokenA := hz.MintUser("widget_owner_a")
	_, tokenB := hz.MintUser("widget_owner_b")

	// A owns a project; B must not be able to mint a link for it.
	hz.Seeder(userA).Projects("secret-proj")
	if rec := do(t, e, "GET", "/api/v1/users/current/widgets/link?scopeType=project&scopeRef=secret-proj", tokenB, nil); rec.Code != http.StatusNotFound {
		t.Errorf("cross-owner project mint: status %d, want 404", rec.Code)
	}
	// A can.
	mintWidgetLink(t, e, tokenA, "project", "secret-proj")

	// Unknown scope type is a 400.
	if rec := do(t, e, "GET", "/api/v1/users/current/widgets/link?scopeType=galaxy&scopeRef=x", tokenA, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad scopeType: status %d, want 400", rec.Code)
	}
	// Space id belonging to nobody 404s.
	if rec := do(t, e, "GET", "/api/v1/users/current/widgets/link?scopeType=space&scopeRef=999999", tokenA, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown space mint: status %d, want 404", rec.Code)
	}
}

func TestWidgetSvgPublicRender(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("widget_render")

	// Seed attributed time so the card has content.
	start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	hz.Seeder(user).Block(testutil.HB{Project: "proj-x", Language: "Go", Editor: "vim"}, start, 10, 60)
	hz.Seeder(user).RefreshRollup(start.Add(-time.Hour))

	link := mintWidgetLink(t, e, token, "user", "")

	for _, kind := range []string{"stats-card", "stats-card-with-grade", "top-langs", "top-projects", "badge"} {
		t.Run(kind, func(t *testing.T) {
			// NO token: the endpoint is public.
			rec := do(t, e, "GET", "/widget/svg/"+link.LinkID+"/"+kind+"?days=30&theme=dark", "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
				t.Errorf("Content-Type = %q, want image/svg+xml", ct)
			}
			if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=300") {
				t.Errorf("Cache-Control = %q, want public max-age=300", cc)
			}
			if !strings.Contains(rec.Body.String(), "<svg") {
				t.Error("body is not SVG")
			}
		})
	}

	// The seeded language must show up on the langs card.
	rec := do(t, e, "GET", "/widget/svg/"+link.LinkID+"/top-langs", "", nil)
	if !strings.Contains(rec.Body.String(), ">Go<") {
		t.Errorf("top-langs should include the seeded language Go:\n%s", rec.Body.String())
	}
}

func TestWidgetSvgErrors(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("widget_err")
	link := mintWidgetLink(t, e, token, "user", "")

	if rec := do(t, e, "GET", "/widget/svg/not-a-uuid/stats-card", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad uuid: status %d, want 400", rec.Code)
	}
	if rec := do(t, e, "GET", "/widget/svg/00000000-0000-0000-0000-000000000000/stats-card", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown uuid: status %d, want 404", rec.Code)
	}
	if rec := do(t, e, "GET", "/widget/svg/"+link.LinkID+"/not-a-kind", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown kind: status %d, want 404", rec.Code)
	}
}

// The privacy gate: a language hidden via curation must NOT appear in the
// public SVG. Forgetting LoadHiddenSets on this endpoint would leak
// curated-away data to anyone with the link.
func TestWidgetSvgHiddenLeak(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	user, token := hz.MintUser("widget_hidden")

	start := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	sdr := hz.Seeder(user)
	sdr.Block(testutil.HB{Project: "proj-pub", Language: "Go", Editor: "vim"}, start, 10, 60)
	sdr.Block(testutil.HB{Project: "proj-sec", Language: "SecretLang", Editor: "vim"}, start.Add(time.Hour), 10, 60)
	sdr.RefreshRollup(start.Add(-time.Hour))

	// Hide SecretLang via the curation API (exact hide rule on language).
	rec := do(t, e, "POST", "/api/v1/users/current/curation", token, map[string]any{
		"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "SecretLang",
	})
	if rec.Code >= 300 {
		t.Fatalf("create hide rule: status %d body=%s", rec.Code, rec.Body.String())
	}

	link := mintWidgetLink(t, e, token, "user", "")
	svg := do(t, e, "GET", "/widget/svg/"+link.LinkID+"/top-langs?days=30", "", nil)
	if svg.Code != http.StatusOK {
		t.Fatalf("render: status %d", svg.Code)
	}
	if strings.Contains(svg.Body.String(), "SecretLang") {
		t.Fatal("PRIVACY LEAK: curation-hidden language appears in the public widget SVG")
	}
	if !strings.Contains(svg.Body.String(), ">Go<") {
		t.Error("non-hidden language should still render")
	}
}

func TestWidgetLinkList(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("widget_list")

	link := mintWidgetLink(t, e, token, "user", "")

	var list struct {
		Links []struct {
			LinkID    string `json:"linkId"`
			ScopeType string `json:"scopeType"`
			ScopeName string `json:"scopeName"`
		} `json:"links"`
	}
	rec := do(t, e, "GET", "/api/v1/users/current/widgets/links", token, nil)
	decode(t, rec, &list)
	if len(list.Links) != 1 || list.Links[0].LinkID != link.LinkID {
		t.Fatalf("list = %+v, want the one minted link", list)
	}
	// user-scope carries no scopeName (empty is fine).
	if list.Links[0].ScopeName != "" {
		t.Errorf("user-scope scopeName = %q, want empty", list.Links[0].ScopeName)
	}
}

// gaka-hsj follow-up: every public SVG fetch bumps last_used_at and merges
// the Referer (or "direct") into a bounded origins set. Settings' badge
// reads this to show "last requested Nm ago" + a click-through popover.
func TestWidgetLinkTracksHitsAndOrigins(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("widget_hits")

	link := mintWidgetLink(t, e, token, "user", "")

	// Hit with two distinct Referers and one Referer twice.
	fire := func(ref string) {
		req := httptest.NewRequest("GET", "/widget/svg/"+link.LinkID+"/stats-card", nil)
		if ref != "" {
			req.Header.Set("Referer", ref)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("SVG hit: %d", rec.Code)
		}
	}
	fire("https://github.com/DJ/repo")
	fire("https://github.com/DJ/repo")
	fire("https://blog.example.com/post")
	fire("") // "direct"

	var list struct {
		Links []struct {
			LinkID     string     `json:"linkId"`
			LastUsedAt *time.Time `json:"lastUsedAt"`
			Origins    []struct {
				Origin string `json:"origin"`
				Count  int    `json:"count"`
			} `json:"origins"`
		} `json:"links"`
	}
	rec := do(t, e, "GET", "/api/v1/users/current/widgets/links", token, nil)
	decode(t, rec, &list)
	if len(list.Links) != 1 {
		t.Fatalf("list len = %d", len(list.Links))
	}
	got := list.Links[0]
	if got.LastUsedAt == nil {
		t.Fatal("last_used_at should be set after a fetch")
	}
	origins := map[string]int{}
	for _, o := range got.Origins {
		origins[o.Origin] = o.Count
	}
	if origins["https://github.com/DJ/repo"] != 2 {
		t.Errorf("github origin count = %d, want 2", origins["https://github.com/DJ/repo"])
	}
	if origins["https://blog.example.com/post"] != 1 {
		t.Errorf("blog origin count = %d, want 1", origins["https://blog.example.com/post"])
	}
	if origins["direct"] != 1 {
		t.Errorf("direct origin count = %d, want 1", origins["direct"])
	}
}

// gaka-hsj follow-up: rolling a link mints a new uuid for the same scope.
// The old id immediately 404s; the new id serves. Cross-owner rolls 404.
func TestWidgetLinkRoll(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, tokenA := hz.MintUser("widget_roll_a")
	_, tokenB := hz.MintUser("widget_roll_b")

	orig := mintWidgetLink(t, e, tokenA, "user", "")

	// B cannot roll A's link.
	if rec := do(t, e, "POST", "/api/v1/users/current/widgets/link/"+orig.LinkID+"/roll", tokenB, nil); rec.Code != http.StatusNotFound {
		t.Errorf("cross-owner roll: status %d, want 404", rec.Code)
	}

	// A rolls it.
	rec := do(t, e, "POST", "/api/v1/users/current/widgets/link/"+orig.LinkID+"/roll", tokenA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("roll: status %d body=%s", rec.Code, rec.Body.String())
	}
	var rolled widgetLinkResp
	decode(t, rec, &rolled)
	if rolled.LinkID == orig.LinkID {
		t.Fatal("roll should mint a new uuid")
	}

	// Old id → 404 on the public endpoint (the point of rolling: kill any
	// leaked/embedded URL immediately).
	if rec := do(t, e, "GET", "/widget/svg/"+orig.LinkID+"/stats-card", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("old link post-roll: status %d, want 404", rec.Code)
	}
	// New id → 200.
	if rec := do(t, e, "GET", "/widget/svg/"+rolled.LinkID+"/stats-card", "", nil); rec.Code != http.StatusOK {
		t.Errorf("new link post-roll: status %d, want 200", rec.Code)
	}

	// list still shows exactly one link (same scope), the new id — rolling
	// preserves the scope row, only the uuid changes.
	var list struct {
		Links []struct{ LinkID, ScopeType, ScopeRef string } `json:"links"`
	}
	rec = do(t, e, "GET", "/api/v1/users/current/widgets/links", tokenA, nil)
	decode(t, rec, &list)
	if len(list.Links) != 1 {
		t.Fatalf("list len = %d after roll, want 1", len(list.Links))
	}
	if list.Links[0].LinkID != rolled.LinkID {
		t.Errorf("list shows %q, want the rolled id %q", list.Links[0].LinkID, rolled.LinkID)
	}
}

func TestWidgetSvgDaysClamped(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("widget_clamp")
	link := mintWidgetLink(t, e, token, "user", "")

	// Absurd values must not error — they clamp.
	for _, days := range []string{"0", "-5", "99999", "abc"} {
		rec := do(t, e, "GET", "/widget/svg/"+link.LinkID+"/stats-card?days="+days, "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("days=%s: status %d, want 200 (clamped)", days, rec.Code)
		}
	}
}
