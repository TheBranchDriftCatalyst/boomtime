package labels

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateCondition (boom-6uf)", func() {
	Describe("accepts every well-formed primitive", func() {
		DescribeTable("valid primitives round-trip",
			func(raw string) {
				Expect(ValidateCondition(json.RawMessage(raw))).To(Succeed(),
					"expected to accept: %s", raw)
			},
			Entry("axis-time",
				`{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":100}`),
			Entry("axis-time-sum",
				`{"kind":"axis-time-sum","axis":"editors","values":["vim","neovim"],"op":">=","hours":50}`),
			Entry("axis-pct",
				`{"kind":"axis-pct","axis":"projects","value":"boomtime","op":">=","pct":0.5}`),
			Entry("top-share",
				`{"kind":"top-share","axis":"languages","op":">=","pct":0.4}`),
			Entry("distinct-count",
				`{"kind":"distinct-count","axis":"languages","minHoursEach":20,"op":">=","n":5}`),
			Entry("punchcard-hour-pct",
				`{"kind":"punchcard-hour-pct","hoursIn":[0,1,2,3,4,5],"op":">=","pct":0.3}`),
			Entry("punchcard-dow-pct",
				`{"kind":"punchcard-dow-pct","dowIn":[0,6],"op":">=","pct":0.2}`),
			Entry("streak current",
				`{"kind":"streak","which":"current","op":">=","days":7}`),
			Entry("streak longest",
				`{"kind":"streak","which":"longest","op":">=","days":30}`),
			Entry("daily-avg",
				`{"kind":"daily-avg","op":">=","hours":4}`),
			Entry("trend",
				`{"kind":"trend","window":"last7-vs-prior7","op":">=","ratio":1.2}`),
		)
	})

	Describe("accepts composers", func() {
		It("all with one primitive child", func() {
			raw := `{"kind":"all","of":[{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":10}]}`
			Expect(ValidateCondition(json.RawMessage(raw))).To(Succeed())
		})
		It("any with mixed primitives", func() {
			raw := `{"kind":"any","of":[
				{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":10},
				{"kind":"streak","which":"longest","op":">=","days":5}
			]}`
			Expect(ValidateCondition(json.RawMessage(raw))).To(Succeed())
		})
		It("not around a primitive", func() {
			raw := `{"kind":"not","of":{"kind":"daily-avg","op":">=","hours":2}}`
			Expect(ValidateCondition(json.RawMessage(raw))).To(Succeed())
		})
		It("all → any → primitive (depth 3)", func() {
			raw := `{"kind":"all","of":[
				{"kind":"any","of":[
					{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":10}
				]}
			]}`
			Expect(ValidateCondition(json.RawMessage(raw))).To(Succeed())
		})
	})

	Describe("rejects malformed input with path + reason", func() {
		DescribeTable("rejections",
			func(raw, wantPath, wantMsgSubstr string) {
				err := ValidateCondition(json.RawMessage(raw))
				Expect(err).To(HaveOccurred(), "expected rejection of: %s", raw)
				ve, ok := err.(*ValidationError)
				Expect(ok).To(BeTrue(), "expected *ValidationError, got %T: %v", err, err)
				Expect(ve.Path).To(Equal(wantPath),
					"path mismatch for input %s: got %q want %q", raw, ve.Path, wantPath)
				Expect(strings.Contains(ve.Message, wantMsgSubstr)).To(BeTrue(),
					"message %q should contain %q", ve.Message, wantMsgSubstr)
			},
			Entry("empty body", ``, "", "empty"),
			Entry("not an object", `"nope"`, "", "not a JSON object"),
			Entry("missing kind", `{}`, "/kind", "missing discriminator"),
			Entry("unknown kind", `{"kind":"bogus"}`, "/kind", `unknown kind "bogus"`),
			Entry("axis-time wrong op",
				`{"kind":"axis-time","axis":"languages","value":"go","op":"===","hours":5}`,
				"/op", "op must be one of >=|<="),
			Entry("axis-time wrong axis",
				`{"kind":"axis-time","axis":"machines","value":"laptop","op":">=","hours":5}`,
				"/axis", "axis must be one of"),
			Entry("axis-time missing value",
				`{"kind":"axis-time","axis":"languages","value":"","op":">=","hours":5}`,
				"/value", "requires a non-empty `value`"),
			Entry("axis-time zero hours",
				`{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":0}`,
				"/hours", "requires `hours` > 0"),
			Entry("axis-time-sum empty values",
				`{"kind":"axis-time-sum","axis":"editors","values":[],"op":">=","hours":5}`,
				"/values", "non-empty `values`"),
			Entry("axis-time-sum empty value inside",
				`{"kind":"axis-time-sum","axis":"editors","values":["vim",""],"op":">=","hours":5}`,
				"/values/1", "must be non-empty"),
			Entry("axis-pct pct > 1 (percent scale mistake)",
				`{"kind":"axis-pct","axis":"projects","value":"x","op":">=","pct":50}`,
				"/pct", "DSL uses 0..1"),
			Entry("axis-pct negative pct",
				`{"kind":"axis-pct","axis":"projects","value":"x","op":">=","pct":-0.1}`,
				"/pct", "0..1"),
			Entry("distinct-count zero n",
				`{"kind":"distinct-count","axis":"languages","minHoursEach":10,"op":">=","n":0}`,
				"/n", "> 0"),
			Entry("punchcard-hour-pct hour out of range",
				`{"kind":"punchcard-hour-pct","hoursIn":[24],"op":">=","pct":0.1}`,
				"/hoursIn/0", "out of range [0,23]"),
			Entry("punchcard-hour-pct empty hoursIn",
				`{"kind":"punchcard-hour-pct","hoursIn":[],"op":">=","pct":0.1}`,
				"/hoursIn", "non-empty"),
			Entry("punchcard-dow-pct dow=7",
				`{"kind":"punchcard-dow-pct","dowIn":[7],"op":">=","pct":0.1}`,
				"/dowIn/0", "out of range [0,6]"),
			Entry("streak wrong which",
				`{"kind":"streak","which":"average","op":">=","days":5}`,
				"/which", `"current" or "longest"`),
			Entry("streak zero days",
				`{"kind":"streak","which":"current","op":">=","days":0}`,
				"/days", "> 0"),
			Entry("trend wrong window",
				`{"kind":"trend","window":"month-vs-month","op":">=","ratio":1}`,
				"/window", "last7-vs-prior7"),
			Entry("all with empty `of`",
				`{"kind":"all","of":[]}`,
				"/of", "non-empty `of`"),
			Entry("nested reject: `all` containing an axis-time with bad axis reports subpath",
				`{"kind":"all","of":[{"kind":"axis-time","axis":"branches","value":"main","op":">=","hours":1}]}`,
				"/of/0/axis", "axis must be one of"),
			Entry("nested reject inside `not`",
				`{"kind":"not","of":{"kind":"daily-avg","op":">=","hours":0}}`,
				"/of/hours", "> 0"),
		)
	})

	Describe("enforces depth cap", func() {
		It("accepts exactly MaxConditionDepth composers", func() {
			// 5 nested composers, each with a valid primitive child.
			// Depth counts composers; depth 5 = all→all→all→all→all→primitive.
			inner := `{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":1}`
			nested := inner
			for i := 0; i < MaxConditionDepth; i++ {
				nested = `{"kind":"all","of":[` + nested + `]}`
			}
			Expect(ValidateCondition(json.RawMessage(nested))).To(Succeed(),
				"depth of exactly MaxConditionDepth must be accepted")
		})
		It("rejects one composer past the cap", func() {
			inner := `{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":1}`
			nested := inner
			for i := 0; i < MaxConditionDepth+1; i++ {
				nested = `{"kind":"any","of":[` + nested + `]}`
			}
			err := ValidateCondition(json.RawMessage(nested))
			Expect(err).To(HaveOccurred())
			ve, ok := err.(*ValidationError)
			Expect(ok).To(BeTrue())
			Expect(ve.Message).To(ContainSubstring("depth exceeds cap"))
		})
	})
})
