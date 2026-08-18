package model

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/model suite")
}
