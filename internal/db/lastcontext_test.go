// lastcontext_test.go — integration coverage for the `<<LAST_*>>` placeholder
// machinery against the test DB: GetLastKnownContext (the ingest seed) and
// BackfillLastContext (the historical-row fix). Both exercise real rows so the
// correlated-subquery SQL, the time-ordering, and the placeholder LIKE filter
// are all covered end-to-end.
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// axisAt reads one heartbeat's project/branch/language by sender + time_sent.
func axisAt(d *DB, ctx context.Context, sender string, ts time.Time) (project, branch, language *string) {
	err := d.Pool.QueryRow(ctx,
		`SELECT project, branch, language FROM heartbeats WHERE sender=$1 AND time_sent=$2`,
		sender, ts).Scan(&project, &branch, &language)
	Expect(err).NotTo(HaveOccurred())
	return project, branch, language
}

var _ = ginkgo.Describe("GetLastKnownContext", func() {
	ginkgo.It("returns the latest REAL value per axis, skipping placeholders and nulls", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "lastknown")
		sender := f.Sender()
		base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)

		// t0: all three axes real.
		f.Seed(hbSeed{project: "foo", language: "Go", branch: "main", ts: base})
		// t1: all three placeholders — must be excluded as a seed source.
		f.Seed(hbSeed{project: "<<LAST_PROJECT>>", language: "<<LAST_LANGUAGE>>", branch: "<<LAST_BRANCH>>", ts: base.Add(time.Minute)})
		// t2: a newer real project; language/branch left null (nz => NULL).
		f.Seed(hbSeed{project: "bar", ts: base.Add(2 * time.Minute)})

		project, language, branch, err := d.GetLastKnownContext(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(project).NotTo(BeNil())
		Expect(*project).To(Equal("bar"), "latest real project wins (t2 > t0), placeholder at t1 ignored")
		Expect(language).NotTo(BeNil())
		Expect(*language).To(Equal("Go"), "t2 language is null, t1 is a placeholder -> falls back to t0")
		Expect(branch).NotTo(BeNil())
		Expect(*branch).To(Equal("main"))
	})

	ginkgo.It("returns nil for every axis when the sender has only placeholders / no real value", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "noreal")
		sender := f.Sender()
		base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
		f.Seed(hbSeed{project: "<<LAST_PROJECT>>", language: "<<LAST_LANGUAGE>>", branch: "<<LAST_BRANCH>>", ts: base})

		project, language, branch, err := d.GetLastKnownContext(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(project).To(BeNil())
		Expect(language).To(BeNil())
		Expect(branch).To(BeNil())
	})
})

var _ = ginkgo.Describe("BackfillLastContext", func() {
	ginkgo.It("dry-run reports counts and writes nothing", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "bfdry")
		sender := f.Sender()
		base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
		f.Seed(hbSeed{project: "foo", branch: "feat", language: "Go", ts: base})
		phTS := base.Add(time.Minute)
		f.Seed(hbSeed{project: "<<LAST_PROJECT>>", branch: "<<LAST_BRANCH>>", language: "<<LAST_LANGUAGE>>", ts: phTS})

		res, err := d.BackfillLastContext(ctx, true /* dryRun */)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.DryRun).To(BeTrue())
		// Global counts include any sibling test's rows, so assert our row is
		// counted (>= 1) rather than an exact global total.
		Expect(res.ProjectSubstituted).To(BeNumerically(">=", int64(1)))
		Expect(res.AffectedSenders).To(ContainElement(sender))

		// Row is still the literal placeholder — dry-run wrote nothing.
		project, branch, language := axisAt(d, ctx, sender, phTS)
		Expect(project).NotTo(BeNil())
		Expect(*project).To(Equal("<<LAST_PROJECT>>"))
		Expect(*branch).To(Equal("<<LAST_BRANCH>>"))
		Expect(*language).To(Equal("<<LAST_LANGUAGE>>"))
	})

	ginkgo.It("substitutes placeholders with the prior real value, nulls the no-prior ones, leaves real rows untouched", func() {
		d := openTestDBG()
		ctx := context.Background()
		base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)

		// Sender A: real -> placeholder (resolvable) -> real (untouched).
		a := newSenderG(d, "bfa")
		senderA := a.Sender()
		aReal := base
		aPH := base.Add(time.Minute)
		aAfter := base.Add(2 * time.Minute)
		a.Seed(hbSeed{project: "foo", branch: "featA", language: "Go", ts: aReal})
		a.Seed(hbSeed{project: "<<LAST_PROJECT>>", branch: "<<LAST_BRANCH>>", language: "<<LAST_LANGUAGE>>", ts: aPH})
		a.Seed(hbSeed{project: "bar", branch: "featB", language: "Rust", ts: aAfter})

		// Sender B: a placeholder with NO prior real value on any axis.
		b := newSenderG(d, "bfb")
		senderB := b.Sender()
		bPH := base.Add(time.Minute)
		b.Seed(hbSeed{project: "<<LAST_PROJECT>>", branch: "<<LAST_BRANCH>>", language: "<<LAST_LANGUAGE>>", ts: bPH})

		res, err := d.BackfillLastContext(ctx, false /* apply */)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.DryRun).To(BeFalse())
		Expect(res.AffectedSenders).To(ContainElements(senderA, senderB))

		// Sender A placeholder row: resolved to the t0 real values.
		project, branch, language := axisAt(d, ctx, senderA, aPH)
		Expect(project).NotTo(BeNil())
		Expect(*project).To(Equal("foo"))
		Expect(branch).NotTo(BeNil())
		Expect(*branch).To(Equal("featA"))
		Expect(language).NotTo(BeNil())
		Expect(*language).To(Equal("Go"))

		// Sender A real "after" row: untouched.
		project2, branch2, language2 := axisAt(d, ctx, senderA, aAfter)
		Expect(*project2).To(Equal("bar"))
		Expect(*branch2).To(Equal("featB"))
		Expect(*language2).To(Equal("Rust"))

		// Sender B: no prior real value -> literal dropped to NULL, never left verbatim.
		projectB, branchB, languageB := axisAt(d, ctx, senderB, bPH)
		Expect(projectB).To(BeNil())
		Expect(branchB).To(BeNil())
		Expect(languageB).To(BeNil())
	})
})
