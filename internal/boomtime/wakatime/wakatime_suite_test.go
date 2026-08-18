package wakatime

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWakatimeSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/wakatime suite")
}
