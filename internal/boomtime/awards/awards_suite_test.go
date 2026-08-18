package awards_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAwardsSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/awards suite")
}
