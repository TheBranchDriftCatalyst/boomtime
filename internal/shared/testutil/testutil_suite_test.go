package testutil_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTestutilSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/testutil suite")
}
