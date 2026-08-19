// suite_test.go — Ginkgo suite entry point for the boomtime-admin domain
// (both internal `package admin` tests and external `package admin_test` specs run
// under this one bootstrapper). The label-images + wakatime.com import admin suites
// moved here from internal/admin (gaka-zp2s); this is their RunSpecs entry.
package admin

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBoomtimeAdminSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/boomtime/admin suite")
}
