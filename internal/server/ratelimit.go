// Package server: rate-limit middleware (gaka-jk6 / gaka-ddp / gaka-awh.1).
//
// Universal token-bucket middleware. Each request is routed to an endpoint
// group whose bucket parameters (rate + burst) come from tunable env vars
// with paranoid defaults. Buckets are keyed by (group + client-identity) so
// login floods can't drain the wakatime-probe budget and one abusive IP can't
// starve a well-behaved one.
//
// Design notes:
//
//   - Bucketing preference: the middleware first tries to resolve the caller
//     by their access token (auth.ParseAuthHeader → DB lookup). If that
//     succeeds we key on `user:<owner>` so authenticated abuse follows the
//     account, not the IP. When there is no token we fall back to
//     `ip:<echo.RealIP()>` — echo's RealIP already does the right thing on
//     RemoteAddr and only trusts X-Real-IP / X-Forwarded-For when the
//     framework is configured with a trust extractor, so we don't need to
//     re-implement header hardening here.
//
//   - Storage: in-memory sync.Map of *rate.Limiter with a lastSeen atomic
//     stamp for lazy TTL eviction. A background goroutine sweeps every 5m and
//     drops entries idle >15m. This does NOT scale horizontally — a follow-up
//     bead tracks redis-backed limiter work (see gaka-awh.2 or its successor
//     recorded at commit time).
//
//   - The middleware short-circuits (never touches the limiter map) for:
//   - OPTIONS preflight  — CORS middleware owns that flight
//   - GET /healthz       — kubelet probe MUST be fast + unbucketed
//
//   - Testing hook: BOOM_DISABLE_RATE_LIMIT=1 disables everything (returns a
//     pass-through middleware) and emits a startup WARN. The existing test
//     suite relies on hammering endpoints without hitting the limit.
//
// 429 response envelope matches what the FE already handles for other 4xx:
//
//	{"error":"rate limited","retryAfter":<seconds>}
//
// with a Retry-After header (seconds, per RFC 9110 §10.2.3) so browsers and
// well-behaved clients (curl, wakatime-cli) both get the hint.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
	"golang.org/x/time/rate"
)

// rateLimitDisableEnv gates the entire middleware for local dev and the Go
// test suite. When "1" the factory logs a WARN and returns a pass-through.
const rateLimitDisableEnv = "BOOM_DISABLE_RATE_LIMIT"

// bucketTTL is how long a limiter entry may sit idle before the cleanup
// sweep evicts it. 15m is long enough to survive a paused test loop but
// short enough to keep the map from growing unbounded on a public host.
const bucketTTL = 15 * time.Minute

// cleanupInterval controls how often the sweep goroutine runs.
const cleanupInterval = 5 * time.Minute

// endpointGroup enumerates the tunable buckets. Everything else lands in
// groupDefault. Kept as a string so log lines and debug endpoints can quote
// the group by name.
type endpointGroup string

const (
	groupAuthWrite     endpointGroup = "auth-write"
	groupWakatimeProbe endpointGroup = "wakatime-probe"
	groupDefault       endpointGroup = "default"
)

// bucketConfig captures the parameters for one group.
// Rate is tokens-per-window; Burst is the max in-flight burst (bucket size).
// We store per-second rate as a rate.Limit so the constructor is trivial.
type bucketConfig struct {
	Rate  rate.Limit
	Burst int
}

// rateLimiterEntry is stored in the sync.Map. lastSeen is nanoseconds since
// epoch (atomic so the sweeper can read it without a lock).
type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

// rateLimitStore is the per-group bucket registry. Buckets are keyed by
// "<identity>" (already namespaced by group via the outer sync.Map).
type rateLimitStore struct {
	// buckets: group → sync.Map[key] → *rateLimiterEntry
	buckets map[endpointGroup]*sync.Map
	configs map[endpointGroup]bucketConfig
	logger  *slog.Logger

	// userLookup is injected so tests don't need a DB. It returns the owner
	// for a given raw token, or empty string when unknown. Never returns an
	// error to the caller — callers just fall back to IP bucketing.
	userLookup func(c *echo.Context) string

	// stop closes on shutdown to end the cleanup goroutine (only used by
	// tests today; the production server is torn down with the whole
	// process).
	stop chan struct{}
}

