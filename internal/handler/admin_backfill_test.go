// admin_backfill_ginkgo_test.go — ginkgo mirror of admin_backfill_test.go
// (gaka-vh8).
// 1:1 case map (2 stdlib TestXxx):
//
//	TestBackfillEvent2JSON_WireShape          → backfillEvent2json > "wire shape (kind/job.*)"
//	TestBackfillConfigPatch_WireShape_RoundTrips
//	                                          → backfillConfigPatch > "round-trips PATCH body"
package handler

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
)

var _ = Describe("backfillEvent2json", func() {
	It("emits the FE wire shape (kind + job.{id,owner,repoName,repoPath,status,enqueuedAt,total})", func() {
		now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
		ev := backfilljobs.Event{
			Kind: backfilljobs.EventAdded,
			Job: backfilljobs.Job{
				ID:         "job-1",
				Owner:      "panda",
				RepoName:   "boomtime",
				RepoPath:   "/Users/panda/code/boomtime",
				Status:     backfilljobs.StatusQueued,
				Total:      42,
				EnqueuedAt: now,
			},
		}
		raw, err := json.Marshal(backfillEvent2json(ev))
		Expect(err).NotTo(HaveOccurred())

		var got map[string]any
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got["kind"]).To(Equal("added"))

		job, ok := got["job"].(map[string]any)
		Expect(ok).To(BeTrue(), "job field missing/wrong type: %#v", got["job"])
		for _, k := range []string{"id", "owner", "repoName", "repoPath", "status", "enqueuedAt", "total"} {
			Expect(job).To(HaveKey(k), "wire field %q missing (raw=%s)", k, string(raw))
		}
		Expect(job["status"]).To(Equal("queued"))
	})
})

var _ = Describe("backfillConfigPatch", func() {
	It("round-trips a full PATCH body (guards against struct-tag drift)", func() {
		src := `{"clusterGapSec": 900, "authorEmails": ["a@b.c"], "sourceTag": "backfill:git", "langMap": {"ts":"TypeScript"}}`
		var p backfillConfigPatch
		Expect(json.Unmarshal([]byte(src), &p)).To(Succeed())

		Expect(p.ClusterGapSec).NotTo(BeNil())
		Expect(*p.ClusterGapSec).To(BeEquivalentTo(900))
		Expect(p.SourceTag).NotTo(BeNil())
		Expect(*p.SourceTag).To(Equal("backfill:git"))
		Expect(p.AuthorEmails).NotTo(BeNil())
		Expect(*p.AuthorEmails).To(Equal([]string{"a@b.c"}))
		Expect(p.LangMap["ts"]).To(Equal("TypeScript"))
	})
})
