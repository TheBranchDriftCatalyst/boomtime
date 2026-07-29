// widgets_ginkgo_test.go — ginkgo mirror of widgets_test.go (bd gaka-6jm.5).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestIsWidgetScopeProjectHidden_HiddenProject     → isWidgetScopeProjectHidden > "hidden project reported (case-insensitive)"
//	TestIsWidgetScopeProjectHidden_VisibleProject    → isWidgetScopeProjectHidden > "visible project passes through"
//	TestIsWidgetScopeProjectHidden_NoRules           → isWidgetScopeProjectHidden > "empty/nil rules → false"
//	TestIsWidgetScopeProjectHidden_OtherAxesIgnored  → isWidgetScopeProjectHidden > "project-scope keys off project axis only"
package handler

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

var _ = Describe("isWidgetScopeProjectHidden", func() {
	It("reports a hidden project as hidden (case-insensitive)", func() {
		hidden := model.HiddenSetsMap{"project": {"hakatime"}}
		Expect(isWidgetScopeProjectHidden(hidden, "hakatime")).To(BeTrue())
		Expect(isWidgetScopeProjectHidden(hidden, "HAKATIME")).To(BeTrue())
		Expect(isWidgetScopeProjectHidden(hidden, "Hakatime")).To(BeTrue())
	})

	It("passes visible projects through", func() {
		hidden := model.HiddenSetsMap{"project": {"hakatime"}}
		Expect(isWidgetScopeProjectHidden(hidden, "boomtime")).To(BeFalse())
	})

	It("with no rules (empty or nil) never hides", func() {
		Expect(isWidgetScopeProjectHidden(model.HiddenSetsMap{}, "boomtime")).To(BeFalse())
		Expect(isWidgetScopeProjectHidden(nil, "boomtime")).To(BeFalse())
	})

	It("ignores non-project axes (hiding a language must NOT hide a project-scope widget)", func() {
		hidden := model.HiddenSetsMap{"language": {"hakatime"}}
		Expect(isWidgetScopeProjectHidden(hidden, "hakatime")).To(BeFalse())
	})
})
