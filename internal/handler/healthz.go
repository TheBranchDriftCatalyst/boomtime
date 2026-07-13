// Public unauthenticated health endpoint. Serves the operator / watchdog case
// (uptime probes, k8s liveness/readiness, "what version is running out there").
//
// Response body is INTENTIONALLY non-sensitive: version, branch, commit, build
// time, uptime, DB reachability, and current migration version. No secrets, no
// per-user data, no request paths — safe to expose on the public internet.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

// HealthzResponse is the JSON shape returned by GET /healthz. Fields are
// omitempty for the string metadata so an un-stamped `go run` build returns a
// clean payload; DB fields always render (their zero values are meaningful).
type HealthzResponse struct {
	Status         string `json:"status"`
	Version        string `json:"version"`
	Branch         string `json:"branch,omitempty"`
	Commit         string `json:"commit,omitempty"`
	BuildTime      string `json:"buildTime,omitempty"`
	StartedAt      string `json:"startedAt"`
	UptimeSeconds  int64  `json:"uptimeSeconds"`
	DBReachable    bool   `json:"dbReachable"`
	SchemaVersion  int64  `json:"schemaVersion"`
}

// Healthz: GET /healthz — unauthenticated, always JSON. Returns 200 with a
// body describing the running instance. DB probes use a short timeout so the
// endpoint stays responsive even if the pool is saturated. status is "ok"
// when the DB is reachable, "degraded" otherwise (still HTTP 200 — k8s probes
// treat the payload as diagnostic; use the standard non-2xx path if we ever
// want the DB outage to fail readiness).
func (h *Handler) Healthz(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()

	dbOK := true
	if err := h.DB.Pool.Ping(ctx); err != nil {
		dbOK = false
	}

	var schemaVer int64
	if dbOK {
		if v, err := h.DB.SchemaVersion(ctx); err == nil {
			schemaVer = v
		}
	}

	now := time.Now()
	status := "ok"
	if !dbOK {
		status = "degraded"
	}

	return c.JSON(http.StatusOK, HealthzResponse{
		Status:        status,
		Version:       h.Cfg.Version,
		Branch:        h.Cfg.Branch,
		Commit:        h.Cfg.Commit,
		BuildTime:     h.Cfg.BuildTime,
		StartedAt:     h.StartTime.UTC().Format(time.RFC3339),
		UptimeSeconds: int64(now.Sub(h.StartTime).Seconds()),
		DBReachable:   dbOK,
		SchemaVersion: schemaVer,
	})
}
