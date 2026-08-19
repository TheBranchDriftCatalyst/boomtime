package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

// Exercises the SPA-serving contract on the embedded dist (the committed
// placeholder locally; the real books-only build in the image): a bare route
// path falls back to index.html so client-side routing resolves, a missing
// hashed asset 404s (never index.html-as-JS), and the API/catch-all ordering
// leaves explicit routes intact.
func TestRegisterSPA(t *testing.T) {
	e := echo.New()
	// An explicit API-shaped route must win over the "/*" SPA catch-all.
	e.GET("/api/v1/ping", func(c *echo.Context) error {
		return c.String(http.StatusOK, "pong")
	})
	if err := RegisterSPA(e, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("RegisterSPA: %v", err)
	}

	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	t.Run("root serves the shell", func(t *testing.T) {
		rec := do("/")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("GET / body missing SPA root div: %q", rec.Body.String())
		}
	})

	t.Run("client route falls back to index.html", func(t *testing.T) {
		rec := do("/app/books")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /app/books = %d, want 200 (SPA fallback)", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("GET /app/books did not serve the shell")
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Fatalf("shell Cache-Control = %q, want no-cache", cc)
		}
	})

	t.Run("missing hashed asset 404s", func(t *testing.T) {
		rec := do("/assets/Nope-deadbeef.js")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET missing asset = %d, want 404", rec.Code)
		}
	})

	t.Run("explicit API route is not shadowed by the SPA", func(t *testing.T) {
		rec := do("/api/v1/ping")
		if rec.Code != http.StatusOK || rec.Body.String() != "pong" {
			t.Fatalf("GET /api/v1/ping = %d %q, want 200 pong", rec.Code, rec.Body.String())
		}
	})
}
