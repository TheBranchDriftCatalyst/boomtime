// db_suite_test.go — entry point for the internal/db ginkgo suite.
//
// Note (name collision): package db exports its own `Label` type (see
// labels.go). Ginkgo v2 also exports a top-level `Label` decorator, so a
// dot-import of ginkgo shadows / collides with the package's Label type in
// every _test.go file. We work around this by using a NAMED import for
// ginkgo throughout the ginkgo mirror files (prefix uses with `ginkgo.`)
// while keeping gomega dot-imported.
package db

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDBSuite(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "internal/db suite")
}
