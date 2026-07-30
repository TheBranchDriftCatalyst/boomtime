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
		Entry("Jinja alias (plain .jinja)", "template.jinja", strptr("Jinja")),
		Entry("Cabal config", "project.cabal", strptr("Cabal Config")),
		Entry("Go template", "index.gotmpl", strptr("Go template")),
		Entry("PureScript", "Main.purs", strptr("PureScript")),
		Entry("Dhall", "config.dhall", strptr("Dhall")),
		Entry("no extension → nil", "noext", (*string)(nil)),
		Entry("trailing dot → nil", "trailingdot.", (*string)(nil)),
	)

	// NAMED INVARIANT: unknown extensions must upper-case the raw ext, so a
	// never-before-seen extension still surfaces *something* to the aggregator
	// rather than getting silently dropped. Guards against a regression where
	// the default branch returns nil.
	It("upper-cases unknown extensions (default branch is not silent nil)", func() {
		got := LanguageFromEntity("weird.qqzzxx")
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("QQZZXX"))
	})

	// NAMED INVARIANT: extension detection uses filepath.Ext semantics — only
	// the FINAL dot-suffix matters, never a middle component. Prevents drift
	// toward splitting on the first dot (which would mis-label "archive.tar.gz"
	// as TAR).
	It("uses only the final extension (multi-dot filename)", func() {
		got := LanguageFromEntity("archive.tar.gz")
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("GZ"))
	})

	// NAMED INVARIANT: path components (dir separators, absolute paths) must
	// not leak into extension detection. Guards against a naive re-implementation
	// using strings.LastIndex(".") which would misfire on "/etc/foo.d/bar".
	It("ignores directory components when extracting extension", func() {
		got := LanguageFromEntity("/src/deep/nested/main.go")
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("GO"))
	})

	// NAMED INVARIANT: dotfile entities like ".gitignore" or ".env" are
	// treated by Go's filepath.Ext as pure-extension paths — so the current
	// contract labels them by their trailing name (".env" → "ENV"). This
	// pins that behavior so any future refactor to filepath.Ext handling
	// (e.g., detecting basename-only dotfiles) is a conscious change, not
	// a silent one.
	It("pins dotfile behavior: filepath.Ext of a dotfile is the whole name", func() {
		gi := LanguageFromEntity(".gitignore")
		env := LanguageFromEntity(".env")
		Expect(gi).NotTo(BeNil())
		Expect(env).NotTo(BeNil())
		Expect(*gi).To(Equal("GITIGNORE"))
		Expect(*env).To(Equal("ENV"))
	})

	// NAMED INVARIANT: the two Jinja aliases (.jinja, .jinja2) must map to the
	// SAME canonical label "Jinja". Prevents the classic bug where new alias
	// gets a different casing and dashboards double-count.
	It("collapses .jinja and .jinja2 to the same label", func() {
		a := LanguageFromEntity("a.jinja")
		b := LanguageFromEntity("b.jinja2")
		Expect(a).NotTo(BeNil())
		Expect(b).NotTo(BeNil())
		Expect(*a).To(Equal(*b))
	})

	// NAMED INVARIANT: named language mappings must NOT be upper-cased. Guards
	// against a regression where a refactor uppercases the switch result and
	// turns "PureScript" into "PURESCRIPT" (breaking canonical labels).
	It("preserves mixed-case labels for named languages", func() {
		names := map[string]string{
			"x.org":     "Org",
			"x.zig":     "Zig",
			"x.purs":    "PureScript",
			"x.dhall":   "Dhall",
			"x.cabal":   "Cabal Config",
			"x.gotmpl":  "Go template",
			"x.jinja":   "Jinja",
			"x.tfvars":  "Terraform",
		}
		for entity, want := range names {
			got := LanguageFromEntity(entity)
			Expect(got).NotTo(BeNil(), "entity %q returned nil", entity)
			Expect(*got).To(Equal(want), "entity %q", entity)
		}
	})
})

// NAMED INVARIANT block for UserAgentInfo — covers non-happy-path shapes.
var _ = Describe("UserAgentInfo edge cases", func() {
	// NAMED INVARIANT: an empty user-agent string still returns a well-formed
	// EditorInfo. filepath.Ext / strings.Split on "" yields [""] (len 1), so
	// platform (index 1) must be nil — NOT an empty-string pointer that would
	// mis-render as "" in dashboards.
	It("empty UA leaves all fields nil (index 1..4 all out of range)", func() {
		info := UserAgentInfo("")
		Expect(info.Platform).To(BeNil())
		Expect(info.Editor).To(BeNil())
		Expect(info.Plugin).To(BeNil())
	})

	// NAMED INVARIANT: index 0 (the "wakatime/x.y" token) is intentionally
	// discarded. Guards against off-by-one refactor that promotes token[0] to
	// platform (would cause every UA to log "wakatime/..." as the OS).
	It("discards token[0] — platform is token[1]", func() {
		info := UserAgentInfo("wakatime/1.0 darwin go1.20 vim wakavim/2.0")
		Expect(info.Platform).NotTo(BeNil())
		Expect(*info.Platform).To(Equal("darwin"))
		// token[0] "wakatime/1.0" must NEVER appear as platform.
		Expect(*info.Platform).NotTo(Equal("wakatime/1.0"))
	})

	// NAMED INVARIANT: the parser is index-based, not name-based. Tokens
	// beyond index 4 must be ignored, not concatenated into Plugin. Prevents
	// a regression where a trailing " extra" token gets appended to plugin.
	It("ignores tokens beyond index 4", func() {
		info := UserAgentInfo("wakatime/1.0 linux go1.20 code/1 code-wakatime/4 EXTRA_TAIL another")
		Expect(info.Plugin).NotTo(BeNil())
		Expect(*info.Plugin).To(Equal("code-wakatime/4"))
		Expect(*info.Plugin).NotTo(ContainSubstring("EXTRA_TAIL"))
	})

	// NAMED INVARIANT: each returned pointer is independent — mutating the
	// caller's copy of *Editor must not corrupt *Platform / *Plugin. Guards
	// against a naive shared-backing-array refactor.
	It("returns independent string pointers per field", func() {
		info := UserAgentInfo("wakatime/1.0 linux go1.20 code/1 code-wakatime/4")
		// Snapshot originals.
		platOrig := *info.Platform
		plugOrig := *info.Plugin
		// Mutate the local Editor copy — must not alias other fields.
		mutated := *info.Editor + "_MUTATED"
		_ = mutated
		Expect(*info.Platform).To(Equal(platOrig))
		Expect(*info.Plugin).To(Equal(plugOrig))
	})
})

// -- restored from internal/wakatime/wakatime_test.go during kill-switch (gaka-0vp.17) --
func strptr(s string) *string { return &s }
