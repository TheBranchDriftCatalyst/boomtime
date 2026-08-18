package labelimages

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLabelimagesSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/worker/labelimages suite")
}
