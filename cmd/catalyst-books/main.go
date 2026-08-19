// Command catalyst-books is the STANDALONE catalyst-books server (gaka-zp2s P4) —
// the books domain running in isolation, buildable as its own image. It wires only
// internal/books + internal/shared (+ the job runner); it imports ZERO of the
// boomtime code domain, which is the whole point of the split: `go build ./cmd/
// catalyst-books` compiles a lean binary with none of the wakatime analytics stack.
//
// Self-hosted, single-tenant: there is NO auth stack — no login, no OIDC, no
// tokens, no users-model. One FIXED owner (BOOM_STANDALONE_OWNER, default "owner")
// is pinned at boot via auth.SetStandaloneOwner, and apihelpers.Identify* returns
// a synthetic all-caps Identity for it without any credential lookup. The database
// is a FRESH, books-only Postgres schema (internal/books/db.MigrationsFS) — no
// wakatime / stats / code tables, and no full users model — applied via
// db.MigrateURLFS (NOT the host's default MigrateURL).
//
// The SAME internal/books packages also mount into the full boomtime host (via the
// god-type handler + server), so books runs both standalone AND embedded, unchanged.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	booksapi "github.com/TheBranchDriftCatalyst/boomtime/internal/books/api"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	booksdb "github.com/TheBranchDriftCatalyst/boomtime/internal/books/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()
	ctx := context.Background()

	// Books is the whole app here — force the domain on regardless of the env gate.
	cfg.FeatureBooks = true

	// Single fixed owner, no auth. Pin it so apihelpers.Identify* resolves every
	// caller to this owner with a synthetic all-caps Identity — no tokens/cookies.
	owner := os.Getenv("BOOM_STANDALONE_OWNER")
	if owner == "" {
		owner = "owner"
	}
	auth.SetStandaloneOwner(owner)

	// Apply the BOOKS-ONLY schema (FK-stripped, no users/wakatime/stats tables) via
	// the caller-supplied FS variant — the host's default MigrateURL is untouched.
	if err := db.MigrateURLFS(ctx, cfg.DatabaseURL(), booksdb.MigrationsFS); err != nil {
		logger.Error("migrations failed", "err", err)
		os.Exit(1)
	}
	database, err := db.New(ctx, cfg.DatabaseURL())
	if err != nil {
		logger.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	// Seed the single owner row so the books DAL's credential/monitor UPDATEs
	// (amazon_device / hardcover_token / reading_monitor, keyed by username) have a
	// row to write. Idempotent — a restart is a no-op. Owner name is configurable,
	// so this is seeded here rather than baked into the migration.
	if _, err := database.Pool.Exec(ctx,
		`INSERT INTO users (username) VALUES ($1) ON CONFLICT (username) DO NOTHING`, owner,
	); err != nil {
		logger.Error("owner seed failed", "err", err, "owner", owner)
		os.Exit(1)
	}

	e := echo.New()
	e.Use(middleware.Recover())

	// Liveness/readiness for k8s probes: 200 with {"status":"ok"} when the DB is
	// reachable, else 503 {"status":"degraded"} — no auth (matches the whole app).
	e.GET("/healthz", func(c *echo.Context) error {
		status, code := "ok", http.StatusOK
		if err := database.Pool.Ping(c.Request().Context()); err != nil {
			status, code = "degraded", http.StatusServiceUnavailable
		}
		return c.JSON(code, map[string]string{"status": status})
	})

	h := booksapi.New(database, cfg, logger)
	// Inline Hardcover push for the per-row sync button (nil-safe without it).
	h.SetHardcoverPush(hardcover.NewPushService(database, hardcover.NewStore(database), logger))
	booksapi.Register(e, h)

	port := cfg.Port
	if port == 0 {
		port = 8080
	}
	addr := ":" + strconv.Itoa(port)
	logger.Info("catalyst-books standalone listening", "addr", addr, "owner", owner)
	if err := e.Start(addr); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}
