// labels_helpers_test.go — in-package (package handler) unit tests for the
// tiny stateless helpers in labels.go. Complements labels_http_test.go
// (external ginkgo, exercises full request/response) with pure-function
// checks that ARE NOT roundtrip-trivial.
//
// Pinned invariants:
//
//   - sqlStr: doubles internal single quotes so INSERT dumps stay valid
//   - sqlStr: never emits NULL (always renders quoted string)
//   - sqlStrOrNull: empty => NULL; non-empty => sqlStr
//   - applyLabelBody: nil pointer preserves target; non-nil overwrites
//     (even with empty string — "" is a valid clear)
//   - applyLabelBody: Condition (raw JSON) only overwrites when len>0 so a
//     partial PATCH that omits Condition doesn't zero it out
package handler

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

var _ = Describe("sqlStr (labels.go)", func() {
	It("wraps a plain string in single quotes without escaping", func() {
		Expect(sqlStr("hello")).To(Equal("'hello'"))
	})
	It("doubles single quotes inside the value", func() {
		Expect(sqlStr("it's")).To(Equal("'it''s'"),
			"single quote must be doubled — otherwise the emitted SQL breaks")
	})
	It("preserves empty string as '' rather than NULL (delegated by sqlStrOrNull)", func() {
		Expect(sqlStr("")).To(Equal("''"))
	})
	It("handles a value containing MULTIPLE quotes", func() {
		Expect(sqlStr("a'b'c")).To(Equal("'a''b''c'"))
	})
})

var _ = Describe("sqlStrOrNull (labels.go)", func() {
	It("returns the literal NULL when the input is empty", func() {
		Expect(sqlStrOrNull("")).To(Equal("NULL"),
			"empty string must become NULL so the dump preserves the migration's NULLIF pattern")
	})
	It("delegates to sqlStr for a non-empty value", func() {
		Expect(sqlStrOrNull("Coding")).To(Equal("'Coding'"))
	})
	It("delegates escaping to sqlStr for a value with a quote", func() {
		Expect(sqlStrOrNull("O'Reilly")).To(Equal("'O''Reilly'"))
	})
})

var _ = Describe("applyLabelBody (labels.go)", func() {
	It("preserves ALL fields when every pointer in the body is nil", func() {
		orig := db.Label{
			ID:              "id1",
			Kind:            "tier",
			Label:           "Python",
			Glyph:           "P",
			Description:     "A snake",
			OptimizedPrompt: "an elegant python",
			Rank:            5,
			Tier:            "novice",
			Condition:       json.RawMessage(`{"kind":"axis-time"}`),
		}
		into := orig
		applyLabelBody(&into, &labelBody{})
		Expect(into).To(Equal(orig), "empty body must not touch any field")
	})

	It("overwrites only the fields whose pointer is non-nil", func() {
		into := db.Label{
			Kind: "tier", Label: "Original", Glyph: "O", Rank: 1, Tier: "novice",
			Condition: json.RawMessage(`{"a":1}`),
		}
		newKind := "categories"
		newRank := 42
		applyLabelBody(&into, &labelBody{Kind: &newKind, Rank: &newRank})
		Expect(into.Kind).To(Equal("categories"))
		Expect(into.Rank).To(Equal(42))
		Expect(into.Label).To(Equal("Original"), "Label was nil in body — must be preserved")
		Expect(into.Glyph).To(Equal("O"), "Glyph was nil in body — must be preserved")
	})

	It("allows clearing a string field via non-nil empty string ('' is a legitimate clear)", func() {
		into := db.Label{Glyph: "keep-me"}
		empty := ""
		applyLabelBody(&into, &labelBody{Glyph: &empty})
		Expect(into.Glyph).To(Equal(""),
			"empty-string via non-nil pointer must clear — required so admin can remove a glyph")
	})

	It("does NOT overwrite Condition when the incoming RawMessage is empty (len==0)", func() {
		into := db.Label{Condition: json.RawMessage(`{"kept":true}`)}
		applyLabelBody(&into, &labelBody{Condition: json.RawMessage{}})
		Expect(string(into.Condition)).To(Equal(`{"kept":true}`),
			"empty raw JSON must be treated as 'no change' so PATCHes that omit condition don't zero the field")
	})

	It("DOES overwrite Condition when the incoming RawMessage is non-empty", func() {
		into := db.Label{Condition: json.RawMessage(`{"old":true}`)}
		applyLabelBody(&into, &labelBody{Condition: json.RawMessage(`{"new":42}`)})
		Expect(string(into.Condition)).To(Equal(`{"new":42}`))
	})
})
