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
// default petstore URL for our self-hosted spec, enables persistAuthorization,
// injects an arasaka-flavored dark theme, and mounts a floating action button
// (bottom-right) that lets a logged-in operator mint an API token in-page and
// hop back to the boomtime app.
const initializerJS = `// boomtime/openapi: custom swagger-initializer.
window.onload = function () {
  // --- 1. dark theme --- injected as an inline <style> so we don't ship
  //     a second static asset. Matches arasaka: crimson + jet black + one
  //     amber accent. Tuned against Swagger UI 5.x class names.
  const css = ` + "`" + `
    :root, body { background: #0a0a0f !important; color: #d8d8dc; }
    .swagger-ui, .swagger-ui * { color: #d8d8dc; }
    .swagger-ui .topbar { background: #0f0f14; border-bottom: 1px solid #e21e51; }
    .swagger-ui .info .title, .swagger-ui .info .title small { color: #e21e51; }
    .swagger-ui .info a, .swagger-ui a { color: #f5a623; }
    .swagger-ui .scheme-container { background: #0f0f14; box-shadow: inset 0 -1px 0 #e21e51; }
    .swagger-ui .opblock-tag { color: #e21e51; border-bottom: 1px solid #2a1a20; }
    .swagger-ui .opblock { background: #12121a; border-color: #2a1a20; box-shadow: none; margin: 0 0 8px; }
    .swagger-ui .opblock .opblock-summary { border-color: #2a1a20; }
    .swagger-ui .opblock .opblock-summary-path,
    .swagger-ui .opblock .opblock-summary-operation-id,
    .swagger-ui .opblock .opblock-summary-description { color: #d8d8dc; }
    .swagger-ui .opblock.opblock-get   { background: #0e1a20; border-color: #1a4a5f; }
    .swagger-ui .opblock.opblock-get   .opblock-summary-method { background: #1a4a5f; }
    .swagger-ui .opblock.opblock-post  { background: #1a1a10; border-color: #4a5a1f; }
    .swagger-ui .opblock.opblock-post  .opblock-summary-method { background: #4a5a1f; }
    .swagger-ui .opblock.opblock-put   { background: #1a140a; border-color: #6a4a1a; }
    .swagger-ui .opblock.opblock-put   .opblock-summary-method { background: #6a4a1a; }
    .swagger-ui .opblock.opblock-patch { background: #14140a; border-color: #4a4a1a; }
    .swagger-ui .opblock.opblock-patch .opblock-summary-method { background: #4a4a1a; }
    .swagger-ui .opblock.opblock-delete{ background: #1a0e12; border-color: #6a1a2a; }
    .swagger-ui .opblock.opblock-delete .opblock-summary-method { background: #6a1a2a; }
    .swagger-ui table thead tr th, .swagger-ui table thead tr td { color: #f5a623; border-bottom: 1px solid #2a1a20; }
    .swagger-ui .parameter__name, .swagger-ui .parameter__type, .swagger-ui .parameter__deprecated,
    .swagger-ui .parameter__in, .swagger-ui .response-col_status, .swagger-ui .response-col_description__inner {
      color: #d8d8dc;
    }
    .swagger-ui .btn { background: #12121a; color: #f5a623; border: 1px solid #4a3a20; }
    .swagger-ui .btn:hover { background: #1c1c26; }
    .swagger-ui .btn.authorize { color: #e21e51; border-color: #e21e51; }
    .swagger-ui .btn.execute { background: #e21e51; color: #fff; border-color: #e21e51; }
    .swagger-ui input[type=text], .swagger-ui input[type=password], .swagger-ui input[type=email],
    .swagger-ui input[type=file], .swagger-ui textarea, .swagger-ui select {
      background: #0a0a0f; color: #d8d8dc; border: 1px solid #2a1a20;
    }
    .swagger-ui .model-box, .swagger-ui .model { background: #0f0f14; }
    .swagger-ui .highlight-code, .swagger-ui .microlight { background: #06060a !important; }
    .swagger-ui .highlight-code pre, .swagger-ui .microlight * { color: #d8d8dc !important; }
    .swagger-ui .dialog-ux .modal-ux { background: #0f0f14; border: 1px solid #e21e51; }
    .swagger-ui .dialog-ux .modal-ux-header, .swagger-ui .dialog-ux .modal-ux-content { color: #d8d8dc; }
    .swagger-ui select { background-image: none; }
    .swagger-ui .prop-format, .swagger-ui .prop-type { color: #f5a623; }
    .swagger-ui section.models { background: #0f0f14; border-color: #2a1a20; }
    .swagger-ui section.models h4 { color: #e21e51; border-bottom-color: #2a1a20; }
    /* --- floating action panel (bottom-right) --- */
    #boom-fab {
      position: fixed; right: 20px; bottom: 20px; z-index: 9999;
      display: flex; flex-direction: column; gap: 8px; align-items: flex-end;
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    }
    #boom-fab button, #boom-fab a {
      background: #12121a; color: #f5a623; border: 1px solid #f5a623;
      padding: 8px 14px; font-size: 11px; text-transform: uppercase; letter-spacing: 0.14em;
      cursor: pointer; text-decoration: none; transition: background .15s;
    }
    #boom-fab button:hover, #boom-fab a:hover { background: #1c1c26; }
    #boom-fab .boom-fab-primary { border-color: #e21e51; color: #e21e51; }
    #boom-token-modal {
      position: fixed; inset: 0; z-index: 10000;
      background: rgba(0,0,0,0.85);
      display: flex; align-items: center; justify-content: center;
    }
    #boom-token-modal .box {
      background: #0f0f14; border: 1px solid #e21e51; padding: 24px;
      max-width: 560px; min-width: 400px;
      font-family: ui-monospace, monospace; color: #d8d8dc;
    }
    #boom-token-modal h3 { color: #e21e51; margin: 0 0 12px; letter-spacing: 0.14em; }
    #boom-token-modal code {
      display: block; padding: 12px; background: #06060a; color: #f5a623;
      word-break: break-all; margin: 12px 0; border: 1px solid #2a1a20;
    }
    #boom-token-modal .warn { color: #f5a623; font-size: 11px; margin-top: 8px; }
    #boom-token-modal .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px; }
  ` + "`" + `;
  const style = document.createElement('style');
  style.textContent = css;
  document.head.appendChild(style);

  window.ui = SwaggerUIBundle({
    url: "/api/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    persistAuthorization: true,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: "StandaloneLayout"
  });

  // --- 2. floating action panel --- probes /auth/users/current for a live
  //     session cookie. If logged in: "generate token" button that mints
  //     a fresh API token in-page and displays it once. If NOT logged in:
  //     a "sign in" link back to the app. Always shows a "back to app" link.
  const fab = document.createElement('div');
  fab.id = 'boom-fab';
  document.body.appendChild(fab);

  const backLink = document.createElement('a');
  backLink.href = '/';
  backLink.textContent = '← back to app';
  fab.appendChild(backLink);

  fetch('/auth/users/current', { credentials: 'include' })
    .then(r => r.ok ? r.json() : Promise.reject(r.status))
    .then(_user => renderGenerateBtn())
    .catch(() => renderSignInLink());

  function renderGenerateBtn() {
    const btn = document.createElement('button');
    btn.className = 'boom-fab-primary';
    btn.textContent = '▸ generate api token';
    btn.onclick = onGenerate;
    fab.insertBefore(btn, backLink);
  }

  function renderSignInLink() {
    const a = document.createElement('a');
    a.className = 'boom-fab-primary';
    a.href = '/';
    a.textContent = '▸ sign in to mint tokens';
    fab.insertBefore(a, backLink);
  }

  async function onGenerate() {
    const name = window.prompt('Optional token name (visible in Settings > Tokens; blank = unnamed):', 'swagger-ui');
    if (name === null) return; // cancelled
    try {
      // Step 1: refresh_token uses the session cookie, gets an access token.
      const refreshRes = await fetch('/auth/refresh_token', { method: 'POST', credentials: 'include' });
      if (!refreshRes.ok) throw new Error('Session expired — sign in again.');
      const { token: accessToken } = await refreshRes.json();
      // Step 2: mint the API token using the access token as Basic auth.
      const authValue = 'Basic ' + btoa(accessToken);
      const mintRes = await fetch('/auth/create_api_token', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Authorization': authValue },
        body: JSON.stringify({ name: (name || '').trim() })
      });
      if (!mintRes.ok) throw new Error('Mint failed (' + mintRes.status + ')');
      const { apiToken } = await mintRes.json();
      showTokenModal(apiToken);
    } catch (e) {
      alert(e && e.message ? e.message : 'Token generation failed.');
    }
  }

  function showTokenModal(token) {
    const wrap = document.createElement('div');
    wrap.id = 'boom-token-modal';
    wrap.innerHTML = ` + "`" + `
      <div class="box">
        <h3>▓ NEW API TOKEN</h3>
        <div>Copy this now — it won't be shown again.</div>
        <code id="boom-token-value"></code>
        <div class="warn">Store securely. Revoke via Settings &gt; Tokens.</div>
        <div class="actions">
          <button id="boom-token-copy">▸ COPY</button>
          <button id="boom-token-authorize">▸ AUTHORIZE HERE</button>
          <button id="boom-token-close">▸ CLOSE</button>
        </div>
      </div>
    ` + "`" + `;
    document.body.appendChild(wrap);
    document.getElementById('boom-token-value').textContent = token;
    document.getElementById('boom-token-copy').onclick = () => {
      navigator.clipboard.writeText(token).then(
        () => document.getElementById('boom-token-copy').textContent = '▸ COPIED',
        () => alert('Copy failed — select the token and copy manually.')
      );
    };
    document.getElementById('boom-token-authorize').onclick = () => {
      // Preload the Authorize dialog with the newly-minted token so the
      // operator can "Try it out" without a manual paste. Swagger UI's
      // authActions.authorize() takes a map keyed by security scheme id.
      const schemes = window.ui.getSystem().authSelectors.definitionsToAuthorize()?.toJS();
      const first = schemes && schemes[0] && Object.keys(schemes[0])[0];
      if (first) {
        window.ui.authActions.authorize({
          [first]: { name: first, schema: schemes[0][first], value: token }
        });
      }
      wrap.remove();
    };
    document.getElementById('boom-token-close').onclick = () => wrap.remove();
    wrap.onclick = (e) => { if (e.target === wrap) wrap.remove(); };
  }
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
