// wakatime_ginkgo_test.go — ginkgo mirror of wakatime_test.go (boom-0vp).
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
		// NAMED INVARIANT (boom-d6x): a bare empty string entity must return nil.
		// filepath.Ext("") == "" → hits the `ext == ""` outer guard directly, but
		// pin the input-shape edge separately from the ".noext" case (which reaches
		// the same branch via a different path). Prevents a refactor that special-
		// cases "no dot in name" without also handling empty input.
		Entry("empty entity → nil (direct empty-string input)", "", (*string)(nil)),
		// NAMED INVARIANT (boom-d6x): a bare "." entity must return nil.
		// filepath.Ext(".") == "." → hits the `ext == "."` outer guard. Distinct
		// from "trailingdot." (which also produces ".") because the input shape
		// itself is a root-level dotfile-like edge case worth pinning.
		Entry("bare dot → nil (root-level dotfile-like input)", ".", (*string)(nil)),
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
			"x.org":    "Org",
			"x.zig":    "Zig",
			"x.purs":   "PureScript",
			"x.dhall":  "Dhall",
			"x.cabal":  "Cabal Config",
			"x.gotmpl": "Go template",
			"x.jinja":  "Jinja",
			"x.tfvars": "Terraform",
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

	// NAMED INVARIANT (boom-d6x): each returned pointer is a DISTINCT allocation
	// — Platform, Editor, and Plugin must not alias the same *string. Guards
	// against a refactor that returns the same &v across fields (e.g., a shared
	// loop variable captured by reference, or a shared-backing-array trick).
	// A write through one pointer must not be observable through the others.
	It("returns independent (non-aliased) string pointers per field", func() {
		info := UserAgentInfo("wakatime/1.0 linux go1.20 code/1 code-wakatime/4")
		Expect(info.Platform).NotTo(BeNil())
		Expect(info.Editor).NotTo(BeNil())
		Expect(info.Plugin).NotTo(BeNil())

		// Pointer-identity assertions: no two fields may point at the same
		// underlying *string. BeIdenticalTo compares pointer values, not
		// dereferenced strings.
		Expect(info.Platform).NotTo(BeIdenticalTo(info.Editor))
		Expect(info.Platform).NotTo(BeIdenticalTo(info.Plugin))
		Expect(info.Editor).NotTo(BeIdenticalTo(info.Plugin))

		// Snapshot originals, then write THROUGH the Editor pointer. If any two
		// fields aliased the same *string, this write would leak into the
		// others (since *info.Editor = "..." reassigns the pointee).
		platOrig := *info.Platform
		plugOrig := *info.Plugin
		*info.Editor = "MUTATED"
		Expect(*info.Platform).To(Equal(platOrig))
		Expect(*info.Plugin).To(Equal(plugOrig))
	})

	// NAMED INVARIANT (boom-d6x): a single-token UA (no spaces at all) must
	// leave Platform / Editor / Plugin nil. strings.Split("onlyone", " ") returns
	// ["onlyone"] (len 1), so indices 1, 3, 4 are all out of range. Distinct
	// from the empty-UA case (which also yields len 1) because the input shape
	// — a bare identifier vs. an empty string — is a real edge worth pinning.
	It("single-token UA (no spaces) leaves platform/editor/plugin nil", func() {
		info := UserAgentInfo("onlyone")
		Expect(info.Platform).To(BeNil())
		Expect(info.Editor).To(BeNil())
		Expect(info.Plugin).To(BeNil())
	})
})

var _ = Describe("IsLastPlaceholder", func() {
	DescribeTable("classifies a value as a <<...>> template token or a real value",
		func(in string, want bool) {
			Expect(IsLastPlaceholder(in)).To(Equal(want))
		},
		// The three tokens macos-wakatime actually sends.
		Entry("LAST_PROJECT", "<<LAST_PROJECT>>", true),
		Entry("LAST_BRANCH", "<<LAST_BRANCH>>", true),
		Entry("LAST_LANGUAGE", "<<LAST_LANGUAGE>>", true),
		// Any future <<...>> token is covered by shape alone.
		Entry("unknown future token", "<<LAST_MACHINE>>", true),
		Entry("empty inner is still a placeholder, never a real value", "<<>>", true),
		// Real values must NOT be treated as placeholders.
		Entry("real project name", "boomtime", false),
		Entry("empty string", "", false),
		Entry("only opening delimiter", "<<LAST_PROJECT", false),
		Entry("only closing delimiter", "LAST_PROJECT>>", false),
		Entry("angle brackets mid-string", "a<<b>>c", false),
		Entry("single-char shy of a delimiter", "<>", false),
	)
})

// -- restored from internal/wakatime/wakatime_test.go during kill-switch (boom-0vp.17) --
func strptr(s string) *string { return &s }
