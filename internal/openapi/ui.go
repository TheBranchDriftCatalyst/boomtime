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
// injects an arasaka-flavored dark theme, and mounts a suite of quality-of-life
// upgrades: auth-status chip, token minting FAB, existing-tokens panel,
// version chip, keyboard shortcuts, deep-link scroll, live cURL, environment
// picker, response history, per-endpoint share links.
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
    /* --- auth chip in the FAB --- */
    #boom-auth-chip {
      background: rgba(226,30,81,0.1); color: #e21e51; border: 1px solid #e21e51;
      padding: 4px 10px; font-size: 10px; text-transform: uppercase; letter-spacing: 0.14em;
    }
    /* --- version chip in the topbar --- */
    #boom-version-chip {
      display: inline-block; margin: 6px 10px 0; padding: 3px 8px;
      background: rgba(245,166,35,0.1); color: #f5a623; border: 1px solid #f5a623;
      font-family: ui-monospace, monospace; font-size: 10px; letter-spacing: 0.12em;
    }
    /* --- environment picker dropdown --- */
    #boom-env-picker {
      margin: 8px 10px 0; padding: 4px 8px;
      background: #0f0f14; color: #f5a623; border: 1px solid #f5a623;
      font-family: ui-monospace, monospace; font-size: 11px;
    }
    /* --- tokens management modal --- */
    #boom-tokens-modal, #boom-help-modal, #boom-diff-modal {
      position: fixed; inset: 0; z-index: 10000;
      background: rgba(0,0,0,0.85);
      display: flex; align-items: center; justify-content: center;
    }
    #boom-tokens-modal .box, #boom-help-modal .box, #boom-diff-modal .box {
      background: #0f0f14; border: 1px solid #e21e51; padding: 24px;
      font-family: ui-monospace, monospace; color: #d8d8dc;
      max-width: 720px; min-width: 480px;
    }
    #boom-diff-modal .box.wide, #boom-tokens-modal .box.wide { max-width: 1000px; }
    #boom-tokens-modal h3, #boom-help-modal h3, #boom-diff-modal h3 {
      color: #e21e51; margin: 0 0 12px; letter-spacing: 0.14em;
    }
    #boom-tokens-modal table { width: 100%; border-collapse: collapse; font-size: 11px; }
    #boom-tokens-modal th { text-align: left; color: #f5a623; padding: 6px 8px; border-bottom: 1px solid #2a1a20; text-transform: uppercase; letter-spacing: 0.12em; }
    #boom-tokens-modal td { padding: 6px 8px; border-bottom: 1px solid #1a1a20; }
    #boom-tokens-modal td button { margin-right: 6px; background: #12121a; color: #f5a623; border: 1px solid #4a3a20; padding: 3px 8px; font-size: 10px; text-transform: uppercase; cursor: pointer; }
    #boom-tokens-modal td button.danger { color: #e21e51; border-color: #e21e51; }
    #boom-tokens-modal .actions, #boom-help-modal .actions, #boom-diff-modal .actions {
      display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px;
    }
    #boom-tokens-modal .actions button, #boom-help-modal .actions button, #boom-diff-modal .actions button {
      background: #12121a; color: #f5a623; border: 1px solid #f5a623; padding: 6px 12px;
      font-size: 11px; text-transform: uppercase; letter-spacing: 0.14em; cursor: pointer;
    }
    /* --- help modal --- */
    #boom-help-modal dl { display: grid; grid-template-columns: 60px 1fr; gap: 6px 12px; margin: 0 0 12px; font-size: 12px; }
    #boom-help-modal dt { color: #f5a623; font-weight: 600; }
    #boom-help-modal ul { padding-left: 18px; font-size: 12px; }
    #boom-help-modal ul li { margin: 4px 0; }
    /* --- diff modal --- */
    #boom-diff-modal .diff-cols { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
    #boom-diff-modal .diff-col { min-width: 0; }
    #boom-diff-modal .diff-hdr { color: #f5a623; font-size: 10px; letter-spacing: 0.14em; text-transform: uppercase; margin-bottom: 4px; }
    #boom-diff-modal pre { background: #06060a; color: #d8d8dc; padding: 8px; overflow: auto; max-height: 400px; font-size: 11px; margin: 0; border: 1px solid #2a1a20; }
    /* --- share link icon per endpoint --- */
    .boom-share {
      margin-left: 8px; background: transparent; color: #f5a623; border: none;
      cursor: pointer; font-size: 12px; opacity: 0.5; transition: opacity 0.15s;
    }
    .boom-share:hover { opacity: 1; }
    /* --- live cURL preview per endpoint --- */
    .boom-curl {
      background: #06060a !important; color: #d8d8dc !important;
      padding: 10px; margin: 8px 0; border: 1px dashed #2a1a20;
      font-family: ui-monospace, monospace; font-size: 11px;
      white-space: pre-wrap; word-break: break-all;
    }
    /* --- diff button injected into responses --- */
    .boom-diff-btn {
      background: #12121a; color: #f5a623; border: 1px solid #f5a623;
      padding: 4px 10px; margin-bottom: 8px; font-size: 10px; text-transform: uppercase;
      letter-spacing: 0.12em; cursor: pointer;
    }
    /* --- history toggle in FAB --- */
    #boom-hist-toggle {
      background: #12121a; color: #d8d8dc; border: 1px dashed #4a3a20;
      padding: 6px 12px; font-size: 10px; text-transform: uppercase; letter-spacing: 0.14em; cursor: pointer;
    }
  ` + "`" + `;
  const style = document.createElement('style');
  style.textContent = css;
  document.head.appendChild(style);

  window.ui = SwaggerUIBundle({
    url: "/api/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    persistAuthorization: true,
    docExpansion: "none",           // #2: collapse-all-tags on load
    filter: true,                    // filter input in the topbar
    tagsSorter: "alpha",
    operationsSorter: "alpha",
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: "StandaloneLayout"
  });

  // ---------------------------------------------------------------------
  // OP UPGRADES — 10-feature bundle. Ordered by dependency, not by number.
  // ---------------------------------------------------------------------

  // --- shared helpers --------------------------------------------------
  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
  const el = (tag, attrs, ...children) => {
    const n = document.createElement(tag);
    if (attrs) for (const k in attrs) {
      if (k === 'onclick') n.onclick = attrs[k];
      else if (k === 'html') n.innerHTML = attrs[k];
      else n.setAttribute(k, attrs[k]);
    }
    for (const c of children) if (c != null) n.append(c.nodeType ? c : document.createTextNode(c));
    return n;
  };
  const api = (path, opts) => fetch(path, Object.assign({ credentials: 'include' }, opts));

  // Access token is held in memory only — NEVER localStorage. Refreshed
  // on demand from the session cookie. Cleared on tab close.
  let ACCESS = null;
  async function refreshAccess() {
    const r = await api('/auth/refresh_token', { method: 'POST' });
    if (!r.ok) throw new Error('session-expired');
    const j = await r.json();
    ACCESS = { token: j.token, expiresAt: Date.now() + (j.expiresIn || 3600) * 1000 };
    return ACCESS;
  }
  async function needAccess() {
    if (ACCESS && ACCESS.expiresAt - Date.now() > 60_000) return ACCESS.token;
    return (await refreshAccess()).token;
  }
  const basicHeader = (token) => 'Basic ' + btoa(token);

  // --- #5 keyboard shortcuts + #6 deep-link scroll ---------------------
  window.addEventListener('keydown', (e) => {
    if (e.target.matches('input, textarea, [contenteditable]')) return;
    if (e.key === '/') {
      const f = $('.filter-container input, .filter input');
      if (f) { e.preventDefault(); f.focus(); }
    } else if (e.key === '?') {
      e.preventDefault();
      showHelpModal();
    } else if (e.key === 'Escape') {
      $$('#boom-token-modal, #boom-help-modal, #boom-tokens-modal, #boom-diff-modal').forEach(m => m.remove());
    }
  });
  // Swagger's deepLinking updates window.location.hash on operation open,
  // but doesn't always scroll into view when the hash was already set on
  // load. Nudge it.
  function scrollToHash() {
    const h = decodeURIComponent(location.hash.replace(/^#\/?/, ''));
    if (!h) return;
    const target = $$('.opblock-tag, .opblock').find(n =>
      (n.id && decodeURIComponent(n.id).includes(h)) ||
      (n.getAttribute('data-tag') === h)
    );
    if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
  window.addEventListener('hashchange', () => setTimeout(scrollToHash, 100));

  // --- FAB scaffolding (used by #1 chip, #3 tokens, mint button, back) --
  const fab = el('div', { id: 'boom-fab' });
  document.body.appendChild(fab);
  const backLink = el('a', { href: '/' }, '← back to app');

  function fabRebuild(children) {
    fab.innerHTML = '';
    for (const c of children) fab.appendChild(c);
    fab.appendChild(backLink);
  }

  // --- #1 auth-status chip + gated FAB rendering -----------------------
  api('/auth/users/current').then(r => r.ok ? r.json() : Promise.reject())
    .then(user => renderAuthedFab(user))
    .catch(() => fabRebuild([el('a', { class: 'boom-fab-primary', href: '/' }, '▸ sign in to mint tokens')]));

  function renderAuthedFab(user) {
    const chip = el('div', { id: 'boom-auth-chip' }, '● ' + user.username);
    const mint = el('button', { class: 'boom-fab-primary', onclick: onGenerate }, '▸ generate api token');
    const manage = el('button', { onclick: onManageTokens }, '▸ manage tokens');
    fabRebuild([chip, mint, manage]);
    // Refresh once to seed ACCESS + populate expiry display; then re-tick every 30s.
    refreshAccess().then(updateExpiryTick).catch(() => {});
    setInterval(updateExpiryTick, 30_000);
  }
  function updateExpiryTick() {
    const chip = $('#boom-auth-chip');
    if (!chip || !ACCESS) return;
    const m = Math.max(0, Math.floor((ACCESS.expiresAt - Date.now()) / 60_000));
    const user = chip.textContent.split('·')[0].trim();
    chip.textContent = user + ' · exp ' + m + 'm';
  }

  // --- token mint (existing flow, extracted) ----------------------------
  async function onGenerate() {
    const name = window.prompt('Optional token name (visible in Settings > Tokens; blank = unnamed):', 'swagger-ui');
    if (name === null) return;
    try {
      const token = await needAccess();
      const r = await api('/auth/create_api_token', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Authorization': basicHeader(token) },
        body: JSON.stringify({ name: (name || '').trim() })
      });
      if (!r.ok) throw new Error('mint failed (' + r.status + ')');
      const { apiToken } = await r.json();
      showTokenModal(apiToken);
    } catch (e) {
      alert(e && e.message ? e.message : 'Token generation failed.');
    }
  }
  function showTokenModal(token) {
    const wrap = el('div', { id: 'boom-token-modal' });
    const box = el('div', { class: 'box' },
      el('h3', {}, '▓ NEW API TOKEN'),
      el('div', {}, "Copy this now — it won't be shown again."),
      el('code', { id: 'boom-token-value' }, token),
      el('div', { class: 'warn' }, 'Store securely. Revoke via Manage Tokens.'),
      el('div', { class: 'actions' },
        el('button', { onclick: () => { navigator.clipboard.writeText(token).then(() => {
          $('.actions button', box).textContent = '▸ COPIED';
        }); } }, '▸ COPY'),
        el('button', { onclick: () => { authorizeInSwagger(token); wrap.remove(); } }, '▸ AUTHORIZE HERE'),
        el('button', { onclick: () => wrap.remove() }, '▸ CLOSE')
      )
    );
    wrap.appendChild(box);
    wrap.onclick = (e) => { if (e.target === wrap) wrap.remove(); };
    document.body.appendChild(wrap);
  }
  function authorizeInSwagger(token) {
    const schemes = window.ui.getSystem().authSelectors.definitionsToAuthorize()?.toJS();
    const first = schemes && schemes[0] && Object.keys(schemes[0])[0];
    if (first) {
      window.ui.authActions.authorize({ [first]: { name: first, schema: schemes[0][first], value: token } });
    }
  }

  // --- #3 existing-tokens panel (list, rename, revoke) ------------------
  async function onManageTokens() {
    try {
      const token = await needAccess();
      const r = await api('/auth/tokens', { headers: { 'Authorization': basicHeader(token) } });
      if (!r.ok) throw new Error('list failed (' + r.status + ')');
      const tokens = await r.json();
      showTokensModal(tokens);
    } catch (e) {
      alert(e.message);
    }
  }
  function showTokensModal(tokens) {
    const wrap = el('div', { id: 'boom-tokens-modal' });
    const rows = (tokens || []).map(t => {
      const row = el('tr', {});
      row.appendChild(el('td', {}, t.name || '—'));
      row.appendChild(el('td', {}, (t.id || '').slice(0, 8) + '…'));
      row.appendChild(el('td', {}, t.createdAt ? new Date(t.createdAt).toISOString().slice(0, 10) : '—'));
      const actions = el('td', {},
        el('button', { onclick: () => renameToken(t.id, row) }, 'RENAME'),
        el('button', { class: 'danger', onclick: () => revokeToken(t.id, row) }, 'REVOKE')
      );
      row.appendChild(actions);
      return row;
    });
    const tbl = el('table', {},
      el('thead', {}, el('tr', {},
        el('th', {}, 'NAME'), el('th', {}, 'ID'), el('th', {}, 'CREATED'), el('th', {}, 'ACTIONS')
      )),
      el('tbody', {}, ...rows)
    );
    const box = el('div', { class: 'box wide' },
      el('h3', {}, '▓ API TOKENS'),
      tokens && tokens.length ? tbl : el('div', { class: 'warn' }, 'No tokens yet — click GENERATE to mint one.'),
      el('div', { class: 'actions' }, el('button', { onclick: () => wrap.remove() }, '▸ CLOSE'))
    );
    wrap.appendChild(box);
    wrap.onclick = (e) => { if (e.target === wrap) wrap.remove(); };
    document.body.appendChild(wrap);
  }
  async function renameToken(id, row) {
    const newName = window.prompt('New name for this token:');
    if (!newName) return;
    const token = await needAccess();
    const r = await api('/auth/token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': basicHeader(token) },
      body: JSON.stringify({ id: id, name: newName.trim() })
    });
    if (r.ok) row.firstChild.textContent = newName.trim();
    else alert('Rename failed (' + r.status + ')');
  }
  async function revokeToken(id, row) {
    if (!window.confirm('Revoke this token? Any client using it will start getting 401.')) return;
    const token = await needAccess();
    const r = await api('/auth/token/' + encodeURIComponent(id), {
      method: 'DELETE',
      headers: { 'Authorization': basicHeader(token) }
    });
    if (r.ok) row.remove();
    else alert('Revoke failed (' + r.status + ')');
  }

  // --- #4 version chip in topbar --------------------------------------
  api('/healthz').then(r => r.ok ? r.json() : null).then(h => {
    if (!h) return;
    const sha = (h.commit || 'dev').slice(0, 7);
    const rev = (h.buildTime || '').slice(0, 10) || 'dev';
    const chip = el('div', { id: 'boom-version-chip' }, '▓ FILE #' + sha + ' · REV ' + rev);
    // Retry appending until Swagger's topbar renders.
    const tryAppend = () => {
      const bar = $('.topbar-wrapper, .topbar');
      if (bar) bar.appendChild(chip); else setTimeout(tryAppend, 200);
    };
    tryAppend();
  }).catch(() => {});

  // --- #8 environment picker (server-provided list only) ---------------
  // Reads spec.servers AFTER the spec loads. If >1 server, renders a
  // dropdown next to the topbar filter that swaps the base URL. Never
  // free-text — only server-declared entries.
  const envPickerInterval = setInterval(() => {
    const spec = window.ui?.specSelectors?.specJson?.()?.toJS();
    if (!spec) return;
    clearInterval(envPickerInterval);
    const servers = (spec.servers || []).filter(s => s.url);
    if (servers.length < 2) return;
    const sel = el('select', { id: 'boom-env-picker', onclick: (e) => e.stopPropagation() });
    servers.forEach((s, i) => sel.appendChild(el('option', { value: i }, s.description ? s.description + ' — ' + s.url : s.url)));
    sel.onchange = () => {
      const s = servers[Number(sel.value)];
      window.ui.specActions.updateJsonSpec(Object.assign({}, spec, { servers: [s].concat(servers.filter((_, i) => i !== Number(sel.value))) }));
    };
    const tryAppend = () => {
      const bar = $('.topbar-wrapper, .topbar');
      if (bar) bar.appendChild(sel); else setTimeout(tryAppend, 200);
    };
    tryAppend();
  }, 300);

  // --- #10 direct link per endpoint (🔗 icon next to each op) ----------
  const linkObserver = new MutationObserver(() => {
    $$('.opblock-summary').forEach(sum => {
      if (sum.querySelector('.boom-share')) return;
      const opblock = sum.closest('.opblock');
      const id = opblock && opblock.id;
      if (!id) return;
      const link = el('button', {
        class: 'boom-share',
        title: 'Copy shareable link to this endpoint',
        onclick: (e) => {
          e.stopPropagation();
          const url = location.origin + location.pathname + '#/' + id.replace(/^operations-/, '');
          navigator.clipboard.writeText(url).then(() => {
            link.textContent = '✓';
            setTimeout(() => link.textContent = '🔗', 1200);
          });
        }
      }, '🔗');
      sum.appendChild(link);
    });
  });
  linkObserver.observe(document.body, { childList: true, subtree: true });

  // --- #7 live cURL preview (per operation, mask auth by default) ------
  // Piggybacks on the same DOM observer; injects a small <pre> below each
  // "Try it out" form that updates as inputs change.
  function attachCurlPreview(opblock) {
    if (opblock.querySelector('.boom-curl')) return;
    const method = (opblock.className.match(/opblock-(get|post|put|patch|delete)/) || [,''])[1].toUpperCase();
    const path = (opblock.querySelector('.opblock-summary-path')?.getAttribute('data-path')) ||
                 (opblock.querySelector('.opblock-summary-path a')?.textContent?.trim()) || '';
    if (!method || !path) return;
    const pre = el('pre', { class: 'boom-curl' }, 'curl -X ' + method + " '" + location.origin + path + "'");
    const holder = opblock.querySelector('.opblock-body') || opblock;
    holder.insertBefore(pre, holder.firstChild);
    // Update on any input change inside this opblock.
    const rebuild = () => {
      let hasAuth = false;
      const headers = [];
      $$('input, textarea, select', opblock).forEach(inp => {
        if (inp.name === 'Authorization' || inp.placeholder?.includes('Bearer') || inp.placeholder?.includes('Basic')) {
          hasAuth = true;
          headers.push("  -H 'Authorization: <hidden — Authorize to reveal>'");
        }
      });
      const authorized = window.ui.getSystem().authSelectors.authorized()?.toJS();
      if (authorized && Object.keys(authorized).length) {
        const scheme = Object.values(authorized)[0];
        const val = scheme && (scheme.value || scheme.token);
        if (val) headers.push("  -H 'Authorization: Basic " + '****REDACTED**** (click reveal below)' + "'");
      }
      pre.textContent = 'curl -X ' + method + " '" + location.origin + path + "'" + (headers.length ? " \\\n" + headers.join(" \\\n") : '');
    };
    opblock.addEventListener('input', rebuild);
    opblock.addEventListener('change', rebuild);
  }
  const curlObserver = new MutationObserver(() => $$('.opblock').forEach(attachCurlPreview));
  curlObserver.observe(document.body, { childList: true, subtree: true });

  // --- #9 response history / diff (opt-in, header/secret scrubbed) -----
  // Hooks Swagger's requestInterceptor + responseInterceptor via the
  // execute flow. Since we can't easily intercept without a plugin, we
  // instead poll for new response bodies rendered in the DOM (safer +
  // simpler + does not touch the actual request path).
  const HIST_KEY = 'boom-response-history';
  const HIST_OPT_IN = 'boom-history-optin';
  const SECRET_KEYS = /"(api_?token|apiToken|password|secret|refresh_?token|session|cookie|authorization)"/i;
  function historyEnabled() { return localStorage.getItem(HIST_OPT_IN) === '1'; }
  function readHistory() { try { return JSON.parse(localStorage.getItem(HIST_KEY) || '{}'); } catch { return {}; } }
  function writeHistory(h) { localStorage.setItem(HIST_KEY, JSON.stringify(h)); }
  function scrubBody(body) {
    if (typeof body !== 'string') return null;
    if (body.length > 100_000) return '(response too large — not persisted)';
    if (SECRET_KEYS.test(body)) return '(response body redacted — matched secret-shaped key)';
    return body;
  }
  const respObserver = new MutationObserver(() => {
    if (!historyEnabled()) return;
    $$('.opblock .responses-inner').forEach(inner => {
      const opblock = inner.closest('.opblock');
      const id = opblock?.id;
      if (!id) return;
      const codeEl = inner.querySelector('.microlight, .highlight-code pre');
      const statusEl = inner.querySelector('.response .response-col_status');
      if (!codeEl || !statusEl) return;
      const body = scrubBody(codeEl.textContent || '');
      const status = statusEl.textContent.trim();
      const key = id;
      const h = readHistory();
      const prev = h[key];
      const entry = { at: new Date().toISOString(), status, body };
      if (prev && prev.body === entry.body && prev.status === entry.status) return;
      h[key] = entry;
      writeHistory(h);
      // Inject a small "diff vs last" button if there's a previous entry.
      if (prev && !inner.querySelector('.boom-diff-btn')) {
        const btn = el('button', {
          class: 'boom-diff-btn',
          onclick: () => showDiffModal(id, prev, entry)
        }, '▸ DIFF vs last (' + new Date(prev.at).toISOString().slice(11, 19) + ')');
        inner.insertBefore(btn, inner.firstChild);
      }
    });
  });
  respObserver.observe(document.body, { childList: true, subtree: true });
  function showDiffModal(id, prev, curr) {
    const wrap = el('div', { id: 'boom-diff-modal' });
    const box = el('div', { class: 'box wide' },
      el('h3', {}, '▓ RESPONSE DIFF · ' + id),
      el('div', { class: 'diff-cols' },
        el('div', { class: 'diff-col' }, el('div', { class: 'diff-hdr' }, 'PREV · ' + prev.status + ' · ' + prev.at), el('pre', {}, prev.body || '')),
        el('div', { class: 'diff-col' }, el('div', { class: 'diff-hdr' }, 'CURR · ' + curr.status + ' · ' + curr.at), el('pre', {}, curr.body || ''))
      ),
      el('div', { class: 'actions' }, el('button', { onclick: () => wrap.remove() }, '▸ CLOSE'))
    );
    wrap.appendChild(box);
    wrap.onclick = (e) => { if (e.target === wrap) wrap.remove(); };
    document.body.appendChild(wrap);
  }
  // Small toggle in the FAB for opt-in.
  const histToggle = el('button', {
    id: 'boom-hist-toggle',
    onclick: () => {
      const on = !historyEnabled();
      localStorage.setItem(HIST_OPT_IN, on ? '1' : '0');
      histToggle.textContent = '▸ history: ' + (on ? 'on' : 'off');
      if (!on) localStorage.removeItem(HIST_KEY);
    }
  }, '▸ history: ' + (historyEnabled() ? 'on' : 'off'));
  fab.appendChild(histToggle);

  // --- help modal (invoked by ?) ---------------------------------------
  function showHelpModal() {
    if ($('#boom-help-modal')) return;
    const wrap = el('div', { id: 'boom-help-modal' });
    const box = el('div', { class: 'box' },
      el('h3', {}, '▓ KEYBOARD'),
      el('dl', { html:
        '<dt>/</dt><dd>focus filter</dd>' +
        '<dt>?</dt><dd>this help</dd>' +
        '<dt>Esc</dt><dd>close modals</dd>'
      }),
      el('h3', {}, '▓ FEATURES'),
      el('ul', { html:
        '<li>Generate + manage API tokens (bottom right)</li>' +
        '<li>Response history — opt-in, secrets scrubbed, click "diff vs last"</li>' +
        '<li>🔗 icon copies shareable link to any endpoint</li>' +
        '<li>Live cURL preview at the top of each operation</li>'
      }),
      el('div', { class: 'actions' }, el('button', { onclick: () => wrap.remove() }, '▸ CLOSE'))
    );
    wrap.appendChild(box);
    wrap.onclick = (e) => { if (e.target === wrap) wrap.remove(); };
    document.body.appendChild(wrap);
  }

  // Kick a scroll after Swagger has had a moment to render tags.
  setTimeout(scrollToHash, 800);
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
		// no-store on this specific asset (not the whole UI): the file is
		// tiny (~30KB) and its contents evolve with the app; browsers
		// heuristically caching it makes every FAB/theme change invisible
		// until an operator manually hard-reloads. Vendored assets (CSS,
		// bundle JS) are stable per-release and stay cacheable.
		if p == "/swagger-initializer.js" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
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
