package comfyui

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestComfyuiSuite(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "internal/comfyui suite")
}
