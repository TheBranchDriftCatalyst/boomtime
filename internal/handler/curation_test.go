// curation_ginkgo_test.go — ginkgo mirror of curation_test.go.
// 1:1 case map (2 stdlib TestXxx):
//   TestCurationAxisWhitelist    → curation whitelist > valid axes + invalid axes (as 2 Its)
//   TestCurationActionConstants  → curation constants > "hide/rename constants have not drifted"
package handler

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

var _ = Describe("curation whitelist", func() {
	It("resolves every valid axis via db.ExploreColumn", func() {
		valid := []string{"project", "language", "editor", "plugin", "platform", "machine", "branch", "category", "type", "entity", "day"}
		for _, a := range valid {
			_, ok := db.ExploreColumn(a)
			Expect(ok).To(BeTrue(), "axis %q should be whitelisted for curation", a)
		}
	})

	It("rejects invalid or raw-column axes", func() {
		invalid := []string{"sender", "id", "ty", "is_write", "time_sent", "", "DROP TABLE"}
		for _, a := range invalid {
			_, ok := db.ExploreColumn(a)
			Expect(ok).To(BeFalse(), "axis %q should be rejected by the curation whitelist", a)
		}
	})
})

var _ = Describe("curation constants", func() {
	It("keeps the hide/rename constants stable (FE JSON contract)", func() {
		Expect(db.CurationHide).To(Equal("hide"))
		Expect(db.CurationRename).To(Equal("rename"))
	})
})
