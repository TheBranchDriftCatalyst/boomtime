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
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"

	swaggerFiles "github.com/swaggo/files/v2"
)

// initializerJS is the ONLY swagger UI file we substitute. It swaps the
// default petstore URL for our self-hosted spec, enables persistAuthorization,
// injects an arasaka-flavored dark theme, and mounts a suite of quality-of-life
// upgrades: auth-status chip, token minting FAB, existing-tokens panel,
// version chip, keyboard shortcuts, deep-link scroll, live cURL, environment
// picker, response history, per-endpoint share links.
//
// The blob lives in initializer.js (adjacent) — 1000+ LOC of raw JS/CSS is
// impossible to review inside a Go raw-string literal, and //go:embed keeps
// it in-binary while making the syntax highlighter + diff view work.
//
// Security posture:
//   - Access tokens live in memory only; never in localStorage.
//   - Minted API tokens shown once, not persisted.
//   - Response-history is opt-in; strips Authorization/Cookie/Set-Cookie
//     headers before persisting and skips bodies matching secret-shaped keys.
//   - Environment picker uses ONLY server-declared spec.servers entries (no
//     free-text URL input that could redirect creds to attacker).
//   - Live cURL masks the Authorization value by default (click to reveal).
//   - X-Frame-Options: SAMEORIGIN set on the docs handler prevents clickjacking.
//
//go:embed initializer.js
var initializerJSBytes []byte

// initializerJS is kept as a string alias so callers (and the hash-computing
// initializer below) don't need to convert on every use. This is a compile-time
// alias — no extra allocation at runtime beyond the embed itself.
var initializerJS = string(initializerJSBytes)

// initializerVersion is a content-hash of the initializer bytes, used to
// cache-bust the <script src="./swagger-initializer.js"> reference in the
// served index.html. Any change to the JS auto-invalidates every browser +
// upstream (Cloudflare, etc.) cache without operator intervention.
var initializerVersion = func() string {
	sum := sha256.Sum256(initializerJSBytes)
	return hex.EncodeToString(sum[:4]) // 8 hex chars is plenty
}()

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
			// Serve the vendored index.html at the docs root, with the
			// initializer <script> tag rewritten to include a content-hash
			// query string. Any change to initializerJS auto-invalidates
			// every browser + upstream (Cloudflare) cache.
			f, err := sub.Open("index.html")
			if err != nil {
				http.Error(w, "index.html missing", http.StatusInternalServerError)
				return
			}
			defer f.Close()
			body, err := io.ReadAll(f)
			if err != nil {
				http.Error(w, "index.html read failed", http.StatusInternalServerError)
				return
			}
			bust := strings.ReplaceAll(
				string(body),
				`./swagger-initializer.js`,
				`./swagger-initializer.js?v=`+initializerVersion,
			)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// The wrapper (index.html) itself must also revalidate so a
			// stale cached copy doesn't keep pointing at the OLD version
			// query string after a deploy.
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
			_, _ = io.WriteString(w, bust)
			return
		}
		// Custom initializer — swap in our own so the UI loads our spec.
		// no-store on this specific asset (not the whole UI): the file is
		// tiny (~50KB) and its contents evolve with the app; browsers
		// heuristically caching it makes every FAB/theme change invisible
		// until an operator manually hard-reloads. Vendored assets (CSS,
		// bundle JS) are stable per-release and stay cacheable.
		if p == "/swagger-initializer.js" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
			_, _ = w.Write(initializerJSBytes)
			return
		}
		// Everything else (CSS, JS bundles, favicons, source maps): serve
		// verbatim from the embedded FS.
		r2 := r.Clone(r.Context())
		r2.URL.Path = p
		fsSrv.ServeHTTP(w, r2)
	})
}
