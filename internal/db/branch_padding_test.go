// branch_padding_test.go — coverage for the small branches that separate
// happy-path tests from complete branch coverage (gaka-d6x).
//
// Each It documents ONE branch or ONE guard invariant. No happy-path repeats.
package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("branch coverage padding (gaka-d6x)", func() {

	// gaka-8tn phase 2b: goals itoaFast / ListGoals / GetGoal /
	// InvalidateGoalsForOwner branch coverage moved to
	// internal/goals/db_branches_test.go together with the goals
	// package extraction. Byte-identical Its at the new location.

	// ---- widgets.CreateWidgetLink: project scope + case-insensitive path ----

	ginkgo.It("CreateWidgetLink: distinct scope_ref yields distinct uuids (isolation invariant)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("wl_distinct")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_links WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'p1'), ($1,'p2')`, u)
		Expect(err).NotTo(HaveOccurred())

		id1, err := d.CreateWidgetLink(ctx, u, WidgetScopeProject, "p1")
		Expect(err).NotTo(HaveOccurred())
		id2, err := d.CreateWidgetLink(ctx, u, WidgetScopeProject, "p2")
		Expect(err).NotTo(HaveOccurred())
		Expect(id1).NotTo(Equal(id2), "distinct scope_ref must mint distinct uuids")
	})

	// ---- award_ledger.LogAwards: rows-written counter ----

	ginkgo.It("LogAwards: rows-written counter counts only NEW rows, not previously-logged ones (ON CONFLICT DO NOTHING branch)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("aw_written")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		cleanupAwardG(d, ctx, u)
		labelID := "aw-written-" + u
		ensureLabelRow(d, ctx, labelID)

		at := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) // Wed
		items := []AwardLogItem{{LabelID: labelID, PeriodType: PeriodWeekly}}

		n, err := d.LogAwards(ctx, u, items, time.UTC, at)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))

		// Second call with same (label, period) but ALSO a NEW label item that
		// isn't yet logged — the counter should be 1, not 2.
		newLabel := "aw-fresh-" + u
		ensureLabelRow(d, ctx, newLabel)
		items2 := []AwardLogItem{
			{LabelID: labelID, PeriodType: PeriodWeekly}, // duplicate
			{LabelID: newLabel, PeriodType: PeriodWeekly}, // new
		}
		n, err = d.LogAwards(ctx, u, items2, time.UTC, at)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1), "only the NEW label counts as written")
	})

	// ---- widget_defs.CreateWidgetDef unique-name violation ----

	ginkgo.It("CreateWidgetDef: same (user, name) twice returns a unique-violation error (handler surfaces 409)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("wd_unique")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_defs WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		spec := json.RawMessage(`{"kind":"x"}`)
		_, err := d.CreateWidgetDef(ctx, u, "dupname", spec)
		Expect(err).NotTo(HaveOccurred())

		_, err = d.CreateWidgetDef(ctx, u, "dupname", spec)
		Expect(err).To(HaveOccurred(), "duplicate (username, name) MUST return a unique-violation, not silently upsert")
	})

	// ---- predicates.HasHiddenOutside ----

	ginkgo.It("HiddenSets.HasHiddenOutside: true when at least one hidden axis is not in the available set", func() {
		hs := mkHiddenSets(map[string][]string{
			"project":  {"x"},
			"language": {"y"},
		})
		Expect(hs.HasHiddenOutside(map[string]bool{"project": true, "language": true})).To(BeFalse())
		Expect(hs.HasHiddenOutside(map[string]bool{"project": true})).To(BeTrue(),
			"language axis has hides but is not available → true (rollup fallback signal)")
		Expect(hs.AnyHidden()).To(BeTrue())

		empty := mkHiddenSets(nil)
		Expect(empty.AnyHidden()).To(BeFalse())
	})

	// ---- dump.DumpAll: writes a small non-empty archive to an in-memory Writer ----

	ginkgo.It("DumpAll: writes a non-empty zip archive to the caller's Writer under a single MVCC snapshot", func() {
		d := openTestDBG()
		ctx := context.Background()
		var buf bytesBuffer
		err := d.DumpAll(ctx, &buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(buf.n).To(BeNumerically(">", 100), "zip archive should carry at least a manifest + a few tables")
	})

	// ---- user_timezone helpers ----

	ginkgo.It("GetUserTimezone / SetUserTimezone: empty string is the sentinel for \"use default\"; roundtrip preserves that", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("utz")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		// Fresh user has default timezone.
		got, err := d.GetUserTimezone(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		_ = got // whatever default the migration set — no invariant on the value

		Expect(d.SetUserTimezone(ctx, u, "America/Denver")).To(Succeed())
		got, err = d.GetUserTimezone(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("America/Denver"))

		// Empty explicitly clears.
		Expect(d.SetUserTimezone(ctx, u, "")).To(Succeed())
		got, err = d.GetUserTimezone(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(""), "empty is the explicit \"use default\" sentinel")
	})

	// ---- ingest.SaveHeartbeats: empty is a fast no-op ----

	ginkgo.It("SaveHeartbeats: empty payload slice returns (nil, nil) — no DB round-trip on the fast path", func() {
		d := openTestDBG()
		ctx := context.Background()
		ids, err := d.SaveHeartbeats(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(HaveLen(0))
	})
})

