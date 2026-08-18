package logging

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLoggingSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/logging suite")
}
