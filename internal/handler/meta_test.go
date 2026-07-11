package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	boomtime "github.com/TheBranchDriftCatalyst/boomtime"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/labstack/echo/v5"
)

// metaHandler builds a Handler wired with just what the meta endpoints touch
// (Cfg + Logger + Cache; no DB, no worker, no hub). Meta endpoints must never
// depend on the database or the importer — kept true by only wiring these.
func metaHandler(t *testing.T, ver string) *Handler {
	t.Helper()
	return &Handler{
		Cfg:    &config.Config{Version: ver},
		Logger: slog.Default(),
		Cache:  cache.New(0),
	}
}

func TestVersionEndpoint(t *testing.T) {
	t.Run("returns configured version", func(t *testing.T) {
		h := metaHandler(t, "v1.2.3")
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		if err := h.Version(c); err != nil {
			t.Fatalf("Version() error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got versionResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Version != "v1.2.3" {
			t.Errorf("version = %q, want %q", got.Version, "v1.2.3")
		}
	})

	t.Run("empty cfg version falls back to dev", func(t *testing.T) {
		h := metaHandler(t, "")
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		if err := h.Version(c); err != nil {
			t.Fatalf("Version() error: %v", err)
		}
		var got versionResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Version != "dev" {
			t.Errorf("empty cfg.Version should surface as %q, got %q", "dev", got.Version)
		}
	})
}

func TestChangelogEndpoint(t *testing.T) {
	// Sanity-check the embedded bytes first — a missing embed is a compile
	// failure, but an accidentally-empty file would slip past that.
	if len(boomtime.ChangelogMD) == 0 {
		t.Fatal("boomtime.ChangelogMD is empty; regenerate with `task changelog`")
	}
	if !strings.HasPrefix(string(boomtime.ChangelogMD), "# Changelog") {
		t.Fatalf("embedded CHANGELOG.md must start with '# Changelog'; got %q", firstLine(boomtime.ChangelogMD))
	}

	h := metaHandler(t, "v1.0.0")
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/changelog", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.Changelog(c); err != nil {
		t.Fatalf("Changelog() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get(echo.HeaderContentType)
	if !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown*", ct)
	}
	if rec.Body.Len() != len(boomtime.ChangelogMD) {
		t.Errorf("body length = %d, want %d (verbatim)", rec.Body.Len(), len(boomtime.ChangelogMD))
	}
}

func firstLine(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
