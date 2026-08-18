// Command catalyst-books is the STANDALONE catalyst-books server (gaka-zp2s P4) —
// the books domain running in isolation, buildable as its own image. It wires only
// internal/books + internal/shared (+ the job runner); it imports ZERO of the
// boomtime code domain, which is the whole point of the split: `go build ./cmd/
// catalyst-books` compiles a lean binary with none of the wakatime analytics stack.
//
// Self-hosted, single-tenant: auth is the shared token layer (Authorization: Bearer
// <token> resolved against auth_tokens by apihelpers.Identify), so no identity HTTP
// handlers (login/OIDC) are needed — provision a user + token out of band. This is
// the "minimal identity" the standalone-mode plan calls for.
//
// The SAME internal/books packages also mount into the full boomtime host (via the
// god-type handler + server), so books runs both standalone AND embedded, unchanged.
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	booksapi "github.com/TheBranchDriftCatalyst/boomtime/internal/books/api"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()
	ctx := context.Background()

	// Books is the whole app here — force the domain on regardless of the env gate.
	cfg.FeatureBooks = true

	if err := db.MigrateURL(ctx, cfg.DatabaseURL()); err != nil {
		logger.Error("migrations failed", "err", err)
		os.Exit(1)
	}
	database, err := db.New(ctx, cfg.DatabaseURL())
	if err != nil {
		logger.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	e := echo.New()
	e.Use(middleware.Recover())

	h := booksapi.New(database, cfg, logger)
	// Inline Hardcover push for the per-row sync button (nil-safe without it).
	h.SetHardcoverPush(hardcover.NewPushService(database, hardcover.NewStore(database), logger))
	booksapi.Register(e, h)

	port := cfg.Port
	if port == 0 {
		port = 8080
	}
	addr := ":" + strconv.Itoa(port)
	logger.Info("catalyst-books standalone listening", "addr", addr)
	if err := e.Start(addr); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}
