// labels_suite_test.go — ginkgo entry point for the `labels` package
// (gaka-tst-ginkgo). Every _test.go file in this package is a spec file
// (`var _ = Describe(...)` at package init) — this file is the single
// `func TestXxx(t *testing.T)` handoff into the ginkgo runner.
//
// Running:
//   go test ./internal/labels/...           # runs everything
//   go test ./internal/labels/... -v        # nested spec tree in output
//   go test ./internal/labels/... -ginkgo.focus="axis-time"  # filter
//
// See docs/testing/ginkgo.md for the migration guide and the epic
// tracking the stdlib → ginkgo conversion (gaka-tst-ginkgo).

package labels

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLabelsSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/labels suite")
}
