// Package ingest owns the write-path for boomtime telemetry: heartbeats
// (single + bulk), workouts (single + bulk), health samples (single +
// bulk), and the read-side inspector endpoints that live alongside them —
// heartbeats latest / group / list (audit views for the Explorer) and the
// Entity Explorer (list + REDACT).
//
// Extracted from internal/handler/ as part of boom-8tn phase 5a. Domain
// scope covers ONLY the ingest write surface and its adjacent explorer
// reads; broader dashboard / stats / rollup rebuild helpers stay put.
//
// SECURITY POSTURE: every write endpoint here binds JSON under a
// bounded MaxBytesReader cap (apihelpers.BodyLimitLarge for the batched
// telemetry writes; apihelpers.BodyLimitMedium for the redact body) so
// an authenticated hostile client cannot OOM the process by streaming
// a multi-GB body. The RedactEntities endpoint requires an explicit
// ?confirm=redact-entities sentinel — the belt-and-braces guard already
// used by the DB restore endpoint — so a stray fetch cannot scrub rows.
//
// Shared helpers live in internal/apihelpers/ — this package imports
// that instead of carrying per-file shims (boom-8tn phase 8 collapse).
package ingest

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// Handler bundles the SUBSET of the god-type handler.Handler's
// dependencies that the ingest domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every heartbeat / workout / health_sample / entity read + write
//   - Cfg    — RemoteWrite forwarding target (best-effort mirror of ingest)
//   - Logger — persistent-store failure log lines, remote-write debug lines,
//     goal invalidation warnings
//   - Cache  — busted on the workouts / health-samples / entity-redact write
//     paths so the Wellness/Explorer cards pick up new state on next fetch
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Cache  *cache.TTL

	// hbIngested is the process-lifetime count of heartbeats successfully stored,
	// used only to sample the "heartbeats ingested" narration line (see
	// sampler.go). Atomic — the ingest handler is a shared singleton hit
	// concurrently. Zero value is correct (no init needed in New).
	hbIngested atomic.Int64
}

// New constructs an ingest.Handler with the passed-in shared deps. Every
// field is required in production; nil-checks are the caller's responsibility
// (the god-type's New wires all four unconditionally).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
	}
}

// httpClient is the shared outbound HTTP client for the ingest package's
// remote-write forwarder (heartbeats.go remoteWrite). A dedicated
// timeout avoids http.DefaultClient's unbounded default which would hang
// the forwarder goroutine if the upstream locks up.
//
// EXPOSED as a package-level var so ingest-side tests can swap it out
// via SwapHTTPClientForTest. Not exported directly — tests use the
// SwapHTTPClientForTest seam to keep the mutation site auditable.
var httpClient = &http.Client{Timeout: 15 * time.Second, Transport: metrics.InstrumentTransport(nil)}

// SwapHTTPClientForTest replaces the package-level httpClient (used by
// remoteWrite) and returns a restore func the caller MUST defer. Test-only
// seam — production code never calls this.
func SwapHTTPClientForTest(c *http.Client) func() {
	prev := httpClient
	httpClient = c
	return func() { httpClient = prev }
}
