// wakatime_ginkgo_test.go — ginkgo mirror of wakatime_test.go (gaka-0vp).
// 1:1 case map (3 stdlib TestXxx):
//
//	TestUserAgentInfo         → UserAgentInfo > "extracts 5-token UA"
//	TestUserAgentInfoShort    → UserAgentInfo > "short UA gracefully"
//	TestLanguageFromEntity    → LanguageFromEntity > DescribeTable of 7 entries
package wakatime

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("UserAgentInfo", func() {
	It("extracts platform/editor/plugin from a 5-token wakatime UA", func() {
		info := UserAgentInfo("wakatime/1.0 (Linux-5.4) go1.20 vscode/1.70 vscode-wakatime/4.0")
		Expect(info.Platform).NotTo(BeNil())
		Expect(*info.Platform).To(Equal("(Linux-5.4)"))
		Expect(info.Editor).NotTo(BeNil())
		Expect(*info.Editor).To(Equal("vscode/1.70"))
		Expect(info.Plugin).NotTo(BeNil())
		Expect(*info.Plugin).To(Equal("vscode-wakatime/4.0"))
	})

	It("leaves editor/plugin nil for a short UA", func() {
		info := UserAgentInfo("only two")
		Expect(info.Platform).NotTo(BeNil())
		Expect(*info.Platform).To(Equal("two"))
		Expect(info.Editor).To(BeNil())
		Expect(info.Plugin).To(BeNil())
	})
})

var _ = Describe("LanguageFromEntity", func() {
	DescribeTable("entity path → detected language",
		func(entity string, want *string) {
			got := LanguageFromEntity(entity)
			if want == nil {
				Expect(got).To(BeNil())
			} else {
				Expect(got).NotTo(BeNil())
				Expect(*got).To(Equal(*want))
			}
		},
		Entry("Go source", "main.go", strptr("GO")),
		Entry("Zig source", "main.zig", strptr("Zig")),
		Entry("Terraform vars", "vars.tfvars", strptr("Terraform")),
		Entry("Org mode", "notes.org", strptr("Org")),
		Entry("Jinja template", "template.jinja2", strptr("Jinja")),
		Entry("no extension → nil", "noext", (*string)(nil)),
		Entry("trailing dot → nil", "trailingdot.", (*string)(nil)),
	)
})

// -- restored from internal/wakatime/wakatime_test.go during kill-switch (gaka-0vp.17) --
func strptr(s string) *string { return &s }
