// og_inject_test.go — server-side OpenGraph <head> injection on /p/:slug
// (gaka social-card). A public profile gets per-user og:*/twitter:* tags with
// an absolute og:image → the og.png endpoint; a non-public / unknown slug
// falls back to the shell's generic default block. Also unit-covers the
// injectOGMeta / publicBaseURL helpers with no DB.
package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/domainreg"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func TestPSlug_InjectsOGMetaForPublicProfile(t *testing.T) {
	hz := testutil.NewHarness(t)
	username, _ := hz.MintUser("pslug")
	slug := fmt.Sprintf("pslug%d", time.Now().UnixNano()%1_000_000_000)
	if err := hz.DB.SetPublicProfile(context.Background(), username, true, slug); err != nil {
		t.Fatalf("SetPublicProfile: %v", err)
	}

	cfg := &config.Config{Port: 8080, SessionExpiry: 24, BadgeURL: "https://boom.example"}
	t.Setenv(rateLimitDisableEnv, "1")
	t.Setenv("BOOM_CORS_ALLOWED_ORIGINS", "https://ok.example.com")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := New(hz.DB, cfg, logger, nil, domainreg.Build().Registry)

	// Public slug → per-user OG tags with an absolute og.png image URL.
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/p/"+slug, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /p/%s: got %d, body=%s", slug, rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	wantImage := fmt.Sprintf(`property="og:image" content="https://boom.example/api/public/profile/%s/og.png"`, slug)
	for _, want := range []string{
		wantImage,
		fmt.Sprintf(`property="og:title" content="@%s · boomtime"`, username),
		fmt.Sprintf(`property="og:url" content="https://boom.example/p/%s"`, slug),
		`property="og:type" content="profile"`,
		`name="twitter:card" content="summary_large_image"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("public /p/:slug missing injected tag:\n  want: %s", want)
		}
	}

	// Unknown slug → generic default block, no per-user og.png image.
	rr2 := httptest.NewRecorder()
	e.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/p/nobodyhere12345", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET /p/unknown: got %d", rr2.Code)
	}
	if strings.Contains(rr2.Body.String(), "/og.png") {
		t.Errorf("unknown slug leaked a per-user og.png image into the shell")
	}
	if !strings.Contains(rr2.Body.String(), `content="boomtime"`) {
		t.Errorf("unknown slug should keep the generic default og:title")
	}
}

func TestInjectOGMeta_ReplacesBlockAndEscapes(t *testing.T) {
	shell := []byte(`<head><!--OG_META
    <meta property="og:title" content="boomtime" />
    <!--/OG_META--></head>`)
	meta := &identity.OGMeta{
		Title:       `@ada · boomtime`,
		Description: `357h coded · TS 42% · <"hi">`,
		ImageURL:    "https://x.example/api/public/profile/ada/og.png",
		ProfileURL:  "https://x.example/p/ada",
	}
	got := string(injectOGMeta(shell, meta))
	if strings.Contains(got, "<!--OG_META") {
		t.Errorf("OG_META block not replaced:\n%s", got)
	}
	if !strings.Contains(got, `property="og:image" content="https://x.example/api/public/profile/ada/og.png"`) {
		t.Errorf("og:image missing:\n%s", got)
	}
	// Hostile description is attribute-escaped.
	if strings.Contains(got, `<"hi">`) {
		t.Errorf("description not escaped:\n%s", got)
	}
	if !strings.Contains(got, "&lt;&quot;hi&quot;&gt;") {
		t.Errorf("expected escaped description entities:\n%s", got)
	}
}

func TestInjectOGMeta_NoMarkerLeavesShellUnchanged(t *testing.T) {
	shell := []byte(`<head><title>x</title></head>`)
	got := injectOGMeta(shell, &identity.OGMeta{Title: "t"})
	if string(got) != string(shell) {
		t.Errorf("shell without marker should be returned unchanged")
	}
}
