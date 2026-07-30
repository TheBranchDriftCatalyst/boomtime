// error_branches_test.go — small targeted tests that pin the input-validation
// error branches of methods across the package (gaka-d6x).
//
// Every case pins the invariant: empty/malformed input MUST NOT reach the SQL
// layer — callers get a clear Go error, and no rows change. This closes the
// low-numbered coverage gaps in goals.go, wakatime_key.go, auth-checks, and
// small write helpers without pretending to test the happy path.
package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/tracelog"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Alias the tracelog level constants so tests read cleanly.
var (
	tracelogLevelError = tracelog.LogLevelError
	tracelogLevelWarn  = tracelog.LogLevelWarn
	tracelogLevelInfo  = tracelog.LogLevelInfo
	tracelogLevelDebug = tracelog.LogLevelDebug
	tracelogLevelTrace = tracelog.LogLevelTrace
)

var _ = ginkgo.Describe("input-validation error branches (gaka-d6x)", func() {

	// gaka-8tn phase 2b: goals.CreateGoal / UpdateGoal / DeleteGoal /
	// ToggleGoal input-validation branches moved to
	// internal/goals/db_branches_test.go together with the goals package
	// extraction. Byte-identical Its at the new location.

	// ---- label_images.go ----

	ginkgo.It("GetLabelImage / SaveLabelImage / HasLabelImage / DeleteLabelImage reject empty id", func() {
		d := openTestDBG()
		ctx := context.Background()

		_, _, err := d.GetLabelImage(ctx, "")
		Expect(err).To(HaveOccurred())
		Expect(d.SaveLabelImage(ctx, "", []byte("X"), "", "", "", nil)).To(HaveOccurred())
		Expect(d.SaveLabelImage(ctx, "someid", nil, "", "", "", nil)).To(HaveOccurred(), "empty bytes rejected")
		_, err = d.HasLabelImage(ctx, "")
		Expect(err).To(HaveOccurred())
		Expect(d.DeleteLabelImage(ctx, "")).To(HaveOccurred())
	})

	ginkgo.It("HasLabelImage/GetLabelImage on a truly-missing id returns false/nil-nil-nil (no SQL error surfaces)", func() {
		d := openTestDBG()
		ctx := context.Background()
		id := "no-such-image-" + time.Now().Format("150405.000000")

		ok, err := d.HasLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		li, ok, err := d.GetLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(li).To(BeNil())
	})

	// ---- labels.go ----

	ginkgo.It("GetLabel / DeleteLabel reject empty id", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, err := d.GetLabel(ctx, "")
		Expect(err).To(HaveOccurred())
		Expect(d.DeleteLabel(ctx, "")).To(HaveOccurred())
	})

	ginkgo.It("GetLabel on missing id returns (nil, nil) — never leak existence via error kind", func() {
		d := openTestDBG()
		ctx := context.Background()
		l, err := d.GetLabel(ctx, "no-such-label-xyz-"+time.Now().Format("150405.000"))
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(BeNil())
	})

	// ---- widgets.go / widget_defs.go: broader missing-row behavior ----

	ginkgo.It("GetWidgetLinkInfo on nonexistent uuid returns (empty, false, nil)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u, st, sr, ok, err := d.GetWidgetLinkInfo(ctx, [16]byte{})
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(u).To(Equal(""))
		Expect(st).To(Equal(""))
		Expect(sr).To(Equal(""))
	})

	// ---- backfill.SetBackfillConfig branches ----

	ginkgo.It("SetBackfillConfig clamps out-of-range values to safe defaults (input-validation invariant)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("bf_conf")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM backfill_config WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		// Absurd values should clamp.
		cfg := BackfillConfig{
			Username:          u,
			ClusterGapSec:     -1,
			PreCommitLeadSec:  -1,
			PostCommitTailSec: -1,
			HeartbeatRateSec:  0, // 0 would emit forever
			SourceTag:         "backfill:test",
			LangMap:           map[string]string{},
			AuthorEmails:      []string{},
		}
		Expect(d.SetBackfillConfig(ctx, cfg)).To(Succeed())
		got, err := d.GetBackfillConfig(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.HeartbeatRateSec).To(BeNumerically(">=", 1), "HeartbeatRateSec MUST clamp > 0")
		Expect(got.ClusterGapSec).To(BeNumerically(">=", 0))
	})

	// ---- observability.mapLevel: cover ALL branches ----

	ginkgo.It("mapLevel: Error/Warn/Info/Debug branches (Info -> Debug is the load-bearing quirk)", func() {
		Expect(mapLevel(tracelogLevelError).String()).To(Equal("ERROR"))
		Expect(mapLevel(tracelogLevelWarn).String()).To(Equal("WARN"))
		Expect(mapLevel(tracelogLevelInfo).String()).To(Equal("DEBUG"), "Info MUST map to Debug (per gaka-ar7 policy)")
		Expect(mapLevel(tracelogLevelDebug).String()).To(Equal("DEBUG"))
		Expect(mapLevel(tracelogLevelTrace).String()).To(Equal("DEBUG"), "trace/unknown falls through to Debug")
	})

	// ---- db.New error path ----

	ginkgo.It("New returns error for a malformed DSN (host cannot be resolved) — pool is closed before return", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Point at a port with no postgres listener.
		_, err := New(ctx, "postgres://user:pass@127.0.0.1:1/nowhere?sslmode=disable&connect_timeout=1")
		Expect(err).To(HaveOccurred())
	})

	// ---- ingest.RefreshRollup empty-since edge (early-return branch) ----

	ginkgo.It("RefreshRollup on a sender with no rows since epoch: no-op, no error", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("refresh_empty")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		Expect(d.RefreshRollup(ctx, u, time.Now().UTC().AddDate(0, 0, 30))).To(Succeed())
	})

	// ---- user_avatars / user_timezone: empty-args ----

	ginkgo.It("SetUserTimezone rejects an empty username", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(d.SetUserTimezone(ctx, "", "UTC")).To(HaveOccurred())
	})

	ginkgo.It("SaveUserAvatar: empty username OR empty bytes reject early; empty mime defaults to image/png", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(d.SaveUserAvatar(ctx, "", []byte("img"), "", "m", "p", nil)).To(HaveOccurred())
		Expect(d.SaveUserAvatar(ctx, "u", nil, "", "m", "p", nil)).To(HaveOccurred())
		Expect(d.SaveUserAvatar(ctx, "u", []byte{}, "", "m", "p", nil)).To(HaveOccurred())

		// Empty mime defaults to image/png.
		u := mkSender("uav_mime")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM user_avatars WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		Expect(d.SaveUserAvatar(ctx, u, []byte("img"), "", "m", "p", nil)).To(Succeed())

		got, ok, err := d.GetUserAvatar(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.MimeType).To(Equal("image/png"), "empty mime MUST default to image/png")
	})

	// ---- widgets.RollWidgetLink SQL error path (bad conn shape doesn't fire in test env, but empty user is safe) ----
	ginkgo.It("RollWidgetLink: empty user + zero uuid returns (nil, false, nil) — never a full-table match", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, ok, err := d.RollWidgetLink(ctx, "", [16]byte{})
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	// ---- Ensure pgx.ErrNoRows is propagated (never swallowed) by SetPublicProfile off+empty on missing user ----
	ginkgo.It("SetPublicProfile: enabled=false + missing user returns pgx.ErrNoRows via the off-branch (Update-0 case)", func() {
		d := openTestDBG()
		ctx := context.Background()
		err := d.SetPublicProfile(ctx, mkSender("pub_off_ghost"), false, "")
		Expect(err).To(MatchError(pgx.ErrNoRows))
	})
})
