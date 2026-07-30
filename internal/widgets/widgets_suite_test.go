package widgets

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWidgetsSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/widgets suite")
}
