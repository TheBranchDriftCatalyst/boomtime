package apierr

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestApierrSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/apierr suite")
}
