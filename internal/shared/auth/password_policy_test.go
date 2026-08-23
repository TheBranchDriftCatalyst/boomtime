// password_policy_ginkgo_test.go — ginkgo mirror of password_policy_test.go (boom-0vp).
// 1:1 case map (2 stdlib TestXxx):
//
//	TestValidatePassword                       → ValidatePassword > DescribeTable of 7 named cases
//	TestValidatePassword_ErrorMessagesUserSafe → sentinel errors > "user-safe messages"
package auth

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidatePassword (rune-aware, unicode-letter)", func() {
	DescribeTable("pw → error",
		func(pw string, wantErr error) {
			got := ValidatePassword(pw)
			if wantErr == nil {
				Expect(got).To(BeNil())
				return
			}
			Expect(got).NotTo(BeNil())
			Expect(errors.Is(got, wantErr)).To(BeTrue(),
				"ValidatePassword(%q) = %v, want errors.Is == %v", pw, got, wantErr)
		},
		// rejects
		Entry("empty → too short", "", ErrPasswordTooShort),
		Entry("7 ASCII digits → too short", "1234567", ErrPasswordTooShort),
		Entry("8 ASCII letters → no digit", "aaaaaaaa", ErrPasswordNoDigit),
		Entry("8 ASCII digits → no letter", "12345678", ErrPasswordNoLetter),
		Entry("4 runes / 8 bytes CJK — must reject (boom-e5e regression)",
			"日本1a", ErrPasswordTooShort),
		// accepts
		Entry("7 letters + 1 digit (minimum viable)", "aaaaaaa1", nil),
		Entry("'password1' meets policy", "password1", nil),
		Entry("8 runes mixed script", "日本語1abcd", nil),
	)
})

var _ = Describe("password policy sentinel errors", func() {
	It("have short, user-safe messages", func() {
		for _, err := range []error{ErrPasswordTooShort, ErrPasswordNoLetter, ErrPasswordNoDigit} {
			msg := err.Error()
			Expect(msg).NotTo(BeEmpty())
			Expect(len(msg)).To(BeNumerically("<=", 120),
				"sentinel error too long (%d chars): %q", len(msg), msg)
		}
	})
})
