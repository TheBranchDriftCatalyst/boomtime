package openapi_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOpenapiSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/openapi suite")
}
