package identity

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIdentitySuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/identity suite")
}
