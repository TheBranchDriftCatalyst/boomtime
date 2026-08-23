// pin_test.go — canonical-entities pin persistence (LoadPinnedSet).
//
// Non-tautological invariants pinned here:
//   - a curation_rule created with action="pin" round-trips through
//     LoadPinnedSet (the read path the queryapi auto-apply relies on);
//   - a DISABLED pin is excluded from the set (boom-dfd parity with hides —
//     the row survives, its query-time effect pauses);
//   - the set is owner+axis scoped: another user's pin, and a pin on a
//     different axis, never bleed into this owner+axis's set.
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("canonical pins (LoadPinnedSet)", func() {
	ginkgo.It("round-trips a pin; excludes disabled; scopes to owner+axis", func() {
		d := openTestDBG()
		ctx := context.Background()

		suffix := time.Now().Format("150405.000000")
		alice := "pin_alice_" + suffix
		bob := "pin_bob_" + suffix
		for _, u := range []string{alice, bob} {
			_, err := d.Pool.Exec(ctx,
				`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, u)
			Expect(err).NotTo(HaveOccurred())
		}
		ginkgo.DeferCleanup(func() {
			for _, u := range []string{alice, bob} {
				_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, u)
				_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
			}
		})

		// alice pins "Fantasy" on the genre axis.
		r, err := d.CreateCurationRule(ctx, alice, "genre", CurationPin, MatchExact, "Fantasy", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Action).To(Equal(CurationPin))
		Expect(r.Enabled).To(BeTrue())

		pins, err := d.LoadPinnedSet(ctx, alice, "genre")
		Expect(err).NotTo(HaveOccurred())
		Expect(pins).To(ConsistOf("Fantasy"), "the enabled pin must round-trip")

		// A DISABLED pin is excluded (but the enabled one stays).
		r2, err := d.CreateCurationRule(ctx, alice, "genre", CurationPin, MatchExact, "Horror", nil)
		Expect(err).NotTo(HaveOccurred())
		found, err := d.SetCurationRuleEnabled(ctx, alice, r2.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		pins, err = d.LoadPinnedSet(ctx, alice, "genre")
		Expect(err).NotTo(HaveOccurred())
		Expect(pins).To(ConsistOf("Fantasy"), "a disabled pin must not appear in the set")

		// Axis-scoped: a pin on a DIFFERENT axis does not bleed into genre.
		_, err = d.CreateCurationRule(ctx, alice, "author", CurationPin, MatchExact, "Sanderson", nil)
		Expect(err).NotTo(HaveOccurred())
		pins, err = d.LoadPinnedSet(ctx, alice, "genre")
		Expect(err).NotTo(HaveOccurred())
		Expect(pins).To(ConsistOf("Fantasy"), "an author pin must not appear on the genre axis")
		apins, err := d.LoadPinnedSet(ctx, alice, "author")
		Expect(err).NotTo(HaveOccurred())
		Expect(apins).To(ConsistOf("Sanderson"))

		// Owner-scoped: bob's genre pin never appears in alice's set.
		_, err = d.CreateCurationRule(ctx, bob, "genre", CurationPin, MatchExact, "SciFi", nil)
		Expect(err).NotTo(HaveOccurred())
		pins, err = d.LoadPinnedSet(ctx, alice, "genre")
		Expect(err).NotTo(HaveOccurred())
		Expect(pins).To(ConsistOf("Fantasy"), "bob's pin leaked into alice's set")
		bpins, err := d.LoadPinnedSet(ctx, bob, "genre")
		Expect(err).NotTo(HaveOccurred())
		Expect(bpins).To(ConsistOf("SciFi"))
	})
})
