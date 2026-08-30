package server

import (
	"context"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/domainreg"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/handler"
)

type stubEnqueuer struct{}

func (stubEnqueuer) Enqueue(context.Context, string, []byte, ...jobs.EnqueueOption) (int64, error) {
	return 1, nil
}

// THE PRODUCTION BUG THIS PINS (c638283, live for ~1 day):
//
// catalyst modules are stateful across registration — books.Module.RegisterRoutes
// stashes `m.h = booksapi.New(...)` so the composition root can late-wire a jobs
// enqueuer onto it once the provider exists. DocumentationRouter used to take the
// LIVE registry, so it ran RegisterRoutes a SECOND time on the same module
// instances and replaced that stashed handler with one built from the
// documentation fixture.
//
// The enqueuer then landed on the fixture handler while the live routes still
// held method values bound to the original. Every background-job enqueue answered
// "background jobs are not available on this server" — Hardcover match/pull,
// Kindle and Audible sync/backfill, liberation, sync-all — while the jobs page
// rendered perfectly, because that reads through a different handler entirely.
//
// The fix is structural (DocumentationRouter builds its own registry, so the live
// one cannot be passed). This asserts the property that fix exists to protect.
func TestDocumentationRouterDoesNotClobberLiveModuleWiring(t *testing.T) {
	live := domainreg.Build()

	// Stand up the live surface exactly as the composition root does, then
	// late-wire the enqueuer — the real ordering.
	registerRoutes(echo.New(), &handler.Handler{
		DB:  &db.DB{},
		Cfg: &config.Config{FeatureBooks: true},
	}, live.Registry)
	live.Books.WireJobEnqueuer(stubEnqueuer{})

	if !live.Books.HasJobEnqueuer() {
		t.Fatal("precondition failed: the enqueuer did not land on the live books handler at all")
	}

	// Building the documentation router must not disturb any of that.
	_ = DocumentationRouter()

	if !live.Books.HasJobEnqueuer() {
		t.Fatal("building the documentation router DISCARDED the live books handler's jobs enqueuer — " +
			"every background-job enqueue would answer 'background jobs are not available on this server' " +
			"while the jobs page still looks healthy")
	}
}
