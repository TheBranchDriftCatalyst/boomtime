// badges_ginkgo_test.go — ginkgo mirror of badges_test.go (bd boom-6jm.3).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestApplyBadgeCuration_HiddenProject      → applyBadgeCuration > "hidden project resolves to 'hidden' (case-insensitive)"
//	TestApplyBadgeCuration_VisibleProject     → applyBadgeCuration > "visible project passes through untouched"
//	TestApplyBadgeCuration_NoRules            → applyBadgeCuration > "empty/nil rules → passthrough"
//	TestApplyBadgeCuration_OtherAxesIgnored   → applyBadgeCuration > "only the project axis matters"
package widgets

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

var _ = Describe("applyBadgeCuration", func() {
	It("resolves a hidden project to 'hidden' (case-insensitive)", func() {
		hidden := model.HiddenSetsMap{"project": {"hakatime"}}
		Expect(applyBadgeCuration(hidden, "hakatime")).To(Equal("hidden"))
		// Case-insensitive: db.LoadHiddenSets lowercases match_value before
		// storing, and exclusionPredicate compares via `lower(col)`.
		Expect(applyBadgeCuration(hidden, "HAKATIME")).To(Equal("hidden"))
	})

	It("passes visible projects through untouched", func() {
		hidden := model.HiddenSetsMap{"project": {"hakatime"}}
		Expect(applyBadgeCuration(hidden, "boomtime")).To(Equal("boomtime"))
	})

	It("with no rules (empty or nil) passes everything through", func() {
		Expect(applyBadgeCuration(model.HiddenSetsMap{}, "boomtime")).To(Equal("boomtime"))
		// Nil-safe (defensive; the handler should never pass nil, but the helper
		// tolerates it so a future refactor can't crash the endpoint).
		Expect(applyBadgeCuration(nil, "boomtime")).To(Equal("boomtime"))
	})

	It("only keys off the project axis (hiding a language must NOT hide a badge)", func() {
		hidden := model.HiddenSetsMap{"language": {"hakatime"}}
		Expect(applyBadgeCuration(hidden, "hakatime")).To(Equal("hakatime"))
	})
})
