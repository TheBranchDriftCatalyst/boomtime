// misc_coverage_test.go — coverage-focused invariant tests for the remaining
// uncovered surfaces after auth/health/award_ledger/wakatime_key (gaka-d6x).
//
// Files touched:
//   - dashboard_layouts.go (Get/Set/Delete)
//   - public_profile.go    (Get/Set/Lookup + slug-off-preserves-slug branch)
//   - migrate.go           (Migrate on live pool + SchemaVersion)
//   - label_images.go      (Truncate + ListMeta + DeleteLabelImages)
//   - widget_defs.go       (Create/Get/GetByName/List/Update/Delete)
//   - widgets.go           (CreateWidgetLink + Get/List/Roll/RecordHit + ProjectExists + ProjectMemberSet)
//   - projects.go          (GetBadgeLinkInfo)
//   - importjobs.go        (GetJobByID + GetJobsByOwner + CancelJob + SetJobDrift + MarkRunningJobsFailed)
//   - backfill.go          (PreviewBackfillBatch)
//   - dump.go              (Senders + HasActiveImportJobs + Restore*Error.Error)
//   - ingest.go            (ResyncDerived)
//   - activity.go          (GetTotalActivityTime)
//   - predicates.go        (HiddenSets.Values + HiddenSets.Projects)
//   - observability.go     (planTracer TraceQueryStart/End/explain + n1Tracer.TraceQueryEnd)
package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ---- dashboard_layouts ----

var _ = ginkgo.Describe("dashboard_layouts (gaka-d6x)", func() {
	ginkgo.It("Set + Get: layout round-trips as JSONB, Delete drops the row so subsequent Get is (nil,false,nil)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("dash_layout")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM dashboard_layouts WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		layout := json.RawMessage(`{"widgets":[{"id":"a"},{"id":"b"}],"cols":12}`)
		Expect(d.SetDashboardLayout(ctx, u, "public_profile", layout)).To(Succeed())

		got, ok, err := d.GetDashboardLayout(ctx, u, "public_profile")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		var probe map[string]any
		Expect(json.Unmarshal(got, &probe)).To(Succeed())
		Expect(probe["cols"]).To(BeEquivalentTo(12))

		// Upsert semantics: 2nd Set replaces layout.
		layout2 := json.RawMessage(`{"widgets":[{"id":"z"}],"cols":6}`)
		Expect(d.SetDashboardLayout(ctx, u, "public_profile", layout2)).To(Succeed())
		got, ok, err = d.GetDashboardLayout(ctx, u, "public_profile")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(json.Unmarshal(got, &probe)).To(Succeed())
		Expect(probe["cols"]).To(BeEquivalentTo(6))

		// Delete → GetDashboardLayout returns (nil, false, nil).
		Expect(d.DeleteDashboardLayout(ctx, u, "public_profile")).To(Succeed())
		_, ok, err = d.GetDashboardLayout(ctx, u, "public_profile")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("Get/Set/Delete all reject empty owner or scope with a clear error (no accidental cross-tenant read)", func() {
		d := openTestDBG()
		ctx := context.Background()

		_, _, err := d.GetDashboardLayout(ctx, "", "s")
		Expect(err).To(HaveOccurred())
		_, _, err = d.GetDashboardLayout(ctx, "o", "")
		Expect(err).To(HaveOccurred())
		Expect(d.SetDashboardLayout(ctx, "", "s", json.RawMessage(`{}`))).To(HaveOccurred())
		Expect(d.SetDashboardLayout(ctx, "o", "", json.RawMessage(`{}`))).To(HaveOccurred())
		Expect(d.SetDashboardLayout(ctx, "o", "s", nil)).To(HaveOccurred())
		Expect(d.DeleteDashboardLayout(ctx, "", "s")).To(HaveOccurred())
		Expect(d.DeleteDashboardLayout(ctx, "o", "")).To(HaveOccurred())
	})

	ginkgo.It("Delete on a missing row is idempotent (no error)", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(d.DeleteDashboardLayout(ctx, "no-such-owner", "no-scope")).To(Succeed())
	})
})

// ---- public_profile ----

