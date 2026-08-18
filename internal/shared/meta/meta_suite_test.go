package meta_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMetaSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/meta suite")
}
