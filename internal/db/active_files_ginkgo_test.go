// active_files_ginkgo_test.go — ginkgo mirror of active_files_test.go (gaka-0vp.13).
// 1:1 case map (5 stdlib TestXxx → 5 Its):
//   TestActiveFilesCrossProject           → "cross-project files count projects+time"
//   TestActiveFilesRespectsGapCutoff      → "respects gap cutoff"
//   TestActiveFilesHiddenProjectExcluded  → "hidden project drops from shared file"
//   TestActiveFilesRenameMergesProjectCount → "rename merges DISTINCT project count"
//   TestActiveFilesTruncation             → "limit caps result and flips truncated=true"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("GetActiveFiles", func() {
	ginkgo.It("cross-project: a file touched by two projects reports projects=2 with summed attributed time; non-file rows excluded", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "actf")
		sender := f.Sender()
		f.Projects("alpha", "beta")

		base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
		f.Seed(hbSeed{project: "alpha", entity: "router.py", ty: "file", ts: base, gap: 120})
		f.Seed(hbSeed{project: "beta", entity: "router.py", ty: "file", ts: base.Add(time.Minute), gap: 60})
		f.Seed(hbSeed{project: "alpha", entity: "only_a.go", ty: "file", ts: base.Add(2 * time.Minute), gap: 200})
		f.Seed(hbSeed{project: "beta", entity: "router.py", ty: "domain", ts: base.Add(3 * time.Minute), gap: 300})

		t0 := base.AddDate(0, 0, -1)
		t1 := base.AddDate(0, 0, 1)

		files, trunc, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(trunc).To(BeFalse())
		by := afByEntity(files)

		r, ok := by["router.py"]
		Expect(ok).To(BeTrue(), "router.py missing")
		Expect(r.Projects).To(BeEquivalentTo(2))
		Expect(r.Seconds).To(BeEquivalentTo(180), "non-file excluded")

		a, ok := by["only_a.go"]
		Expect(ok).To(BeTrue(), "only_a.go missing")
		Expect(a.Projects).To(BeEquivalentTo(1))
		Expect(a.Seconds).To(BeEquivalentTo(200))

		Expect(files[0].Entity).To(Equal("router.py"), "expected router.py first (lynchpin order)")
	})

	ginkgo.It("respects the gap cutoff (>timeLimit*60 dropped)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "actfgap")
		sender := f.Sender()
		f.Projects("alpha")

		base := time.Date(2025, 5, 4, 10, 0, 0, 0, time.UTC)
		f.Seed(hbSeed{project: "alpha", entity: "x.go", ts: base, gap: 120})
		f.Seed(hbSeed{project: "alpha", entity: "x.go", ts: base.Add(time.Minute), gap: 999999})

		files, _, err := d.GetActiveFiles(ctx, sender, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1), 15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		by := afByEntity(files)
		Expect(by["x.go"].Seconds).To(BeEquivalentTo(120))
	})

	ginkgo.It("hidden project drops its contribution from a shared file's project count + time", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "actfhide")
		sender := f.Sender()
		f.Projects("keep", "secret")

		base := time.Date(2025, 5, 5, 10, 0, 0, 0, time.UTC)
		f.Seed(hbSeed{project: "keep", entity: "shared.go", ts: base, gap: 120})
		f.Seed(hbSeed{project: "secret", entity: "shared.go", ts: base.Add(time.Minute), gap: 60})

		t0 := base.AddDate(0, 0, -1)
		t1 := base.AddDate(0, 0, 1)

		all, _, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(all[0].Projects).To(BeEquivalentTo(2))
		Expect(all[0].Seconds).To(BeEquivalentTo(180))

		hs := mkHiddenSets(map[string][]string{"project": {"secret"}})
		hidden, _, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, hs, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		by := afByEntity(hidden)
		s := by["shared.go"]
		Expect(s.Projects).To(BeEquivalentTo(1))
		Expect(s.Seconds).To(BeEquivalentTo(120))
	})

	ginkgo.It("rename merges DISTINCT project count when both raw names map to the same display", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "actfren")
		sender := f.Sender()
		f.Projects("web-old", "web-new")

		base := time.Date(2025, 5, 6, 10, 0, 0, 0, time.UTC)
		f.Seed(hbSeed{project: "web-old", entity: "api.go", ts: base, gap: 120})
		f.Seed(hbSeed{project: "web-new", entity: "api.go", ts: base.Add(time.Minute), gap: 60})

		t0 := base.AddDate(0, 0, -1)
		t1 := base.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "web-old", "web")
		createRenameG(d, ctx, sender, "project", "web-new", "web")
		rs := loadRenamesG(d, ctx, sender)

		files, _, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		by := afByEntity(files)
		api := by["api.go"]
		Expect(api.Projects).To(BeEquivalentTo(1), "merged display name")
		Expect(api.Seconds).To(BeEquivalentTo(180), "conserved")
	})

	ginkgo.It("truncates the result at the limit and flips truncated=true", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "actftrunc")
		sender := f.Sender()
		f.Projects("alpha")

		base := time.Date(2025, 5, 7, 10, 0, 0, 0, time.UTC)
		for i := 0; i < 5; i++ {
			f.Seed(hbSeed{project: "alpha", entity: "f" + string(rune('a'+i)) + ".go", ts: base.Add(time.Duration(i) * time.Minute), gap: 60})
		}

		files, trunc, err := d.GetActiveFiles(ctx, sender, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1), 15, 3, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(HaveLen(3))
		Expect(trunc).To(BeTrue())
	})
})
