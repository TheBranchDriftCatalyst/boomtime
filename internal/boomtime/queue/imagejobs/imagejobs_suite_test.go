package imagejobs

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestImagejobsSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/queue/imagejobs suite")
}
