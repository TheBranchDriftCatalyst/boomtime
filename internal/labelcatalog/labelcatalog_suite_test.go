package labelcatalog

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLabelcatalogSuite(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "internal/labelcatalog suite")
}
