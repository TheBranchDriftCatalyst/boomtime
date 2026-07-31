// boomtime/openapi: custom swagger-initializer.
window.onload = function () {
  // --- 1. dark theme --- Arasaka corporate dossier: crimson + jet black
  //     + one amber accent. Punched-up contrast, monospace method chips,
  //     scanline overlay, corner brackets on op blocks, subtle glow on
  //     primary actions. Tuned against Swagger UI 5.x class names.
  const css = `
    /* ----- import Chakra Petch for headers ----- */
    @import url('https://fonts.googleapis.com/css2?family=Chakra+Petch:wght@500;600;700&family=JetBrains+Mono:wght@400;500;600&display=swap');

    :root {
      --bx-bg: #08080c;
      --bx-panel: #0e0e14;
      --bx-elev: #14141c;
      --bx-line: #241820;
      --bx-line-hot: #3a2028;
      --bx-fg: #dee0e6;
      --bx-fg-muted: #90929a;
      --bx-crimson: #ff2a5b;
      --bx-crimson-dim: #b81640;
      --bx-amber: #f5a623;
      --bx-cyan: #35c8ff;
      --bx-green: #6dd07a;
      --bx-purple: #b183ff;
    }

    ::selection { background: var(--bx-crimson); color: #fff; }
    ::-webkit-scrollbar { width: 10px; height: 10px; }
    ::-webkit-scrollbar-track { background: var(--bx-bg); }
    ::-webkit-scrollbar-thumb { background: var(--bx-line-hot); border: 2px solid var(--bx-bg); }
    ::-webkit-scrollbar-thumb:hover { background: var(--bx-crimson-dim); }

    html, body {
      background: var(--bx-bg) !important;
      color: var(--bx-fg) !important;
      font-family: 'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
    }

    /* subtle graph-paper grid + scanline overlay across the whole page */
    body::before {
      content: ''; position: fixed; inset: 0; pointer-events: none; z-index: 0;
      background:
        linear-gradient(to right, rgba(255,42,91,0.035) 1px, transparent 1px) 0 0/24px 24px,
        linear-gradient(to bottom, rgba(255,42,91,0.035) 1px, transparent 1px) 0 0/24px 24px;
    }
    body::after {
      content: ''; position: fixed; inset: 0; pointer-events: none; z-index: 0;
      background: linear-gradient(transparent 50%, rgba(0,0,0,0.35) 50%);
      background-size: 100% 3px; opacity: 0.05;
    }
    #swagger-ui { position: relative; z-index: 1; }

    /* ----- topbar (URL bar + Explore button) ----- */
    .swagger-ui .topbar {
      background: var(--bx-panel);
      border-bottom: 1px solid var(--bx-crimson);
      box-shadow: 0 0 12px rgba(255,42,91,0.15);
      padding: 8px 0;
    }
    .swagger-ui .topbar .download-url-wrapper input[type=text] {
      background: var(--bx-bg); color: var(--bx-fg);
      border: 1px solid var(--bx-line-hot);
      border-radius: 0;
    }
    .swagger-ui .topbar .download-url-wrapper .download-url-button {
      background: var(--bx-crimson); color: #fff;
      border: 1px solid var(--bx-crimson);
      border-radius: 0;
      font-family: 'Chakra Petch', sans-serif; font-weight: 600; letter-spacing: 0.1em;
      text-transform: uppercase;
    }
    .swagger-ui .topbar .download-url-wrapper .download-url-button:hover {
      background: var(--bx-crimson-dim);
      box-shadow: 0 0 8px var(--bx-crimson);
    }

    /* ----- info block (title + description) ----- */
    .swagger-ui .info { margin: 32px 0 24px; }
    .swagger-ui .info .title {
      color: var(--bx-crimson) !important;
      font-family: 'Chakra Petch', sans-serif; font-weight: 700; letter-spacing: 0.02em;
      text-shadow: 0 0 20px rgba(255,42,91,0.4);
    }
    .swagger-ui .info .title small {
      background: var(--bx-elev); color: var(--bx-amber); border: 1px solid var(--bx-amber);
      border-radius: 0; padding: 2px 8px; font-family: 'JetBrains Mono', monospace;
    }
    .swagger-ui .info .title small.version-stamp { background: var(--bx-elev); }
    .swagger-ui .info a { color: var(--bx-amber); }
    .swagger-ui .info li, .swagger-ui .info p, .swagger-ui .info table { color: var(--bx-fg); }
    .swagger-ui .info .base-url, .swagger-ui .info .description { color: var(--bx-fg); }

    /* ----- servers dropdown + auth bar ----- */
    .swagger-ui .scheme-container {
      background: var(--bx-panel);
      border-top: 1px solid var(--bx-line);
      border-bottom: 1px solid var(--bx-line);
      box-shadow: none;
    }
    .swagger-ui .servers > label select {
      background: var(--bx-bg); color: var(--bx-fg);
      border: 1px solid var(--bx-line-hot); border-radius: 0;
      font-family: 'JetBrains Mono', monospace;
    }
    .swagger-ui .servers > label { color: var(--bx-amber); font-family: 'Chakra Petch', sans-serif; letter-spacing: 0.08em; }

    /* ----- filter input ----- */
    .swagger-ui .filter .operation-filter-input {
      background: var(--bx-panel) !important; color: var(--bx-fg) !important;
      border: 1px solid var(--bx-line-hot) !important; border-radius: 0 !important;
      font-family: 'JetBrains Mono', monospace;
    }
    .swagger-ui .filter .operation-filter-input::placeholder { color: var(--bx-fg-muted); }

    /* ----- tag headers ----- */
    .swagger-ui .opblock-tag {
      color: var(--bx-crimson) !important;
      border-bottom: 1px solid var(--bx-line-hot) !important;
      font-family: 'Chakra Petch', sans-serif !important; font-weight: 600 !important;
      letter-spacing: 0.06em !important; text-transform: uppercase;
      padding: 14px 0 !important;
    }
    .swagger-ui .opblock-tag small { color: var(--bx-fg-muted); font-family: 'JetBrains Mono', monospace; font-weight: 400; letter-spacing: 0; text-transform: none; }
    .swagger-ui .opblock-tag svg { fill: var(--bx-crimson); }

    /* ----- operation blocks ----- */
    .swagger-ui .opblock {
      background: var(--bx-panel) !important;
      border: 1px solid var(--bx-line) !important; border-radius: 0 !important;
      box-shadow: none !important; margin: 0 0 8px !important;
      position: relative;
    }
    .swagger-ui .opblock:hover { border-color: var(--bx-line-hot) !important; }
    /* corner brackets on each opblock — arasaka signature */
    .swagger-ui .opblock::before, .swagger-ui .opblock::after {
      content: ''; position: absolute; width: 8px; height: 8px;
      border-color: var(--bx-crimson); pointer-events: none; z-index: 2;
      opacity: 0; transition: opacity 0.2s;
    }
    .swagger-ui .opblock::before { top: -1px; left: -1px; border-top: 1px solid; border-left: 1px solid; }
    .swagger-ui .opblock::after { bottom: -1px; right: -1px; border-bottom: 1px solid; border-right: 1px solid; }
    .swagger-ui .opblock:hover::before, .swagger-ui .opblock:hover::after,
    .swagger-ui .opblock.is-open::before, .swagger-ui .opblock.is-open::after { opacity: 0.9; }

    .swagger-ui .opblock .opblock-summary { border: none !important; padding: 6px 12px !important; }
    .swagger-ui .opblock .opblock-summary-path,
    .swagger-ui .opblock .opblock-summary-path a {
      color: var(--bx-fg) !important; font-family: 'JetBrains Mono', monospace !important;
    }
    .swagger-ui .opblock .opblock-summary-operation-id { color: var(--bx-fg-muted) !important; }
    .swagger-ui .opblock .opblock-summary-description { color: var(--bx-fg-muted) !important; }

    /* ----- method chips: monospace, bordered, uppercase, on-brand ----- */
    .swagger-ui .opblock-summary-method {
      background: transparent !important; color: var(--bx-fg) !important;
      border: 1px solid; border-radius: 0 !important;
      font-family: 'JetBrains Mono', monospace !important; font-weight: 600 !important;
      letter-spacing: 0.14em !important; text-shadow: none !important;
      min-width: 72px; padding: 4px 8px !important;
    }
    .swagger-ui .opblock.opblock-get   .opblock-summary-method { color: var(--bx-cyan);   border-color: var(--bx-cyan); }
    .swagger-ui .opblock.opblock-post  .opblock-summary-method { color: var(--bx-green);  border-color: var(--bx-green); }
    .swagger-ui .opblock.opblock-put   .opblock-summary-method { color: var(--bx-amber);  border-color: var(--bx-amber); }
    .swagger-ui .opblock.opblock-patch .opblock-summary-method { color: var(--bx-purple); border-color: var(--bx-purple); }
    .swagger-ui .opblock.opblock-delete .opblock-summary-method { color: var(--bx-crimson); border-color: var(--bx-crimson); }
    /* subtle left-border wash keyed by method */
    .swagger-ui .opblock.opblock-get   { border-left: 3px solid var(--bx-cyan) !important; }
    .swagger-ui .opblock.opblock-post  { border-left: 3px solid var(--bx-green) !important; }
    .swagger-ui .opblock.opblock-put   { border-left: 3px solid var(--bx-amber) !important; }
    .swagger-ui .opblock.opblock-patch { border-left: 3px solid var(--bx-purple) !important; }
    .swagger-ui .opblock.opblock-delete { border-left: 3px solid var(--bx-crimson) !important; }

    /* ----- inside the expanded body ----- */
    .swagger-ui .opblock-body { background: var(--bx-bg); }
    .swagger-ui .opblock-description-wrapper,
    .swagger-ui .opblock-external-docs-wrapper,
    .swagger-ui .opblock-title_normal { background: transparent; }
    .swagger-ui .opblock-description-wrapper p,
    .swagger-ui .opblock-description-wrapper h4,
    .swagger-ui .opblock-title_normal p { color: var(--bx-fg) !important; }
    .swagger-ui .tab li { color: var(--bx-fg-muted) !important; }
    .swagger-ui .tab li.active { color: var(--bx-crimson) !important; }
    .swagger-ui .tab li::after { background: var(--bx-crimson) !important; }
    /* the pale tab-bar strip in the screenshot — kill it */
    .swagger-ui .opblock-section-header {
      background: var(--bx-panel) !important; border-color: var(--bx-line) !important;
      box-shadow: none !important;
    }
    .swagger-ui .opblock-section-header h4,
    .swagger-ui .opblock-section-header .btn { color: var(--bx-amber) !important; }
    .swagger-ui .opblock-section-header > label { color: var(--bx-amber) !important; }

    /* ----- parameters + response tables ----- */
    .swagger-ui table thead tr th, .swagger-ui table thead tr td {
      color: var(--bx-amber) !important;
      border-bottom: 1px solid var(--bx-line-hot) !important;
      font-family: 'JetBrains Mono', monospace; text-transform: uppercase; letter-spacing: 0.1em;
    }
    .swagger-ui .parameters-col_description p,
    .swagger-ui .parameter__name, .swagger-ui .parameter__type,
    .swagger-ui .parameter__deprecated, .swagger-ui .parameter__in,
    .swagger-ui .response-col_status, .swagger-ui .response-col_description__inner,
    .swagger-ui .responses-inner h4, .swagger-ui .responses-inner h5 {
      color: var(--bx-fg) !important;
    }
    .swagger-ui .parameter__name.required::after { color: var(--bx-crimson) !important; }

    /* ----- buttons ----- */
    .swagger-ui .btn {
      background: var(--bx-panel) !important; color: var(--bx-fg) !important;
      border: 1px solid var(--bx-line-hot) !important; border-radius: 0 !important;
      font-family: 'Chakra Petch', sans-serif !important; font-weight: 600 !important;
      letter-spacing: 0.08em !important; text-transform: uppercase;
      box-shadow: none !important; transition: all 0.15s;
    }
    .swagger-ui .btn:hover { background: var(--bx-elev) !important; border-color: var(--bx-crimson) !important; box-shadow: 0 0 8px rgba(255,42,91,0.2); }
    .swagger-ui .btn.authorize {
      color: var(--bx-crimson) !important; border-color: var(--bx-crimson) !important;
    }
    .swagger-ui .btn.authorize svg { fill: var(--bx-crimson); }
    .swagger-ui .btn.authorize:hover { background: rgba(255,42,91,0.1) !important; box-shadow: 0 0 12px rgba(255,42,91,0.4); }
    .swagger-ui .btn.execute {
      background: var(--bx-crimson) !important; color: #fff !important;
      border-color: var(--bx-crimson) !important;
    }
    .swagger-ui .btn.execute:hover { background: var(--bx-crimson-dim) !important; box-shadow: 0 0 16px rgba(255,42,91,0.5); }
    .swagger-ui .btn.try-out__btn { color: var(--bx-amber) !important; border-color: var(--bx-amber) !important; }
    .swagger-ui .btn.cancel { color: var(--bx-crimson) !important; border-color: var(--bx-crimson) !important; }

    /* ----- inputs / textareas / selects ----- */
    .swagger-ui input[type=text], .swagger-ui input[type=password], .swagger-ui input[type=email],
    .swagger-ui input[type=search], .swagger-ui input[type=file], .swagger-ui input[type=number],
    .swagger-ui textarea, .swagger-ui select {
      background: var(--bx-bg) !important; color: var(--bx-fg) !important;
      border: 1px solid var(--bx-line-hot) !important; border-radius: 0 !important;
      font-family: 'JetBrains Mono', monospace !important;
    }
    .swagger-ui textarea.body-param__text { min-height: 100px; }
    .swagger-ui input:focus, .swagger-ui textarea:focus, .swagger-ui select:focus {
      outline: none !important; border-color: var(--bx-crimson) !important;
      box-shadow: 0 0 8px rgba(255,42,91,0.25) !important;
    }
    .swagger-ui select { background-image: none !important; padding-right: 8px; }

    /* ----- code blocks (request/response bodies, cURL) ----- */
    .swagger-ui .highlight-code, .swagger-ui .microlight,
    .swagger-ui .example, .swagger-ui pre.example {
      background: #04040a !important; border: 1px solid var(--bx-line) !important;
    }
    .swagger-ui .highlight-code pre, .swagger-ui .microlight,
    .swagger-ui .microlight *, .swagger-ui .example {
      color: var(--bx-fg) !important; font-family: 'JetBrains Mono', monospace !important;
    }
    /* syntax token colors (swagger uses generic microlight spans; hit them) */
    .swagger-ui .microlight span[style*="color: rgb(162, 252, 162)"],
    .swagger-ui .microlight span[style*="color: rgb(51, 156, 214)"] { color: var(--bx-cyan) !important; }
    .swagger-ui .microlight span[style*="color: rgb(255, 155, 155)"],
    .swagger-ui .microlight span[style*="color: rgb(230, 219, 116)"] { color: var(--bx-amber) !important; }

    /* ----- authorize dialog ----- */
    .swagger-ui .dialog-ux .modal-ux {
      background: var(--bx-panel) !important; border: 1px solid var(--bx-crimson) !important;
      border-radius: 0 !important;
    }
    .swagger-ui .dialog-ux .modal-ux-header,
    .swagger-ui .dialog-ux .modal-ux-content { color: var(--bx-fg) !important; }
    .swagger-ui .dialog-ux .modal-ux-header h3 { color: var(--bx-crimson) !important; font-family: 'Chakra Petch', sans-serif; }
    .swagger-ui .auth-container h4, .swagger-ui .auth-container .wrapper > * { color: var(--bx-fg) !important; }

    /* ============================================================
       SCHEMAS — data-sheet aesthetic. Prior pass made the section a
       floating cluster of dim text with unreadable hierarchy. Redesign:
       each schema is a bordered card with a corner-bracketed header,
       tight rows, tree-line guides for nested properties, type badges
       for primitives, hover highlight for scanning.
       ============================================================ */
    .swagger-ui section.models {
      background: var(--bx-panel) !important;
      border: 1px solid var(--bx-line) !important;
      border-radius: 0 !important;
      padding: 12px !important;
      margin: 24px 0 !important;
    }
    .swagger-ui section.models.is-open {
      padding: 12px !important;
    }
    .swagger-ui section.models > h4 {
      color: var(--bx-crimson) !important;
      border-bottom: 1px solid var(--bx-line-hot) !important;
      font-family: 'Chakra Petch', sans-serif !important;
      font-weight: 700 !important;
      text-transform: uppercase; letter-spacing: 0.14em;
      padding: 6px 0 10px !important; margin: 0 0 8px !important;
    }
    .swagger-ui section.models > h4 svg { fill: var(--bx-crimson) !important; }

    /* -- individual schema card -- */
    .swagger-ui section.models .model-container {
      background: var(--bx-bg) !important;
      border: 1px solid var(--bx-line) !important;
      border-left: 2px solid var(--bx-amber) !important;
      margin: 6px 0 !important; padding: 0 !important;
      position: relative; transition: border-color 0.15s;
    }
    .swagger-ui section.models .model-container:hover {
      border-color: var(--bx-line-hot) !important;
      border-left-color: var(--bx-crimson) !important;
    }
    .swagger-ui section.models .model-container.active {
      border-left-color: var(--bx-crimson) !important;
    }
    /* corner brackets on each schema card (matches op blocks) */
    .swagger-ui section.models .model-container::before,
    .swagger-ui section.models .model-container::after {
      content: ''; position: absolute; width: 6px; height: 6px;
      border-color: var(--bx-crimson); pointer-events: none;
      opacity: 0; transition: opacity 0.2s;
    }
    .swagger-ui section.models .model-container::before {
      top: -1px; right: -1px; border-top: 1px solid; border-right: 1px solid;
    }
    .swagger-ui section.models .model-container::after {
      bottom: -1px; right: -1px; border-bottom: 1px solid; border-right: 1px solid;
    }
    .swagger-ui section.models .model-container:hover::before,
    .swagger-ui section.models .model-container:hover::after,
    .swagger-ui section.models .model-container.active::before,
    .swagger-ui section.models .model-container.active::after { opacity: 0.7; }

    /* -- schema title (BulkHeartbeatData, CommitReport, etc.) -- */
    .swagger-ui section.models .model-box {
      background: transparent !important;
      padding: 8px 12px !important;
    }
    .swagger-ui .model-title {
      color: var(--bx-amber) !important;
      font-family: 'Chakra Petch', sans-serif !important;
      font-weight: 600 !important;
      font-size: 14px !important;
      letter-spacing: 0.08em; text-transform: uppercase;
    }
    .swagger-ui .model-title__text { color: var(--bx-amber) !important; }
    .swagger-ui .model-toggle {
      color: var(--bx-crimson) !important;
      transition: transform 0.2s;
    }
    .swagger-ui .model-toggle::after {
      background-image: none !important;
      content: '▸'; color: var(--bx-crimson); font-size: 10px;
      display: inline-block; transition: transform 0.2s;
    }
    .swagger-ui .model-toggle.collapsed::after { transform: rotate(0deg); }
    .swagger-ui .model-toggle:not(.collapsed)::after { transform: rotate(90deg); }

    /* -- schema body: the expanded property tree -- */
    .swagger-ui .model {
      background: transparent !important;
      color: var(--bx-fg) !important;
      font-family: 'JetBrains Mono', monospace !important;
      font-size: 12px !important; line-height: 1.55 !important;
    }
    .swagger-ui .model-box .model {
      padding: 4px 0 !important;
    }
    /* delimiter chars: {, }, [, ] — subtle punctuation */
    .swagger-ui .model .brace-open,
    .swagger-ui .model .brace-close {
      color: var(--bx-fg-muted) !important; font-weight: 400;
    }

    /* -- property rows -- */
    .swagger-ui .model .property {
      color: var(--bx-fg) !important;
      padding: 1px 0 !important;
    }
    .swagger-ui .model .property.primitive {
      color: var(--bx-cyan) !important;
    }
    .swagger-ui .prop-name {
      color: var(--bx-fg) !important;
      font-weight: 500 !important;
    }
    .swagger-ui .property-row td:first-child { padding-right: 14px; }

    /* -- type / format badges (string, integer($int64), etc.) -- */
    .swagger-ui .prop-type {
      color: var(--bx-amber) !important;
      background: rgba(245,166,35,0.08);
      border: 1px solid rgba(245,166,35,0.35);
      padding: 1px 6px; margin-left: 4px;
      font-family: 'JetBrains Mono', monospace; font-size: 11px;
      display: inline-block; letter-spacing: 0.03em;
    }
    .swagger-ui .prop-format {
      color: var(--bx-cyan) !important;
      background: rgba(53,200,255,0.08);
      border: 1px solid rgba(53,200,255,0.35);
      padding: 1px 6px; margin-left: 4px;
      font-family: 'JetBrains Mono', monospace; font-size: 11px;
      display: inline-block;
    }
    /* enum values */
    .swagger-ui .model .property .prop-enum {
      color: var(--bx-purple) !important;
    }
    /* the "required" star on required properties */
    .swagger-ui .model .property-row .star {
      color: var(--bx-crimson) !important;
      font-size: 14px; font-weight: 700;
    }

    /* -- nested indent + tree-line guides -- */
    /* Swagger's model DOM is <table> with property-row rows; nesting is
       via <table> inside <td>. We add a left tree-line to the nested
       tables so hierarchy reads at a glance. */
    .swagger-ui .model .inner-object {
      border-left: 1px dashed rgba(255,42,91,0.2);
      margin-left: 2px; padding-left: 12px !important;
    }
    .swagger-ui .model table {
      border-collapse: collapse; margin: 0;
    }
    .swagger-ui .model table tr:hover td {
      background: rgba(255,42,91,0.04) !important;
    }
    .swagger-ui .model table td {
      padding: 1px 8px 1px 0 !important;
      vertical-align: top;
      border: none !important;
    }
    /* the toggle chevrons inside nested objects */
    .swagger-ui .model .model-toggle,
    .swagger-ui .model .expand-methods {
      cursor: pointer;
    }

    /* -- description text on schemas -- */
    .swagger-ui .model .property .prop-desc,
    .swagger-ui .model .markdown p {
      color: var(--bx-fg-muted) !important;
      font-style: normal !important;
      font-size: 11px !important;
    }

    /* -- example blocks embedded in schemas -- */
    .swagger-ui .model .prop-default,
    .swagger-ui .model-example {
      color: var(--bx-fg) !important;
      background: rgba(255,255,255,0.02);
      padding: 4px 8px; border-left: 2px solid var(--bx-line);
    }

    /* Backward-compat: some Swagger versions use .model-box for the
       schema container without .model-container. Style both. */
    .swagger-ui section.models > .model-box {
      background: var(--bx-bg) !important;
      border: 1px solid var(--bx-line) !important;
      border-left: 2px solid var(--bx-amber) !important;
      margin: 6px 0 !important;
    }

    /* misc odds and ends */
    .swagger-ui .expand-methods svg,
    .swagger-ui .expand-operation svg { fill: var(--bx-crimson) !important; }
    .swagger-ui .arrow { fill: var(--bx-crimson) !important; }
    .swagger-ui label { color: var(--bx-amber) !important; font-family: 'Chakra Petch', sans-serif; letter-spacing: 0.06em; }
    .swagger-ui .response-control-media-type__title,
    .swagger-ui .response-control-media-type__accept-message { color: var(--bx-fg-muted) !important; }
    .swagger-ui .opblock-summary-control:focus { outline-color: var(--bx-crimson); }
    .swagger-ui .loading-container .loading::after { color: var(--bx-crimson); }
    .swagger-ui .no-margin { color: var(--bx-fg); }
    .swagger-ui .btn-group .btn { border-radius: 0 !important; }
    .swagger-ui hr { border-color: var(--bx-line) !important; }
    .swagger-ui .markdown code, .swagger-ui .renderedMarkdown code {
      background: var(--bx-elev) !important; color: var(--bx-amber) !important;
      border: 1px solid var(--bx-line);
    }
    /* auth lock icons */
    .swagger-ui .authorization__btn svg { fill: var(--bx-fg-muted); }
    .swagger-ui .authorization__btn.locked svg,
    .swagger-ui .authorization__btn.unlocked svg { fill: var(--bx-crimson); }
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
  `;
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
  // The access token returned by POST /auth/refresh_token is already the
  // exact wire value the server expects after "Basic " — it's stored as
  // base64(uuid) at mint time (see internal/handler/auth.go mkTokenData)
  // and looked up by SHA256 of that same string. btoa()ing it here would
  // send double-base64, which never matches the stored hash → 403.
  const basicHeader = (token) => 'Basic ' + token;

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
    .then(resp => {
      // Endpoint returns {data: {fullName, email, isAdmin, ...}} — fullName
      // IS the username in this codebase. Fall back defensively so a
      // future shape change doesn't render "undefined".
      const username = resp?.data?.fullName || resp?.data?.username || resp?.username || 'operator';
      renderAuthedFab({ username, isAdmin: !!resp?.data?.isAdmin });
    })
    .catch(() => fabRebuild([el('a', { class: 'boom-fab-primary', href: '/' }, '▸ sign in to mint tokens')]));

  function renderAuthedFab(user) {
    const chip = el('div', { id: 'boom-auth-chip' }, '● ' + user.username + (user.isAdmin ? ' · admin' : ''));
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
    const sha = (h.commit && h.commit !== 'dev') ? h.commit.slice(0, 7) : null;
    const rev = h.buildTime ? h.buildTime.slice(0, 10) : null;
    // Don't render "dev/dev" placeholders — show a meaningful stamp or none.
    let text;
    if (sha && rev) text = '▓ FILE #' + sha + ' · REV ' + rev;
    else if (sha) text = '▓ FILE #' + sha;
    else if (h.version && h.version !== 'dev') text = '▓ v' + h.version;
    else text = '▓ BOOMTIME · DEV BUILD';
    const chip = el('div', { id: 'boom-version-chip' }, text);
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