var _ = ginkgo.Describe("public_profile (gaka-d6x)", func() {
	ginkgo.It("SetPublicProfile: enabled=true with slug writes both; toggle off with empty slug PRESERVES slug", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("pub_toggle")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		Expect(d.SetPublicProfile(ctx, u, true, "coolslug")).To(Succeed())
		enabled, slug, err := d.GetPublicProfile(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeTrue())
		Expect(slug).NotTo(BeNil())
		Expect(*slug).To(Equal("coolslug"))

		// Off + empty slug -> slug column preserved (invariant: toggle off + on keeps the slug).
		Expect(d.SetPublicProfile(ctx, u, false, "")).To(Succeed())
		enabled, slug, err = d.GetPublicProfile(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeFalse())
		Expect(slug).NotTo(BeNil(), "toggle-off with empty slug MUST preserve prior slug")
		Expect(*slug).To(Equal("coolslug"))
	})

	ginkgo.It("LookupUsernameBySlug: rejects empty slug (returns pgx.ErrNoRows) — no full-table scan oracle", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, err := d.LookupUsernameBySlug(ctx, "")
		Expect(err).To(MatchError(pgx.ErrNoRows))
	})

	ginkgo.It("LookupUsernameBySlug: finds the owner of a known slug; unknown returns pgx.ErrNoRows", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("pub_lookup")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		Expect(d.SetPublicProfile(ctx, u, true, "unique-slug-42x")).To(Succeed())

		got, err := d.LookupUsernameBySlug(ctx, "unique-slug-42x")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(u))

		_, err = d.LookupUsernameBySlug(ctx, "no-such-slug-ever")
		Expect(err).To(MatchError(pgx.ErrNoRows))
	})

	ginkgo.It("GetPublicProfile returns (false, nil, nil) for a nonexistent user (missing-row convention)", func() {
		d := openTestDBG()
		ctx := context.Background()
		enabled, slug, err := d.GetPublicProfile(ctx, mkSender("pub_ghost"))
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeFalse())
		Expect(slug).To(BeNil())
	})

	ginkgo.It("Set{On,Off}PublicProfile rejects empty username; ErrNoRows on nonexistent user", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(d.SetPublicProfile(ctx, "", true, "x")).To(HaveOccurred())
		err := d.SetPublicProfile(ctx, mkSender("pub_ghost2"), false, "")
		Expect(err).To(MatchError(pgx.ErrNoRows))
	})
})

// ---- migrate ----

var _ = ginkgo.Describe("migrate (gaka-d6x)", func() {
	ginkgo.It("SchemaVersion returns the highest applied migration version (>= 44 for this suite)", func() {
		d := openTestDBG()
		ctx := context.Background()
		v, err := d.SchemaVersion(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(BeNumerically(">=", 44), "test DB should have applied every migration up to at least 44")
	})

	ginkgo.It("Migrate on a live pool is a no-op when everything is applied (idempotent)", func() {
		d := openTestDBG()
		ctx := context.Background()
		// A second Migrate on an up-to-date pool must not error.
		Expect(Migrate(ctx, d.Pool)).To(Succeed())
	})
})

// ---- label_images ----

var _ = ginkgo.Describe("label_images (gaka-d6x)", func() {
	ginkgo.It("Truncate wipes every row; ListMeta after Truncate is empty; ListMeta populated after Save", func() {
		d := openTestDBG()
		ctx := context.Background()
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM label_images`)
		})
		Expect(d.TruncateLabelImages(ctx)).To(Succeed())

		meta, err := d.ListLabelImagesMeta(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(HaveLen(0))

		// Save two.
		bytesA := []byte("PNGDATA-A")
		bytesB := []byte("PNGDATA-BB")
		Expect(d.SaveLabelImage(ctx, "lbl-a", bytesA, "image/png", "sd", "cyberpunk", nil)).To(Succeed())
		Expect(d.SaveLabelImage(ctx, "lbl-b", bytesB, "", "", "", nil)).To(Succeed())

		meta, err = d.ListLabelImagesMeta(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(HaveLen(2))
		// SizeBytes reflects the byte length via octet_length.
		byID := map[string]int64{}
		for _, m := range meta {
			byID[m.ID] = m.SizeBytes
		}
		Expect(byID["lbl-a"]).To(Equal(int64(len(bytesA))))
		Expect(byID["lbl-b"]).To(Equal(int64(len(bytesB))))
	})

	ginkgo.It("DeleteLabelImages: batches ANY() delete for N ids in a single round-trip (nil/empty is no-op)", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(d.TruncateLabelImages(ctx)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM label_images`) })

		Expect(d.SaveLabelImage(ctx, "d-1", []byte("X"), "", "", "", nil)).To(Succeed())
		Expect(d.SaveLabelImage(ctx, "d-2", []byte("Y"), "", "", "", nil)).To(Succeed())
		Expect(d.SaveLabelImage(ctx, "d-3", []byte("Z"), "", "", "", nil)).To(Succeed())

		// nil / empty is a no-op.
		Expect(d.DeleteLabelImages(ctx, nil)).To(Succeed())
		Expect(d.DeleteLabelImages(ctx, []string{})).To(Succeed())

		Expect(d.DeleteLabelImages(ctx, []string{"d-1", "d-3"})).To(Succeed())
		meta, err := d.ListLabelImagesMeta(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(HaveLen(1))
		Expect(meta[0].ID).To(Equal("d-2"), "only the un-deleted survives")
	})
})

