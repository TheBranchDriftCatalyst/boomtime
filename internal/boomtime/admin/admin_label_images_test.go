// admin_label_images_ginkgo_test.go — ginkgo mirror of
// admin_label_images_test.go (boom-8bz).
// 1:1 case map (3 stdlib TestXxx):
//
//	TestEvent2JSON_WireShape         → event2json > "wire shape (FE hook keys)"
//	TestRegenResponseJob_WireShape   → regenResponseJob > "exact JSON envelope"
//
// The imagejobs.Event / imagejobs.Job wire-shape specs that used to live here
// went with the pipeline (boom-piig phase 2). They pinned the WebSocket frame
// format for a socket nothing connects to any more — the FE polls
// /api/v1/admin/label-images/status instead. regenResponseJob survives because
// that envelope is still what POST /regenerate returns.
//
//	TestJob_JSONOmitsZeroTimestamps  → Job JSON > "omits zero pointer fields"
package admin

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("regenResponseJob", func() {
	It("marshals to the exact {jobId,labelId,existing} envelope", func() {
		j := regenResponseJob{JobID: "j1", LabelID: "polyglot", Existing: true}
		b, err := json.Marshal(j)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(`{"jobId":"j1","labelId":"polyglot","existing":true}`))
	})
})
