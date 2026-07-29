package stats

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStatsSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/stats suite")
}
