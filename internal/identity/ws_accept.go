// ws_accept.go — one place that decides how the identity domain's push streams
// (jobs_ws.go, notify_ws.go) upgrade to a WebSocket.
package identity

import "github.com/coder/websocket"

// wsAcceptOptions returns the AcceptOptions both push streams upgrade with.
//
// Both used to pass `InsecureSkipVerify: true, // same-origin` — a comment that
// asserts the exact opposite of what the flag does. InsecureSkipVerify DISABLES
// coder/websocket's Origin verification, so ANY origin could complete the
// handshake. The refresh_token cookie is SameSite=Strict, which blunts a
// classic cross-site WebSocket hijack, but SameSite treats SUBDOMAINS of the
// same registrable domain as same-site: a page served by any sibling app under
// the shared parent domain (the homelab layout — many services under one
// domain) could open wss://boomtime…/api/v1/{jobs,notify}/ws, have the browser
// attach the victim's cookie, and stream their events.
//
// Leaving InsecureSkipVerify unset restores the library default: the Origin
// header's host must equal the request's Host. Behind Traefik (passHostHeader
// on) the browser's Origin and Host match, so production is unaffected.
//
// OUTSIDE production we deliberately keep the check off. vite's dev proxy sets
// changeOrigin:true, which rewrites Host to the backend (localhost:8080) while
// leaving Origin on the vite port (localhost:5173) — a permanent mismatch that
// a strict check would turn into a broken dev + Playwright stack for no
// security gain on loopback. BOOM_ENV defaults to "prod" (config.Load), so an
// unconfigured deployment is strict by default and only an explicit
// dev/test env opts out.
func wsAcceptOptions(isProd bool) *websocket.AcceptOptions {
	return &websocket.AcceptOptions{InsecureSkipVerify: !isProd}
}
