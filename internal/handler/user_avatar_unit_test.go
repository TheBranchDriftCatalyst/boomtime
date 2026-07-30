// user_avatar_unit_test.go — gaka-d6x.handler: in-package unit tests for
// user_avatar.go helpers that aren't reachable via the HTTP surface.
//
// Named invariant:
//
//	"buildAvatarUserMessage renders both branches deterministically" —
//	the empty-topLabels branch injects "NEW OPERATOR", and the populated
//	branch comma-joins the labels. The synopsis is trimmed. Pure function,
//	pinned so a downstream prompt author never sees an accidentally-blank
//	profile string.
package handler

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("buildAvatarUserMessage", func() {
	It("renders 'NEW OPERATOR' when topLabels is empty and omits an empty synopsis", func() {
		got := buildAvatarUserMessage(avatarSynthReq{})
		Expect(got).To(ContainSubstring("NEW OPERATOR"),
			"empty labels branch missing the NEW OPERATOR sentinel: %q", got)
		Expect(got).NotTo(ContainSubstring("- activity synopsis:"),
			"empty synopsis should not render its line: %q", got)
		Expect(strings.Count(got, "\n")).To(BeNumerically(">", 1),
			"message should be multi-line even in the empty case")
	})

	It("comma-joins topLabels and trims the synopsis", func() {
		got := buildAvatarUserMessage(avatarSynthReq{
			TopLabels: []string{"PYTHON MASTER", "NIGHT OWL"},
			Synopsis:  "  night owl · 6h/day  ",
		})
		Expect(got).To(ContainSubstring("PYTHON MASTER, NIGHT OWL"),
			"labels not comma-joined: %q", got)
		Expect(got).To(ContainSubstring("night owl · 6h/day"),
			"synopsis missing or not trimmed: %q", got)
		Expect(got).NotTo(ContainSubstring("NEW OPERATOR"),
			"populated branch must not fall back to NEW OPERATOR: %q", got)
	})
})
