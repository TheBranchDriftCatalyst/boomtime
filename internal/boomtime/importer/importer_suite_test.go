package importer

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestImporterSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/importer suite")
}