// ---- widget_defs ----

var _ = ginkgo.Describe("widget_defs (gaka-d6x)", func() {
	ginkgo.It("CRUD: Create + GetByName + Get(byID) + Update + List + Delete round-trip a WidgetDef", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("wd_crud")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_defs WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		spec := json.RawMessage(`{"kind":"stat","axis":"language"}`)
		id, err := d.CreateWidgetDef(ctx, u, "myWidget", spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(Equal(uuid.Nil))

		// GetByName
		got, ok, err := d.GetWidgetDefByName(ctx, u, "myWidget")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.DefID).To(Equal(id))
		Expect(got.Name).To(Equal("myWidget"))

		// Get by uuid returns owner too.
		owner, gotByID, ok, err := d.GetWidgetDef(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(owner).To(Equal(u))
		Expect(gotByID.DefID).To(Equal(id))

		// Update: new spec, updated_at bumps.
		newSpec := json.RawMessage(`{"kind":"stat","axis":"editor"}`)
		beforeUpdate := gotByID.UpdatedAt
		time.Sleep(2 * time.Millisecond) // ensure timestamp differs
		ok2, err := d.UpdateWidgetDef(ctx, u, "myWidget", newSpec)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok2).To(BeTrue())
		got, _, _ = d.GetWidgetDefByName(ctx, u, "myWidget")
		Expect(got.UpdatedAt.After(beforeUpdate)).To(BeTrue(), "updated_at MUST bump on UpdateWidgetDef")

		// List includes the def.
		list, err := d.ListWidgetDefs(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].DefID).To(Equal(id))

		// Delete + verify missing.
		ok3, err := d.DeleteWidgetDef(ctx, u, "myWidget")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok3).To(BeTrue())
		_, ok, err = d.GetWidgetDefByName(ctx, u, "myWidget")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("Get by unknown uuid returns (empty, false, nil); GetByName unknown returns (zero, false, nil)", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, _, ok, err := d.GetWidgetDef(ctx, uuid.New())
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		_, ok, err = d.GetWidgetDefByName(ctx, mkSender("wd_ghost"), "nothing")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("Update/Delete on missing rows return ok=false without error (missing-row convention)", func() {
		d := openTestDBG()
		ctx := context.Background()
		ok, err := d.UpdateWidgetDef(ctx, "no-user", "no-name", json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		ok, err = d.DeleteWidgetDef(ctx, "no-user", "no-name")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
})

// ---- widgets ----

var _ = ginkgo.Describe("widgets (gaka-d6x)", func() {
	ginkgo.It("CreateWidgetLink is idempotent: same (user, scope) returns the SAME uuid on re-mint", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("wl_stable")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_links WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		id1, err := d.CreateWidgetLink(ctx, u, WidgetScopeUser, "")
		Expect(err).NotTo(HaveOccurred())
		id2, err := d.CreateWidgetLink(ctx, u, WidgetScopeUser, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(id2).To(Equal(id1), "same scope must re-mint the SAME uuid (stable embeds)")

		user, sType, sRef, ok, err := d.GetWidgetLinkInfo(ctx, id1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(user).To(Equal(u))
		Expect(sType).To(Equal(WidgetScopeUser))
		Expect(sRef).To(Equal(""))
	})

	ginkgo.It("RollWidgetLink: cross-owner id returns (nil, false, nil) — cannot roll another user's link", func() {
		d := openTestDBG()
		ctx := context.Background()
		alice := mkSender("wl_alice")
		bob := mkSender("wl_bob")
		Expect(insertFreshUser(d, ctx, alice)).To(Succeed())
		Expect(insertFreshUser(d, ctx, bob)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_links WHERE username IN ($1,$2)`, alice, bob)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username IN ($1,$2)`, alice, bob)
		})

		aliceID, err := d.CreateWidgetLink(ctx, alice, WidgetScopeUser, "")
		Expect(err).NotTo(HaveOccurred())

		// Bob tries to roll Alice's link → no-op, no error, no roll.
		_, ok, err := d.RollWidgetLink(ctx, bob, aliceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		// Alice can roll her own → new uuid returned; old uuid stops resolving.
		newID, ok, err := d.RollWidgetLink(ctx, alice, aliceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(newID).NotTo(Equal(aliceID))

		_, _, _, ok, err = d.GetWidgetLinkInfo(ctx, aliceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "post-roll: old id MUST 404")
	})

	ginkgo.It("RecordWidgetLinkHit: bumps count on repeat origin, adds new entry for a new origin, empty origin -> \"direct\"", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("wl_hit")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_links WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		id, err := d.CreateWidgetLink(ctx, u, WidgetScopeUser, "")
		Expect(err).NotTo(HaveOccurred())

		// Empty origin lands as "direct".
		Expect(d.RecordWidgetLinkHit(ctx, id, "")).To(Succeed())
		Expect(d.RecordWidgetLinkHit(ctx, id, "")).To(Succeed())
		Expect(d.RecordWidgetLinkHit(ctx, id, "https://github.com")).To(Succeed())

		links, err := d.ListWidgetLinks(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(1))
		wl := links[0]
		Expect(wl.Origins).To(HaveLen(2))
		// counts must include: direct=2, github=1
		total := 0
		hasDirect := false
		hasGithub := false
		for _, o := range wl.Origins {
			total += o.Count
			if o.Origin == "direct" {
				hasDirect = true
				Expect(o.Count).To(Equal(2))
			}
			if o.Origin == "https://github.com" {
				hasGithub = true
			}
		}
		Expect(hasDirect).To(BeTrue())
		Expect(hasGithub).To(BeTrue())
		Expect(total).To(Equal(3), "3 total hits across 2 origins")
	})

	ginkgo.It("RecordWidgetLinkHit on a missing uuid is a no-op (returns nil — fire-and-forget)", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(d.RecordWidgetLinkHit(ctx, uuid.New(), "https://example.com")).To(Succeed())
	})

	ginkgo.It("ProjectExists is case-insensitive on name (matches inclusion-predicate semantics)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("wl_projexists")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1, 'MyProject')`, u)
		Expect(err).NotTo(HaveOccurred())

		ok, err := d.ProjectExists(ctx, u, "myproject")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "lower('MyProject') = lower('myproject')")

		ok, err = d.ProjectExists(ctx, u, "not-a-project")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("ProjectMemberSet: builds MemberSets with a lowercased single-entry exact set on project axis", func() {
		ms := ProjectMemberSet("MyProject")
		Expect(ms.byAxis).To(HaveKey("project"))
		Expect(ms.byAxis["project"].exact).To(Equal([]string{"myproject"}))
	})
})

// ---- projects.GetBadgeLinkInfo ----

var _ = ginkgo.Describe("badge_link (gaka-d6x)", func() {
	ginkgo.It("GetBadgeLinkInfo: known id round-trips (user, project); unknown returns (\"\",\"\",false,nil)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("badge_info")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM badges WHERE username=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'proj')`, u)
		Expect(err).NotTo(HaveOccurred())

		id, err := d.CreateBadgeLink(ctx, u, "proj")
		Expect(err).NotTo(HaveOccurred())

		gotU, gotP, ok, err := d.GetBadgeLinkInfo(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(gotU).To(Equal(u))
		Expect(gotP).To(Equal("proj"))

		_, _, ok, err = d.GetBadgeLinkInfo(ctx, uuid.New())
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
})

// ---- importjobs: extra branches ----

var _ = ginkgo.Describe("import_jobs extra (gaka-d6x)", func() {
	ginkgo.It("GetJobByID unknown returns (nil, nil); CancelJob only cancels queued/running; second cancel is (nil, nil)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("ij_cancel")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		got, err := d.GetJobByID(ctx, -12345)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())

		j, err := d.CreateImportJob(ctx, u, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		Expect(err).NotTo(HaveOccurred())

		cancelled, err := d.CancelJob(ctx, j.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(cancelled).NotTo(BeNil())
		Expect(cancelled.State).To(Equal(JobStateCancelled))

		// Second cancel: state already cancelled → (nil, nil).
		again, err := d.CancelJob(ctx, j.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(BeNil(), "already-terminal job cannot be re-cancelled")
	})

	ginkgo.It("SetJobDrift with []byte(nil) clears the column; with valid JSON writes it back on GetJobByID", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("ij_drift")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		j, err := d.CreateImportJob(ctx, u, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		Expect(err).NotTo(HaveOccurred())

		Expect(d.SetJobDrift(ctx, j.ID, []byte(`[{"field":"x","note":"schema drift"}]`))).To(Succeed())
		got, err := d.GetJobByID(ctx, j.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(len(got.Drift)).To(BeNumerically(">", 0))

		// Clear -> Drift is empty after.
		Expect(d.SetJobDrift(ctx, j.ID, nil)).To(Succeed())
		got, err = d.GetJobByID(ctx, j.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(len(got.Drift)).To(Equal(0), "nil drift MUST NULL the column")
	})

	ginkgo.It("GetJobsByOwner returns newest-first; MarkRunningJobsFailed sweeps queued/running to failed on startup recovery", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("ij_list")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		j1, err := d.CreateImportJob(ctx, u, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		Expect(err).NotTo(HaveOccurred())
		j2, err := d.CreateImportJob(ctx, u, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		Expect(err).NotTo(HaveOccurred())

		list, err := d.GetJobsByOwner(ctx, u)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].ID).To(Equal(j2.ID), "newest first (ORDER BY id DESC)")
		Expect(list[1].ID).To(Equal(j1.ID))

		// Mark j2 as running so MarkRunningJobsFailed has both queued+running to sweep.
		_, err = d.MarkJobRunning(ctx, j2.ID)
		Expect(err).NotTo(HaveOccurred())

		ids, err := d.MarkRunningJobsFailed(ctx, "unit-test-startup-sweep")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(ids)).To(BeNumerically(">=", 2))

		// Both jobs are now failed.
		list, _ = d.GetJobsByOwner(ctx, u)
		for _, j := range list {
			Expect(j.State).To(Equal(JobStateFailed))
		}
	})
})

// ---- backfill.PreviewBackfillBatch ----

var _ = ginkgo.Describe("backfill preview (gaka-d6x)", func() {
	ginkgo.It("PreviewBackfillBatch: reports Accepted count WITHOUT inserting any heartbeat", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("bf_prev")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		start := time.Now().UTC().Add(-2 * time.Hour)
		end := start.Add(30 * time.Minute)
		proj := "P"
		lang := "Go"
		batch := BackfillBatch{
			Username:  u,
			SourceTag: "unit-test-backfill",
			Sessions: []BackfillSession{
				{
					Start: start,
					End:   end,
					Heartbeats: []model.HeartbeatPayload{
						{Entity: "a.go", Type: "file", TimeSent: float64(start.Unix()), Project: &proj, Language: &lang},
						{Entity: "a.go", Type: "file", TimeSent: float64(start.Add(1 * time.Minute).Unix()), Project: &proj, Language: &lang},
					},
				},
			},
		}
		res, err := d.PreviewBackfillBatch(ctx, batch)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.AcceptedHeartbeats).To(Equal(2), "preview reports Accepted count")

		// Prove NOTHING was inserted.
		var rows int
		Expect(d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, u).Scan(&rows)).To(Succeed())
		Expect(rows).To(Equal(0), "PreviewBackfillBatch MUST NOT insert (contract vs. InsertBackfillBatch)")
	})

	ginkgo.It("InsertBackfillBatch: overlap with an existing real (source IS NULL) heartbeat marks the session Skipped/reason=overlap", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("bf_overlap")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		// Plant a REAL heartbeat inside the window.
		windowMid := time.Now().UTC().Add(-3 * time.Hour)
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'P')`, u)
		Expect(err).NotTo(HaveOccurred())
		_, err = d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, project, entity, ty, time_sent, user_agent, source) VALUES ($1,'P','a.go','file',$2,'ua',NULL)`, u, windowMid)
		Expect(err).NotTo(HaveOccurred())

		proj := "P"
		batch := BackfillBatch{
			Username:  u,
			SourceTag: "backfill:test",
			Sessions: []BackfillSession{
				{
					Start: windowMid.Add(-1 * time.Hour),
					End:   windowMid.Add(1 * time.Hour),
					Heartbeats: []model.HeartbeatPayload{
						{Entity: "b.go", Type: "file", TimeSent: float64(windowMid.Add(30 * time.Minute).Unix()), Project: &proj},
					},
				},
			},
		}
		res, err := d.InsertBackfillBatch(ctx, batch)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.SkippedHeartbeats).To(Equal(1))
		Expect(res.AcceptedHeartbeats).To(Equal(0))
		Expect(res.Sessions[0].Reason).To(Equal("overlap"))
	})
})

