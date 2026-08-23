// testhelpers_test.go — boomtime-admin copy of the external ginkgo test helpers
// (testutilTokenData) + local router builders that stand up the moved label-images /
// import admin surface (boom-zp2s). The internal/admin copies stay put for the
// admin-package tests that remain there.
package admin_test

import (
	"io"
	"log/slog"

	"github.com/labstack/echo/v5"

	boomtimeadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// discardLogger is a silent slog logger for handlers under test.
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// boomtimeRouterH builds a boomtime-admin handler (from the harness DB + Cfg) wired to
// an echo router carrying its full surface (label-images cluster + public label-image
// GET + wakatime.com import cluster). Returns the handler too so tests can late-wire the
// label-images worker / image-job queue before exercising the routes.
func boomtimeRouterH(hz *testutil.Harness) (*echo.Echo, *boomtimeadmin.Handler) {
	e := echo.New()
	h := boomtimeadmin.New(hz.DB, hz.Cfg, discardLogger())
	boomtimeadmin.Register(e, h)
	return e, h
}

// boomtimeRouter is boomtimeRouterH when the test doesn't need the handle.
func boomtimeRouter(hz *testutil.Harness) *echo.Echo {
	e, _ := boomtimeRouterH(hz)
	return e
}

// testutilTokenData builds a db.TokenData with a throwaway access token + the supplied
// refresh token — a valid refresh_token cookie without the public Login flow.
func testutilTokenData(user, refresh string) db.TokenData {
	return db.TokenData{Owner: user, Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RefreshToken: refresh}
}
