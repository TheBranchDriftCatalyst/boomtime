// ingest_scrub_test.go — end-to-end proof that an apply_at_ingest rename rule
// (gaka-scrub) rewrites the STORED heartbeat field. POSTs a heartbeat and reads
// the row back, so it pins the whole path (storeAndRespond → LoadIngestRenameRules
// → IngestRenameSet.Apply → Save), not just the applier unit.
package ingest_test

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Ingest-time rename scrub (gaka-scrub)", func() {
	hbBody := func(entity string) map[string]any {
		return map[string]any{
			"time": float64(time.Now().Unix()), "entity": entity, "type": "file",
			"project": "boomtime", "user_agent": "wakatime/1 (Linux) go/1 vscode wakatime-vscode/1",
		}
	}
	post := func(hz *testutil.Harness, tok, entity string) {
		e := hz.Router()
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/heartbeats", tok, hbBody(entity))
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
	}

	It("an apply_at_ingest entity rename strips the prefix from the STORED entity", func() {
		hz := testutil.NewHarness(GinkgoT())
		owner, tok := hz.MintUser("scrub_on")
		empty := ""
		_, err := hz.DB.CreateCurationRuleWithIngest(context.Background(), owner,
			"entity", "rename", "template", `^/secret/`, &empty, true)
		Expect(err).NotTo(HaveOccurred())

		post(hz, tok, "/secret/app/main.go")
		Expect(latestEnrichedRow(hz, owner).Entity).To(Equal("app/main.go"))
	})

	It("no rule → stored entity is unchanged", func() {
		hz := testutil.NewHarness(GinkgoT())
		owner, tok := hz.MintUser("scrub_none")
		post(hz, tok, "/secret/app/main.go")
		Expect(latestEnrichedRow(hz, owner).Entity).To(Equal("/secret/app/main.go"))
	})

	It("a DISABLED apply_at_ingest rule does NOT scrub (LoadIngestRenameRules skips disabled)", func() {
		hz := testutil.NewHarness(GinkgoT())
		owner, tok := hz.MintUser("scrub_off")
		empty := ""
		rule, err := hz.DB.CreateCurationRuleWithIngest(context.Background(), owner,
			"entity", "rename", "template", `^/secret/`, &empty, true)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = hz.DB.ToggleCurationRule(context.Background(), owner, rule.ID) // pause it
		Expect(err).NotTo(HaveOccurred())

		post(hz, tok, "/secret/app/main.go")
		Expect(latestEnrichedRow(hz, owner).Entity).To(Equal("/secret/app/main.go"))
	})

	It("a NON-ingest (query-time) rename does NOT rewrite the stored row", func() {
		hz := testutil.NewHarness(GinkgoT())
		owner, tok := hz.MintUser("scrub_qtime")
		empty := ""
		_, err := hz.DB.CreateCurationRuleWithIngest(context.Background(), owner,
			"entity", "rename", "template", `^/secret/`, &empty, false) // apply_at_ingest = false
		Expect(err).NotTo(HaveOccurred())

		post(hz, tok, "/secret/app/main.go")
		Expect(latestEnrichedRow(hz, owner).Entity).To(Equal("/secret/app/main.go"),
			"a query-time rule leaves raw rows pristine — only apply_at_ingest bakes them")
	})
})