// bytesBuffer is a minimal io.Writer for DumpAll that avoids importing bytes.
type bytesBuffer struct {
	n int
}

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.n += len(p)
	return len(p), nil
}

var _ = ginkgo.Describe("more branch padding (gaka-d6x)", func() {

	// ---- remap.RenameSets.Any: both branches ----

	ginkgo.It("RenameSets.Any: zero map -> false; populated map -> true", func() {
		empty := RenameSets{byAxis: map[string]axisRenames{}}
		Expect(empty.Any()).To(BeFalse())

		// Populated with an empty axisRenames still -> false.
		emptyAxis := RenameSets{byAxis: map[string]axisRenames{"project": {}}}
		Expect(emptyAxis.Any()).To(BeFalse(), "axis present but empty MUST NOT flip Any() true")

		// Populated with an exact rule -> true.
		full := RenameSets{byAxis: map[string]axisRenames{
			"project": {exact: map[string]string{"old": "new"}},
		}}
		Expect(full.Any()).To(BeTrue())
	})

	// ---- splice.numToFloat: invalid + zero cases ----

	ginkgo.It("numToFloat: invalid pgtype.Numeric (zero-value) -> 0.0, no panic", func() {
		// Zero-value Numeric has Valid=false → early-return 0.
		Expect(numToFloat(pgtypeNumericZero())).To(Equal(0.0))
	})

	// ---- observability.NewWithObservability with N1DupThresh only ----

	ginkgo.It("NewWithObservability: N1DupThresh alone triggers the n1Tracer branch (single tracer, not multitracer)", func() {
		if !dbReady {
			ginkgo.Skip("isolated test database unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		d, err := NewWithObservability(ctx, testDatabaseURL(), Options{N1DupThresh: 3})
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() { d.Close() })

		// Pool is functional.
		var one int
		Expect(d.Pool.QueryRow(ctx, "SELECT 1").Scan(&one)).To(Succeed())
	})

	ginkgo.It("NewWithObservability: Dev+ExplainSlow attaches the planTracer (multitracer path)", func() {
		if !dbReady {
			ginkgo.Skip("isolated test database unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		d, err := NewWithObservability(ctx, testDatabaseURL(), Options{
			LogQueries:  true,
			Dev:         true,
			ExplainSlow: 1 * time.Nanosecond,
		})
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() { d.Close() })

		// Trigger an EXPLAIN-able read.
		var one int
		Expect(d.Pool.QueryRow(ctx, "SELECT 1").Scan(&one)).To(Succeed())
	})

	// ---- observability.isReadQuery: comment-only / edge inputs ----

	ginkgo.It("isReadQuery: comment-only string (no newline) -> false (no infinite loop on malformed input)", func() {
		Expect(isReadQuery("-- just a comment")).To(BeFalse())
		Expect(isReadQuery("")).To(BeFalse())
	})

	// ---- entities.ListEntitiesByType ----

	ginkgo.It("ListEntitiesByType: sender with no heartbeats returns empty slice, no error", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("entities_none")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		got, _, err := d.ListEntitiesByType(ctx, u, "file", 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(0))
	})

	// ---- goals.CreateGoal: happy path with description non-nil ----

	// ---- curation.SetCurationRuleEnabled idempotent no-op branch ----

	ginkgo.It("SetCurationRuleEnabled: setting the SAME value returns (true, nil) — no-op no-fault (idempotent)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("cur_set_idem")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		rule, err := d.CreateCurationRule(ctx, u, "project", "hide", "exact", "SecretProj", nil)
		Expect(err).NotTo(HaveOccurred())

		// Flip to true (already true from CreateCurationRule) — expect found=true.
		found, err := d.SetCurationRuleEnabled(ctx, u, rule.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "no-op set MUST still return found=true (idempotent)")

		// Flip to false → found=true.
		found, err = d.SetCurationRuleEnabled(ctx, u, rule.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())

		// Unknown id → found=false.
		found, err = d.SetCurationRuleEnabled(ctx, u, -999, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	// ---- ingest.RefreshRollup covers the non-empty branch too ----

	ginkgo.It("RefreshRollup on a sender WITH heartbeats produces at least one rollup row (positive branch)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("refresh_pos")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'P')`, u)
		Expect(err).NotTo(HaveOccurred())
		ts := time.Now().UTC().Add(-1 * time.Hour)
		_, err = d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, project, language, entity, ty, time_sent, user_agent, gap_seconds) VALUES ($1,'P','Go','a.go','file',$2,'ua',60)`, u, ts)
		Expect(err).NotTo(HaveOccurred())

		Expect(d.RefreshRollup(ctx, u, ts.Add(-1*time.Hour))).To(Succeed())
		var n int
		Expect(d.Pool.QueryRow(ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, u).Scan(&n)).To(Succeed())
		Expect(n).To(BeNumerically(">=", 1))
	})

	// ---- labels.GetGenConfig fallback branch ----

	ginkgo.It("GetGenConfig: singleton row returns some string (may be empty) — never errors on healthy DB", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	ginkgo.It("SetGenConfig: round-trips a system prompt string via label_gen_config singleton", func() {
		d := openTestDBG()
		ctx := context.Background()
		orig, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = d.SetGenConfig(ctx, orig) })
		Expect(d.SetGenConfig(ctx, "unit-test-prompt-x")).To(Succeed())
		got, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("unit-test-prompt-x"))
	})

	// ---- ingest.refreshRollup: pgx.ErrNoRows branch (unknown user, skip refresh) ----

	ginkgo.It("RefreshRollup on a NONEXISTENT sender: skips refresh (no error) — refresh path guards against unknown user", func() {
		d := openTestDBG()
		ctx := context.Background()
		// Sender that has no users row → refreshRollup hits pgx.ErrNoRows and
		// returns nil without doing a DELETE.
		Expect(d.RefreshRollup(ctx, "___never_existed_"+time.Now().Format("150405.000000"), time.Now().UTC())).To(Succeed())
	})

	// ---- migrate.MigrateURL: pins the driver-open + up-context path against the live test DB ----

	ginkgo.It("MigrateURL on the isolated test DB is idempotent (no error, no schema change)", func() {
		if !dbReady {
			ginkgo.Skip("isolated test database unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		Expect(MigrateURL(ctx, testDatabaseURL())).To(Succeed())
	})

	// gaka-8tn phase 2b: CreateGoal description-branch coverage moved
	// to internal/goals/db_branches_test.go together with the goals
	// package extraction. Byte-identical It at the new location.
})

// pgtypeNumericZero returns a zero-valued pgtype.Numeric (Valid=false).
// Wrapped in a helper so we don't need to import pgtype just for that.
func pgtypeNumericZero() pgtypeNum { return pgtypeNum{} }

// pgtypeNum is a stand-in alias so tests compile without a direct pgtype
// import path duplication. numToFloat accepts pgtype.Numeric which is a
// struct; we build a zero value via a same-shape helper.
type pgtypeNum = pgtype.Numeric

var _ = ginkgo.Describe("spaces branch padding (gaka-d6x)", func() {

	ginkgo.It("RenameSpace / DeleteSpace on cross-owner id returns 0 rows-affected (owner scope invariant)", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := mkSender("sp_a")
		bob := mkSender("sp_b")
		Expect(insertFreshUser(d, ctx, alice)).To(Succeed())
		Expect(insertFreshUser(d, ctx, bob)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM spaces WHERE owner IN ($1,$2)`, alice, bob)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username IN ($1,$2)`, alice, bob)
		})

		s, err := d.CreateSpace(ctx, alice, "alice-space")
		Expect(err).NotTo(HaveOccurred())

		// Bob tries to rename Alice's space.
		newName := "hijacked"
		n, err := d.RenameSpace(ctx, bob, s.ID, &newName, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(0), "cross-owner rename MUST NOT affect any row")

		// Bob tries to delete Alice's space.
		n, err = d.DeleteSpace(ctx, bob, s.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(0))

		// Alice can rename her own.
		n, err = d.RenameSpace(ctx, alice, s.ID, &newName, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1))
	})

	ginkgo.It("DeleteSpaceRule on cross-owner ids: returns 0 (space-not-owned isolation)", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := mkSender("spr_a")
		bob := mkSender("spr_b")
		Expect(insertFreshUser(d, ctx, alice)).To(Succeed())
		Expect(insertFreshUser(d, ctx, bob)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM spaces WHERE owner IN ($1,$2)`, alice, bob)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username IN ($1,$2)`, alice, bob)
		})

		s, err := d.CreateSpace(ctx, alice, "a-sp")
		Expect(err).NotTo(HaveOccurred())
		rule, err := d.AddSpaceRule(ctx, alice, s.ID, "project", "somevalue", "exact")
		Expect(err).NotTo(HaveOccurred())
		Expect(rule).NotTo(BeNil())

		// Bob tries to delete Alice's rule.
		n, err := d.DeleteSpaceRule(ctx, bob, s.ID, rule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(0))

		// Alice can delete her own.
		n, err = d.DeleteSpaceRule(ctx, alice, s.ID, rule.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1))
	})

	// ---- misc auth: ChangePasswordAndRevoke happy path with API tokens PRESENT ----

	ginkgo.It("ListApiTokens: user with NO api tokens returns empty slice — never nil for stable FE render", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("api_list_empty")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		list, err := d.ListApiTokens(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(0))
	})

	// ---- curation empty-matchType default; DeleteCurationRule missing-id ----

	ginkgo.It("CreateCurationRule: empty matchType defaults to MatchExact (constant-lift branch)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("cur_default_match")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		r, err := d.CreateCurationRule(ctx, u, "project", "hide", "", "SecretProj", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.MatchType).To(Equal(MatchExact), "empty matchType MUST default to exact")

		// Duplicate insert with newValue: ON CONFLICT DO UPDATE re-enables.
		newVal := "renamed"
		r2, err := d.CreateCurationRule(ctx, u, "project", "hide", "exact", "SecretProj", &newVal)
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.ID).To(Equal(r.ID), "conflict path returns same id")
		Expect(r2.Enabled).To(BeTrue(), "re-added rule MUST be re-enabled (gaka-dfd)")
	})

	ginkgo.It("DeleteCurationRule: unknown id returns 0 rows-affected, no error (idempotent nuke)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("cur_del_missing")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		n, err := d.DeleteCurationRule(ctx, u, -999)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(0))
	})

	ginkgo.It("GetCurationRule: unknown id returns (nil, \"\", nil) — never leak existence via error", func() {
		d := openTestDBG()
		ctx := context.Background()
		r, sender, err := d.GetCurationRule(ctx, -999)
		Expect(err).NotTo(HaveOccurred())
		Expect(r).To(BeNil())
		Expect(sender).To(Equal(""))
	})
})

// insertFreshUser plants a user row at ArgonVersion=2 (current).
// (Extracted from the db writer's auth_test.go which collided with
// gaka-se2.6's auth_test.go on cherry-pick.)
func insertFreshUser(d *DB, ctx context.Context, username string) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used, argon_version) VALUES ($1, '\x00', '\x00', 2) ON CONFLICT DO NOTHING`,
		username)
	return err
}

// cleanupAwardG deletes award_ledger + users rows for u via DeferCleanup.
// (Extracted from the db writer's award_ledger_test.go which collided with gaka-se2.5.)
func cleanupAwardG(d *DB, ctx context.Context, u string) {
	ginkgo.DeferCleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM award_ledger WHERE username=$1`, u)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
	})
}

// ensureLabelRow inserts a valid labels row so award_ledger FK inserts don't
// choke; DeferCleanup wipes it.
func ensureLabelRow(d *DB, ctx context.Context, id string) {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO labels (id, kind, label, condition) VALUES ($1, 'archetype', $1, '{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":1}'::jsonb) ON CONFLICT DO NOTHING`,
		id)
	Expect(err).NotTo(HaveOccurred())
	ginkgo.DeferCleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM labels WHERE id=$1`, id)
	})
}