// defaultBucketConfigs returns the baseline limits from the design brief.
// Overridable via env (BOOM_RATELIMIT_<GROUP>_RATE / _BURST) — see
// bucketFromEnv for the parser.
func defaultBucketConfigs() map[endpointGroup]bucketConfig {
	return map[endpointGroup]bucketConfig{
		// 10 requests per minute per IP — allows a few typos + 2FA fumble
		// but shuts down credential-stuffing sprays quickly.
		groupAuthWrite: {Rate: rate.Every(6 * time.Second), Burst: 10},
		// 5 requests per minute per USER — probes hit wakatime.com so
		// protect their budget too.
		groupWakatimeProbe: {Rate: rate.Every(12 * time.Second), Burst: 5},
		// 60 requests per second per IP — generous, only stops runaway
		// loops and outright DoS.
		groupDefault: {Rate: 60, Burst: 60},
	}
}

// bucketFromEnv returns the (possibly env-overridden) config for a group.
// Uses BOOM_RATELIMIT_<GROUP>_RATE (tokens per second, float) and
// BOOM_RATELIMIT_<GROUP>_BURST (int) if set. Malformed values are silently
// dropped (the default wins) and a WARN is logged so operators notice.
func bucketFromEnv(group endpointGroup, def bucketConfig, logger *slog.Logger) bucketConfig {
	envBase := "BOOM_RATELIMIT_" + strings.ToUpper(strings.ReplaceAll(string(group), "-", "_"))
	if raw := os.Getenv(envBase + "_RATE"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			def.Rate = rate.Limit(v)
		} else if logger != nil {
			logger.Warn("rate-limit env var invalid — using default",
				"env", envBase+"_RATE", "value", raw)
		}
	}
	if raw := os.Getenv(envBase + "_BURST"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			def.Burst = v
		} else if logger != nil {
			logger.Warn("rate-limit env var invalid — using default",
				"env", envBase+"_BURST", "value", raw)
		}
	}
	return def
}

// newRateLimitStore wires the buckets, seeded from defaults + env overrides,
// and kicks off the background cleanup sweep.
func newRateLimitStore(logger *slog.Logger, userLookup func(c *echo.Context) string) *rateLimitStore {
	cfg := defaultBucketConfigs()
	for g, def := range cfg {
		cfg[g] = bucketFromEnv(g, def, logger)
	}
	s := &rateLimitStore{
		buckets:    make(map[endpointGroup]*sync.Map, len(cfg)),
		configs:    cfg,
		logger:     logger,
		userLookup: userLookup,
		stop:       make(chan struct{}),
	}
	for g := range cfg {
		s.buckets[g] = &sync.Map{}
	}
	go s.cleanupLoop()
	return s
}

// limiterFor returns (or lazily creates) the *rate.Limiter for the given
// group + key. The lastSeen stamp is refreshed on every call so the sweeper
// leaves active buckets alone.
func (s *rateLimitStore) limiterFor(group endpointGroup, key string) *rate.Limiter {
	m, ok := s.buckets[group]
	if !ok {
		// Unknown group — shouldn't happen, but be defensive: use default.
		m = s.buckets[groupDefault]
		group = groupDefault
	}
	if v, ok := m.Load(key); ok {
		entry := v.(*rateLimiterEntry)
		entry.lastSeen.Store(time.Now().UnixNano())
		return entry.limiter
	}
	cfg := s.configs[group]
	entry := &rateLimiterEntry{limiter: rate.NewLimiter(cfg.Rate, cfg.Burst)}
	entry.lastSeen.Store(time.Now().UnixNano())
	actual, _ := m.LoadOrStore(key, entry)
	stored := actual.(*rateLimiterEntry)
	stored.lastSeen.Store(time.Now().UnixNano())
	return stored.limiter
}

// cleanupLoop evicts entries idle > bucketTTL every cleanupInterval. Runs
// until s.stop is closed. Deliberately non-blocking: uses sync.Map.Range and
// deletes stale entries in-place.
func (s *rateLimitStore) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.evictOlderThan(now.Add(-bucketTTL))
		}
	}
}

// evictOlderThan is separated out so tests can drive cleanup deterministically.
func (s *rateLimitStore) evictOlderThan(cutoff time.Time) {
	cutoffNs := cutoff.UnixNano()
	for _, m := range s.buckets {
		m.Range(func(k, v any) bool {
			entry := v.(*rateLimiterEntry)
			if entry.lastSeen.Load() < cutoffNs {
				m.Delete(k)
			}
			return true
		})
	}
}

