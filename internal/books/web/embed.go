// Package web embeds and serves the STANDALONE catalyst-books SPA (gaka-zp2s).
//
// The books-only React build (web/dist-books, produced by `yarn build:books`)
// is embedded here and served by cmd/catalyst-books ALONGSIDE the books API and
// /healthz. This is the books mirror of internal/shared/server's registerStatic
// (SPA fallback for client-side routing) minus the host-only concerns (per-user
// OpenGraph injection, on-disk dashboard override). It imports NOTHING from
// internal/boomtime, so the standalone binary's dependency isolation holds
// (go list -deps ./cmd/catalyst-books | grep internal/boomtime == 0).
//
// The Dockerfile copies web/dist-books into ./dist before `go build`. For a
// local `go build` the dist/ dir carries a committed-ignored placeholder
// index.html (see .gitignore: internal/books/web/dist/) so the embed directive
// always resolves.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/labstack/echo/v5"
)

//go:embed all:dist
var distFS embed.FS

// RegisterSPA mounts the embedded books SPA on the Echo instance: hashed asset
// files are served directly; every other non-API path falls back to index.html
// so react-router's client-side routes (/app, /app/books, …) resolve. Register
// it AFTER the API + /healthz routes — Echo ranks explicit/param routes above
// the "/*" catch-all, so the API is never shadowed.
func RegisterSPA(e *echo.Echo, logger *slog.Logger) error {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		logger.Error("failed to open embedded books dist", "err", err)
		return err
	}
	fileServer := http.FileServer(http.FS(sub))

	e.GET("/*", func(c *echo.Context) error {
		reqPath := strings.TrimPrefix(c.Request().URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		servingShell := false
		if _, statErr := fs.Stat(sub, reqPath); statErr != nil {
			// Missing file. 404 anything that looks like an asset (last path
			// segment has a file extension) so a stale-cached client asking for
			// an old chunk hash doesn't get index.html served as 200 and then
			// try to parse HTML as JS. Real routes (no extension) fall back to
			// index.html so client-side routing keeps working.
			if strings.Contains(path.Base(reqPath), ".") {
				return echo.NewHTTPError(http.StatusNotFound, "not found")
			}
			c.Request().URL.Path = "/"
			servingShell = true
		} else if reqPath == "index.html" {
			servingShell = true
		}
		if servingShell {
			// The shell embeds hashed chunk names via dynamic imports; it must
			// revalidate every load or clients ride stale hashes after a deploy
			// and lazy routes 404. Hashed asset files keep the default policy.
			c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})
	return nil
}
