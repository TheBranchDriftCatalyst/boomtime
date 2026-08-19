package server

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestServerSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/server suite")
}