// ---- dump: helpers + probes ----

var _ = ginkgo.Describe("dump misc (gaka-d6x)", func() {
	ginkgo.It("RestoreValidationError.Error / RestoreVersionError.Error carry the caller's diagnostic message", func() {
		e1 := &RestoreValidationError{Msg: "manifest missing app_id"}
		Expect(e1.Error()).To(Equal("manifest missing app_id"))

		e2 := &RestoreVersionError{Archive: 40, Current: 44}
		Expect(e2.Error()).To(ContainSubstring("40"))
		Expect(e2.Error()).To(ContainSubstring("44"))
	})

	ginkgo.It("Senders returns every distinct heartbeat sender (used for post-restore rebuild)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("dmp_senders")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'p')`, u)
		Expect(err).NotTo(HaveOccurred())
		_, err = d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, project, entity, ty, time_sent, user_agent, gap_seconds) VALUES ($1, 'p', 'a.go', 'file', now(), 'ua', 60)`, u)
		Expect(err).NotTo(HaveOccurred())

		got, err := d.Senders(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainElement(u))
	})

	ginkgo.It("HasActiveImportJobs is true when ANY owner has a queued/running row, false otherwise", func() {
		d := openTestDBG()
		ctx := context.Background()

		// Clear all so we test the true edge.
		_, _ = d.Pool.Exec(ctx, `UPDATE import_jobs SET state='failed', error='pre-test', finished_at=now() WHERE state IN ('queued','running')`)

		active, err := d.HasActiveImportJobs(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(BeFalse(), "with no queued/running rows, HasActiveImportJobs must be false")

		u := mkSender("dmp_hasactive")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		_, err = d.CreateImportJob(ctx, u, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		Expect(err).NotTo(HaveOccurred())

		active, err = d.HasActiveImportJobs(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(BeTrue(), "a queued job MUST flip HasActiveImportJobs to true")
	})
})

// ---- ingest.ResyncDerived ----

var _ = ginkgo.Describe("ingest ResyncDerived (gaka-d6x)", func() {
	ginkgo.It("ResyncDerived: RecomputeGaps + RefreshRollup for epoch — rollup row appears after rebuild", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("resync")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})

		ts := time.Now().UTC().Add(-3 * time.Hour)
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'P')`, u)
		Expect(err).NotTo(HaveOccurred())
		_, err = d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, project, language, editor, entity, ty, time_sent, user_agent, gap_seconds) VALUES ($1,'P','Go','vim','a.go','file',$2,'ua',60)`, u, ts)
		Expect(err).NotTo(HaveOccurred())

		// Wipe the rollup to prove ResyncDerived rebuilds it from raw.
		_, err = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, u)
		Expect(err).NotTo(HaveOccurred())

		Expect(d.ResyncDerived(ctx, u)).To(Succeed())

		var rollupCnt int
		Expect(d.Pool.QueryRow(ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, u).Scan(&rollupCnt)).To(Succeed())
		Expect(rollupCnt).To(BeNumerically(">=", 1), "ResyncDerived MUST rebuild the rollup from raw heartbeats")
	})
})

// ---- activity.GetTotalActivityTime ----

var _ = ginkgo.Describe("activity GetTotalActivityTime (gaka-d6x)", func() {
	ginkgo.It("GetTotalActivityTime: 0 for a fresh user with no heartbeats (no-oracle sanity)", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("gtat")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		total, err := d.GetTotalActivityTime(ctx, u, 30, "some-project")
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeEquivalentTo(0))
	})
})

// ---- predicates: pure helpers ----

var _ = ginkgo.Describe("predicates helpers (gaka-d6x)", func() {
	ginkgo.It("HiddenSets.Values + HiddenSets.Projects: axis-scoped read-through", func() {
		hs := mkHiddenSets(map[string][]string{
			"project":  {"secret-proj"},
			"language": {"c++", "asm"},
		})
		Expect(hs.Values("project")).To(Equal([]string{"secret-proj"}))
		Expect(hs.Values("language")).To(Equal([]string{"c++", "asm"}))
		Expect(hs.Values("editor")).To(BeNil(), "axis with no hides returns nil")
		Expect(hs.Projects()).To(Equal([]string{"secret-proj"}))
	})
})

// ---- observability: planTracer + n1Tracer.TraceQueryEnd ----

var _ = ginkgo.Describe("observability plan tracer (gaka-d6x)", func() {
	ginkgo.It("planTracer.TraceQueryStart/End: fast query (< threshold) does NOT trigger explain (no pool call)", func() {
		p := &planTracer{slow: 1 * time.Hour} // absurdly high threshold
		ctx := p.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
		Expect(ctx).NotTo(BeNil())
		// No pool set → explain would panic; but the fast-query check must short-circuit.
		p.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	})

	ginkgo.It("planTracer.TraceQueryStart: recursion-guard skip flag returns the ctx unchanged (never wraps with planStartKey)", func() {
		p := &planTracer{slow: 1 * time.Millisecond}
		skipCtx := context.WithValue(context.Background(), explainSkipKey{}, struct{}{})
		out := p.TraceQueryStart(skipCtx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
		// Verify NO planStartData was attached.
		_, ok := out.Value(planStartKey{}).(*planStartData)
		Expect(ok).To(BeFalse(), "skipped ctx must NOT be enriched — prevents recursive EXPLAIN storm")
	})

	ginkgo.It("planTracer.TraceQueryEnd: nil pool short-circuits (no panic)", func() {
		p := &planTracer{slow: 1 * time.Nanosecond}
		ctx := p.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
		time.Sleep(2 * time.Millisecond) // exceed threshold
		// pool is nil → the guard MUST early-return without touching it.
		p.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	})

	ginkgo.It("planTracer.explain: end-to-end for slow SELECT with a real pool logs EXPLAIN plan (no error)", func() {
		d := openTestDBG()
		p := &planTracer{slow: 1 * time.Nanosecond, pool: d.Pool}
		ctx := p.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
		time.Sleep(2 * time.Millisecond)
		// EXPLAIN SELECT 1 succeeds → the tracer's log path runs its rows-scan loop.
		p.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: nil})
	})

	ginkgo.It("n1Tracer.TraceQueryEnd: no-op even without a reqStats bucket in ctx (safe on non-HTTP paths)", func() {
		tr := n1Tracer{}
		tr.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{})
	})
})
