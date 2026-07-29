package auth

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAuthSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/auth suite")
}
