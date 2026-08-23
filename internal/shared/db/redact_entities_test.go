// redact_entities_ginkgo_test.go — ginkgo mirror of redact_entities_test.go (boom-0vp.13).
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//
//	TestRedactEntitiesCaseInsensitiveAndOwnerScoped → "case-insensitive + owner-scoped"
//	TestRedactEntitiesTyScoped                      → "ty-scoped (file redact doesn't touch url)"
//	TestRedactEntitiesEmptyInputIsNoop              → "empty input is a no-op"
//	TestListEntitiesByTypeExcludesRedacted          → "list excludes redacted rows"
package db

import (
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("RedactEntities", func() {
	ginkgo.It("is case-insensitive and owner-scoped and preserves the heartbeat rows", func() {
		d := openTestDBG()

		a := newSenderG(d, "redA")
		b := newSenderG(d, "redB")

		day := time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC)
		ensureProjectsG(d, a.Ctx(), a.Sender(), "P")
		ensureProjectsG(d, b.Ctx(), b.Sender(), "P")

		// A: three case variants of the same entity + one distinct entity.
		insertSeedG(d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "src/main.go", ts: day, gap: 60})
		insertSeedG(d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "src/Main.go", ts: day.Add(time.Minute), gap: 60})
		insertSeedG(d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "SRC/MAIN.GO", ts: day.Add(2 * time.Minute), gap: 60})
		insertSeedG(d, a.Ctx(), a.Sender(), hbSeed{project: "P", entity: "keep.go", ts: day.Add(3 * time.Minute), gap: 60})

		// B: identical case variant that MUST survive (owner scoping).
		insertSeedG(d, b.Ctx(), b.Sender(), hbSeed{project: "P", entity: "src/main.go", ts: day, gap: 60})

		n, err := d.RedactEntities(a.Ctx(), a.Sender(), "file", []string{"src/main.go"})
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(3), "three case variants of A's")

		// The heartbeat ROWS must survive.
		var totalAfter int
		err = d.Pool.QueryRow(a.Ctx(), `SELECT count(*) FROM heartbeats WHERE sender=$1`, a.Sender()).Scan(&totalAfter)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalAfter).To(Equal(4), "rows preserved")

		// All three of A's src/main.go variants now have entity = ''.
		var blank int
		err = d.Pool.QueryRow(a.Ctx(), `SELECT count(*) FROM heartbeats WHERE sender=$1 AND entity=''`, a.Sender()).Scan(&blank)
		Expect(err).NotTo(HaveOccurred())
		Expect(blank).To(Equal(3))

		// The 'keep.go' row is untouched.
		var keep int
		err = d.Pool.QueryRow(a.Ctx(), `SELECT count(*) FROM heartbeats WHERE sender=$1 AND entity='keep.go'`, a.Sender()).Scan(&keep)
		Expect(err).NotTo(HaveOccurred())
		Expect(keep).To(Equal(1), "non-matching entity untouched")

		// B's src/main.go is UNTOUCHED.
		var bMain int
		err = d.Pool.QueryRow(b.Ctx(), `SELECT count(*) FROM heartbeats WHERE sender=$1 AND lower(entity)=lower($2)`, b.Sender(), "src/main.go").Scan(&bMain)
		Expect(err).NotTo(HaveOccurred())
		Expect(bMain).To(Equal(1), "A's redact must not touch B")
	})

	ginkgo.It("is ty-scoped: file redact doesn't touch a matching url row", func() {
		d := openTestDBG()
		f := newSenderG(d, "red_ty")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		day := time.Date(2025, 6, 4, 10, 0, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "https://ex.com/x", ty: "file", ts: day, gap: 60})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "https://ex.com/x", ty: "url", ts: day.Add(time.Minute), gap: 60})

		n, err := d.RedactEntities(ctx, sender, "file", []string{"https://ex.com/x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1), "only ty='file' row")

		var alive int
		err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND ty='url' AND entity=$2`, sender, "https://ex.com/x").Scan(&alive)
		Expect(err).NotTo(HaveOccurred())
		Expect(alive).To(Equal(1), "redact must be ty-scoped")
	})

	ginkgo.It("empty entity slice is a no-op", func() {
		d := openTestDBG()
		f := newSenderG(d, "red_empty")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "x.go",
			ts: time.Date(2025, 6, 5, 10, 0, 0, 0, time.UTC), gap: 60})

		n, err := d.RedactEntities(ctx, sender, "file", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(0))

		var blanks int
		err = d.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND entity=''`, sender).Scan(&blanks)
		Expect(err).NotTo(HaveOccurred())
		Expect(blanks).To(Equal(0))
	})

	ginkgo.It("ListEntitiesByType excludes redacted rows and does not surface a blank-entity bucket", func() {
		d := openTestDBG()
		f := newSenderG(d, "red_list")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		day := time.Date(2025, 6, 6, 10, 0, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "purgeme.go", ts: day, gap: 60})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "keep.go", ts: day.Add(time.Minute), gap: 60})

		_, err := d.RedactEntities(ctx, sender, "file", []string{"purgeme.go"})
		Expect(err).NotTo(HaveOccurred())
		list, _, err := d.ListEntitiesByType(ctx, sender, "file", 100)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range list {
			Expect(strings.EqualFold(e.Entity, "purgeme.go")).To(BeFalse(), "redacted entity %q still listed", e.Entity)
			Expect(e.Entity).NotTo(BeEmpty(), "blank-entity bucket surfaced — the entity<>'' filter is broken")
		}
		seen := false
		for _, e := range list {
			if e.Entity == "keep.go" {
				seen = true
			}
		}
		Expect(seen).To(BeTrue(), "keep.go missing from list after redact")
	})
})
