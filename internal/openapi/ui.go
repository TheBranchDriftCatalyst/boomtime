// Package openapi — Swagger UI hosting.
//
// The UI is standard Swagger UI (swagger-api/swagger-ui) vendored via the
// github.com/swaggo/files/v2 Go module (MIT licensed; see that module's LICENSE
// for the upstream Swagger UI + Swaggo package licenses). Using a Go module
// keeps the assets self-contained inside the compiled binary — no CDN, no
// build-time curl, no external network dependency at runtime.
//
// We serve the vendored dist/* under /api/docs/ and override only
// swagger-initializer.js to point SwaggerUI at our own /api/openapi.json and
// configure the security-scheme id so "Authorize" prompts correctly for our
// wakatime-style `Authorization: Basic <token>` header.
package openapi

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	swaggerFiles "github.com/swaggo/files/v2"
)

// initializerJS is the ONLY swagger UI file we substitute. It swaps the
// default petstore URL for our self-hosted spec + persists the entered token
// (persistAuthorization) so "Try it out" doesn't have to re-Authorize every
// tab reload.
const initializerJS = `// boomtime/openapi: custom swagger-initializer. Points Swagger UI at our
// self-hosted /api/openapi.json and enables persistAuthorization so the token
// entered via Authorize survives page reloads.
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "/api/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    persistAuthorization: true,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: "StandaloneLayout"
  });
};
`

// UIHandler returns an http.Handler that serves the Swagger UI static bundle
// (index.html, CSS, JS, favicons, source maps) at the given prefix.
//
// The one substitution: requests for "swagger-initializer.js" get our
// self-referencing initializer above, not the upstream petstore stub.
//
// prefix is the URL prefix registered on the router (e.g. "/api/docs"). It's
// used to normalize the incoming path when the request hits either "/api/docs"
// or "/api/docs/*".
func UIHandler(prefix string) http.Handler {
	sub, err := fs.Sub(swaggerFiles.FS, ".") // FS is already rooted at dist/
	if err != nil {
		// Shouldn't happen — swaggerFiles.FS is a static embed — but degrade
		// gracefully to a 500-html rather than nil-panic.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "swagger ui unavailable", http.StatusInternalServerError)
		})
	}
	fsSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the prefix (with or without trailing slash) so the file server
		// sees paths rooted at "/".
		p := strings.TrimPrefix(r.URL.Path, prefix)
		if p == "" || p == "/" {
			// Serve the vendored index.html at the docs root.
			f, err := sub.Open("index.html")
			if err != nil {
				http.Error(w, "index.html missing", http.StatusInternalServerError)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.Copy(w, f)
			return
		}
		// Custom initializer — swap in our own so the UI loads our spec.
		if p == "/swagger-initializer.js" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			_, _ = io.WriteString(w, initializerJS)
			return
		}
		// Everything else (CSS, JS bundles, favicons, source maps): serve
		// verbatim from the embedded FS.
		r2 := r.Clone(r.Context())
		r2.URL.Path = p
		fsSrv.ServeHTTP(w, r2)
	})
}
