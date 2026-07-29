package widget

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWidgetSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/widget suite")
}
