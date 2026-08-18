// admin_label_images_ginkgo_test.go — ginkgo mirror of
// admin_label_images_test.go (gaka-8bz).
// 1:1 case map (3 stdlib TestXxx):
//
//	TestEvent2JSON_WireShape         → event2json > "wire shape (FE hook keys)"
//	TestRegenResponseJob_WireShape   → regenResponseJob > "exact JSON envelope"
//	TestJob_JSONOmitsZeroTimestamps  → Job JSON > "omits zero pointer fields"
package admin

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/queue/imagejobs"
)

var _ = Describe("event2json", func() {
	It("emits the FE wire shape (kind + job.{id,labelId,prompt,status,enqueuedAt})", func() {
		now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		seed := int64(42)
		ev := imagejobs.Event{
			Kind: imagejobs.EventAdded,
			Job: imagejobs.Job{
				ID:         "job-1",
				LabelID:    "late-night-coder",
				Prompt:     "hooded figure",
				Model:      "sdxl-illustrious-xl",
				Size:       "1024x1024",
				Seed:       &seed,
				Status:     imagejobs.StatusQueued,
				EnqueuedAt: now,
			},
		}
		raw, err := json.Marshal(event2json(ev))
		Expect(err).NotTo(HaveOccurred())

		var got map[string]any
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got["kind"]).To(Equal("added"))

		job, ok := got["job"].(map[string]any)
		Expect(ok).To(BeTrue(), "job field missing or wrong type: %#v", got["job"])
		for _, k := range []string{"id", "labelId", "prompt", "status", "enqueuedAt"} {
			Expect(job).To(HaveKey(k), "wire field %q missing (raw=%s)", k, string(raw))
		}
		Expect(job["id"]).To(Equal("job-1"))
		Expect(job["labelId"]).To(Equal("late-night-coder"))
		Expect(job["status"]).To(Equal("queued"))
	})
})

var _ = Describe("regenResponseJob", func() {
	It("marshals to the exact {jobId,labelId,existing} envelope", func() {
		j := regenResponseJob{JobID: "j1", LabelID: "polyglot", Existing: true}
		b, err := json.Marshal(j)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(`{"jobId":"j1","labelId":"polyglot","existing":true}`))
	})
})

var _ = Describe("imagejobs.Job JSON", func() {
	It("omits zero pointer fields (startedAt, finishedAt, error, seed)", func() {
		j := imagejobs.Job{
			ID:         "x",
			LabelID:    "a",
			Status:     imagejobs.StatusQueued,
			EnqueuedAt: time.Now().UTC(),
		}
		b, err := json.Marshal(j)
		Expect(err).NotTo(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		for _, k := range []string{"startedAt", "finishedAt", "error", "seed"} {
			Expect(got).NotTo(HaveKey(k), "%s present on queued job: %s", k, string(b))
		}
	})
})
