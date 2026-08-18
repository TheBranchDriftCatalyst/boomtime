// pin_test.go — canonical-entities auto-apply through POST /api/v1/query.
//
// This is the whole feature end-to-end: a low-share genre that would fall into
// the "Other" bucket under a TopN policy is KEPT as its own group once the
// caller has pinned it (a curation_rule with action="pin"). The same seeded
// data, queried WITHOUT the pin, rolls that genre into "Other" — so the test
// proves the pin (not the seeding) is what saves the group.
package queryapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// seedFinishedBook inserts one finished reading_items row with a single genre.
func seedFinishedBook(hz *testutil.Harness, owner, genre string, n int) {
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_items
			(owner, source, external_id, title, status, finished, finished_at, genres, runtime_min)
		VALUES ($1, 'audible', $2, $3, 'read', true, $4, $5::jsonb, 60)`,
		owner,
		fmt.Sprintf("%s-%d", genre, n),
		fmt.Sprintf("%s book %d", genre, n),
		time.Now().UTC().AddDate(0, 0, -1),
		fmt.Sprintf(`["%s"]`, genre),
	)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "seed reading_items %s#%d: %v", genre, n, err)
}

// groupsByKey folds a query response's groups into a key→value map.
func groupsByKey(resp queryResponse) map[string]float64 {
	m := make(map[string]float64, len(resp.Groups))
	for _, g := range resp.Groups {
		m[g.Key] = g.Value
	}
	return m
}

var _ = Describe("POST /api/v1/query canonical pins (auto-apply)", func() {
	It("keeps a pinned low-share genre out of Other; without the pin it rolls in", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureBooks = true // reading domain must be reachable
		e := hz.Router()
		owner, token := hz.MintUser("q_pin_genre")

		// Fantasy=5, SciFi=4, Horror=1. Under TopN=2 + Other, Horror (lowest
		// share) is the only remainder → it rolls into "Other".
		for i := 0; i < 5; i++ {
			seedFinishedBook(hz, owner, "Fantasy", i)
		}
		for i := 0; i < 4; i++ {
			seedFinishedBook(hz, owner, "SciFi", i)
		}
		seedFinishedBook(hz, owner, "Horror", 0)

		groupedSpec := map[string]any{
			"domain":  "reading",
			"measure": "books",
			"group":   "genre",
			"bucket":  map[string]any{"topN": 2, "other": true},
		}

		// --- WITHOUT the pin: Horror falls into Other ---
		rec := doQuery(e, token, groupedSpec)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("groups"))
		before := groupsByKey(resp)
		Expect(before).To(HaveKeyWithValue("Fantasy", float64(5)))
		Expect(before).To(HaveKeyWithValue("SciFi", float64(4)))
		Expect(before).NotTo(HaveKey("Horror"), "without a pin, Horror must roll into Other")
		Expect(before).To(HaveKeyWithValue("Other", float64(1)), "the 1 Horror book is the Other bucket")

		// --- Pin Horror on the genre axis ---
		_, err := hz.DB.CreateCurationRule(context.Background(), owner, "genre", db.CurationPin, db.MatchExact, "Horror", nil)
		Expect(err).NotTo(HaveOccurred())

		// --- WITH the pin: identical query + data, Horror survives as its own group ---
		rec = doQuery(e, token, groupedSpec)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var resp2 queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp2)).To(Succeed())
		after := groupsByKey(resp2)
		Expect(after).To(HaveKeyWithValue("Fantasy", float64(5)))
		Expect(after).To(HaveKeyWithValue("SciFi", float64(4)))
		Expect(after).To(HaveKeyWithValue("Horror", float64(1)), "the pinned genre must be its own group, not Other")
		Expect(after).NotTo(HaveKey("Other"), "Horror was the only remainder, so pinning it empties Other")
	})
})
