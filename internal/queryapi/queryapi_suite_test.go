// queryapi_suite_test.go — ginkgo entrypoint for the queryapi domain package
// (gaka-174.q). External test package; drives the POST /api/v1/query endpoint
// through the shared testutil.Harness router against the isolated test DB.
package queryapi_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQueryAPIExternal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "queryapi (external)")
}
