// Package server: CORS allowlist config isolated from server wiring.
//
// gaka-n5r: the previous implementation used UnsafeAllowOriginFunc to reflect
// ANY Origin back with Access-Control-Allow-Credentials=true. An attacker page
// on evil.example.com could then read credentialed responses from a victim's
// local boomtime instance (login response body leaks the fresh access token).
//
// This file parses BOOM_CORS_ALLOWED_ORIGINS (comma-separated origins) into an
// exact-match allowlist. The match is case-sensitive on scheme+host+port — no
// suffix matching, no scheme downgrade, no port wildcarding. Empty / whitespace
// / malformed entries are dropped with a WARN so a fat-fingered env var doesn't
// silently open the door.
//
// The prod-vs-dev gate lives in cmd/boomtime (reuses isProdEnv). Here we just
// return the parsed allowlist + a bool telling the caller whether it fell back
// to dev defaults.
package server

import (
	"log/slog"
	"net/url"
	"strings"
)

// defaultDevAllowedOrigins is the fallback used when BOOM_CORS_ALLOWED_ORIGINS
// is unset in a dev/test environment. Covers the Vite dev server (5173) and
// the boomtime backend serving its own embedded SPA (8080).
var defaultDevAllowedOrigins = []string{
	"http://localhost:5173",
	"http://localhost:8080",
}

// parseAllowedOrigins splits the raw BOOM_CORS_ALLOWED_ORIGINS value on commas,
// trims whitespace, drops empty and structurally-invalid entries (must parse as
// scheme+host, must not carry path/query/fragment). Returns the cleaned list.
//
// The logger receives a WARN for each dropped entry so operators can spot typos.
func parseAllowedOrigins(raw string, logger *slog.Logger) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		if err := validateOrigin(origin); err != nil {
			if logger != nil {
				logger.Warn("BOOM_CORS_ALLOWED_ORIGINS: dropping invalid entry",
					"entry", origin, "err", err.Error())
			}
			continue
		}
		out = append(out, origin)
	}
	return out
}

// validateOrigin returns an error if s is not a well-formed origin per RFC 6454
// (scheme + "://" + host + optional ":" + port, nothing else). Anything with a
// path, query, or fragment is rejected — those are common footguns that
// masquerade as valid entries (e.g. `http://example.com/`).
func validateOrigin(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme == "" || u.Host == "" {
		return errInvalidOrigin
	}
	if u.Path != "" && u.Path != "/" {
		return errInvalidOrigin
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errInvalidOrigin
	}
	// Normalize: reject trailing slash by reconstructing scheme://host[:port].
	// If the user wrote `http://example.com/`, u.Path == "/" and reconstructing
	// gives us the canonical origin, which we then require to match the input.
	canonical := u.Scheme + "://" + u.Host
	if s != canonical {
		return errInvalidOrigin
	}
	return nil
}

// isOriginAllowed reports whether origin exactly matches any entry in
// allowlist. Case-sensitive on all three components (scheme, host, port) so a
// `http` allowlist entry does NOT authorize an `HTTPS` origin and vice versa.
// Empty origin (browser sent no Origin header, or `null`) is never allowed.
func isOriginAllowed(origin string, allowlist []string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	for _, allowed := range allowlist {
		if origin == allowed {
			return true
		}
	}
	return false
}

// errInvalidOrigin is a sentinel returned by validateOrigin for any structural
// problem with the origin string. We keep it opaque (no origin echo) because
// the value is copied into a WARN log and we don't want to encourage grepping
// for user input in structured logs.
var errInvalidOrigin = &invalidOriginError{}

type invalidOriginError struct{}

func (*invalidOriginError) Error() string {
	return "origin must be scheme://host[:port] with no path/query/fragment/userinfo"
}
