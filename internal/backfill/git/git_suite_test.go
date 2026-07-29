package git

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/backfill/git suite")
}
