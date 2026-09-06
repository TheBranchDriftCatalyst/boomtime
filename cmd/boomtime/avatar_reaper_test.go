// avatar_reaper_test.go — DB-backed regression test for the boot-time avatar
// reaper (audit 2026-09-06, composition-config medium; cmd/boomtime/main.go:277).
//
// The failure being pinned: avatar renders are an OFFLOADED jobs kind
// (jobReg.SetOffload(identity.AvatarRenderKind)), so the server reserves
// user_avatars.status='running' and enqueues the job, and KEDA scales a drain
// pod up to run it. That drain pod's boot used to run the reaper FIRST and flip
// the row it was about to render to 'error' — the FE showed a failed render
// while the render was proceeding.
//
// Runs against an ISOLATED database because ReapOrphanedAvatarRenders is a
// fleet-wide, unscoped UPDATE over user_avatars.
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func avatarReaperLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// avatarStatus reads the raw status column so the assertion is about what the
// FE poll would see, not about a helper's interpretation of it.
func avatarStatus(t *testing.T, database *db.DB, owner string) string {
	t.Helper()
	var status string
	if err := database.Pool.QueryRow(context.Background(),
		`SELECT status FROM user_avatars WHERE username = $1`, owner).Scan(&status); err != nil {
		t.Fatalf("read avatar status for %s: %v", owner, err)
	}
	return status
}

func TestReapOrphanedAvatarRenders_GuardsInFlightRenders(t *testing.T) {
	database := testutil.OpenIsolatedDB(t, "avatarreap")
	hz := testutil.NewHarnessWithDB(t, database)
	ctx := context.Background()
	jobStore := jobs.NewStore(database.Pool)
	logger := avatarReaperLogger()

	// Isolated DB persists between runs on the same machine, and the reaper's
	// UPDATE has no owner scoping — start from a clean avatar/jobs table so a
	// leftover row from a previous run can't decide the outcome.
	reset := func() {
		if _, err := database.Pool.Exec(ctx, `DELETE FROM user_avatars`); err != nil {
			t.Fatalf("clean user_avatars: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `DELETE FROM jobs`); err != nil {
			t.Fatalf("clean jobs: %v", err)
		}
	}

	// reserveRunning reproduces exactly what identity.RegenerateAvatar does on
	// the server before enqueueing the job.
	reserveRunning := func(owner string) {
		t.Helper()
		if err := database.SetAvatarStatus(ctx, owner, db.UserAvatarStatusRunning, ""); err != nil {
			t.Fatalf("reserve running for %s: %v", owner, err)
		}
	}

	t.Run("a KEDA drain pod does NOT reap the render it was spawned to run", func(t *testing.T) {
		reset()
		owner, _ := hz.MintUser("avreap_drain")
		reserveRunning(owner)
		if _, err := jobStore.Enqueue(ctx, identity.AvatarRenderKind, owner, []byte(`{}`), 1, time.Time{}); err != nil {
			t.Fatalf("enqueue avatar-render: %v", err)
		}

		// The drain pod: --role=worker + BOOM_JOBS_DRAIN=true (see
		// k8s/.../keda-scaledjob-jobs.yaml).
		cfg := &config.Config{Role: "worker", JobsDrain: true}
		reapOrphanedAvatarRenders(ctx, cfg, database, jobStore, logger)

		if got := avatarStatus(t, database, owner); got != string(db.UserAvatarStatusRunning) {
			t.Errorf("avatar status = %q, want %q — the drain pod reaped the very render it was scaled up to perform; "+
				"the FE shows 'render interrupted by a server restart' while the render proceeds", got, db.UserAvatarStatusRunning)
		}
	})

	t.Run("a rolling server pod does NOT reap a render whose job is still queued", func(t *testing.T) {
		reset()
		owner, _ := hz.MintUser("avreap_rolling")
		reserveRunning(owner)
		if _, err := jobStore.Enqueue(ctx, identity.AvatarRenderKind, owner, []byte(`{}`), 1, time.Time{}); err != nil {
			t.Fatalf("enqueue avatar-render: %v", err)
		}

		cfg := &config.Config{Role: "server"}
		reapOrphanedAvatarRenders(ctx, cfg, database, jobStore, logger)

		if got := avatarStatus(t, database, owner); got != string(db.UserAvatarStatusRunning) {
			t.Errorf("avatar status = %q, want %q — a second server pod booting mid-render "+
				"declared another pod's in-flight render dead", got, db.UserAvatarStatusRunning)
		}
	})

	t.Run("a genuinely orphaned row (no avatar-render job at all) IS still reaped", func(t *testing.T) {
		reset()
		owner, _ := hz.MintUser("avreap_orphan")
		reserveRunning(owner)
		// No job enqueued: this is the inline-goroutine/enqueue-never-happened
		// case the reaper exists for. The original behaviour must survive.
		cfg := &config.Config{Role: "all"}
		reapOrphanedAvatarRenders(ctx, cfg, database, jobStore, logger)

		if got := avatarStatus(t, database, owner); got != string(db.UserAvatarStatusError) {
			t.Errorf("avatar status = %q, want %q — the reaper stopped doing its job: "+
				"a row with no backing job would poll 'running' forever", got, db.UserAvatarStatusError)
		}
	})

	t.Run("an unrelated pending job kind does not disarm the reaper's role gate", func(t *testing.T) {
		reset()
		owner, _ := hz.MintUser("avreap_otherkind")
		reserveRunning(owner)
		// A label-image job pending must NOT protect an orphaned avatar row —
		// the liveness check is per-kind, not "any job anywhere".
		if _, err := jobStore.Enqueue(ctx, "label-image", "somelabel", []byte(`{}`), 1, time.Time{}); err != nil {
			t.Fatalf("enqueue label-image: %v", err)
		}
		cfg := &config.Config{Role: "all"}
		reapOrphanedAvatarRenders(ctx, cfg, database, jobStore, logger)

		if got := avatarStatus(t, database, owner); got != string(db.UserAvatarStatusError) {
			t.Errorf("avatar status = %q, want %q — the liveness guard is too broad; "+
				"any pending job of any kind now blocks the reaper", got, db.UserAvatarStatusError)
		}
	})
}
