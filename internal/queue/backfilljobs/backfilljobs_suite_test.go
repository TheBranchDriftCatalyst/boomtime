package backfilljobs

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBackfilljobsSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/queue/backfilljobs suite")
}