// classifyEndpoint decides which bucket a request goes into. Path matching
// is exact on the canonical route paths — avoids surprises with trailing
// slashes and query strings.
func classifyEndpoint(method, path string) endpointGroup {
	switch path {
	case "/auth/login",
		"/auth/register",
		"/auth/refresh_token":
		return groupAuthWrite
	case "/api/v1/users/current/password":
		if method == http.MethodPost {
			return groupAuthWrite
		}
	case "/api/v1/users/current/wakatime_key":
		if method == http.MethodPost {
			return groupWakatimeProbe
		}
	}
	return groupDefault
}

// bucketKey returns the identity string for the (group, request) pair. For
// wakatime-probe we DEMAND a resolved user (auth is enforced by the handler
// too, but we prefer to key on user so multi-IP abuse from one account still
// hits the same bucket). If the lookup fails we fall back to IP so we never
// silently disable the limit.
func (s *rateLimitStore) bucketKey(c *echo.Context, group endpointGroup) string {
	if group == groupWakatimeProbe {
		if owner := s.userLookup(c); owner != "" {
			return "user:" + owner
		}
	}
	if owner := s.userLookup(c); owner != "" {
		return "user:" + owner
	}
	return "ip:" + c.RealIP()
}

// installRateLimit installs the middleware on the echo instance and returns
// the store (for tests / future admin endpoints). When the disable env is
// set, it installs a no-op middleware and returns nil.
func installRateLimit(e *echo.Echo, logger *slog.Logger, database *db.DB) *rateLimitStore {
	if os.Getenv(rateLimitDisableEnv) == "1" {
		if logger != nil {
			logger.Warn("rate limiting DISABLED via "+rateLimitDisableEnv,
				"impact", "no throttling on any endpoint",
				"remediation", "unset "+rateLimitDisableEnv+" in production")
		}
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return next
		})
		return nil
	}
	lookup := userLookupFromDB(database)
	store := newRateLimitStore(logger, lookup)
	e.Use(store.middleware())
	if logger != nil {
		for g, cfg := range store.configs {
			logger.Info("rate-limit bucket configured",
				"group", string(g),
				"rate_per_sec", float64(cfg.Rate),
				"burst", cfg.Burst)
		}
	}
	return store
}

// userLookupFromDB is the production wiring: parse the Authorization header,
// look the token up, return the owner (or "" on any miss). Errors are
// swallowed — we only care whether the caller is authenticated for bucketing
// purposes; the actual auth check is enforced downstream by the handler.
func userLookupFromDB(database *db.DB) func(c *echo.Context) string {
	if database == nil {
		return func(*echo.Context) string { return "" }
	}
	return func(c *echo.Context) string {
		tkn, ok := auth.ParseAuthHeader(c.Request().Header.Get(echo.HeaderAuthorization))
		if !ok || tkn == "" {
			return ""
		}
		owner, ok, err := database.GetUserByToken(c.Request().Context(), tkn)
		if err != nil || !ok {
			return ""
		}
		return owner
	}
}

// middleware returns the echo middleware that consults the store.
func (s *rateLimitStore) middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			// Cheap bypasses first — kubelet + preflight must be zero-cost.
			if req.Method == http.MethodOptions {
				return next(c)
			}
			if req.Method == http.MethodGet && req.URL.Path == "/healthz" {
				return next(c)
			}
			group := classifyEndpoint(req.Method, req.URL.Path)
			key := s.bucketKey(c, group)
			limiter := s.limiterFor(group, key)
			if !limiter.Allow() {
				// Compute a Retry-After from the reservation delay.
				res := limiter.Reserve()
				delay := res.Delay()
				// We don't intend to actually consume this reservation.
				res.Cancel()
				retrySec := int(delay.Round(time.Second) / time.Second)
				if retrySec < 1 {
					retrySec = 1
				}
				return writeRateLimited(c, retrySec)
			}
			return next(c)
		}
	}
}

// writeRateLimited emits the 429 envelope + Retry-After header.
func writeRateLimited(c *echo.Context, retryAfterSec int) error {
	c.Response().Header().Set("Retry-After", strconv.Itoa(retryAfterSec))
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusTooManyRequests)
	// json.Encode is fine here — the payload is tiny and never fails.
	return json.NewEncoder(c.Response()).Encode(map[string]any{
		"error":      "rate limited",
		"retryAfter": retryAfterSec,
	})
}
