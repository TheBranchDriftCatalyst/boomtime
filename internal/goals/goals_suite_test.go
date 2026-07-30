// goals_suite_test.go — ginkgo entrypoint for the goals domain package
// (gaka-8tn phase 2b). External test package variant; the internal
// package_goals variant lives in goals_internal_suite_test.go.
package goals_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGoalsExternal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "goals (external)")
}
