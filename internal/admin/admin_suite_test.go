// admin_suite_test.go — Ginkgo suite entry point for the admin domain
// (both internal `package admin` tests and external `package admin_test`
// specs run under this one TestAdminSuite bootstrapper). Mirrors the
// meta / spaces / goals / widgets / identity / awards / ingest /
// curation / stats suite files.
package admin

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAdminSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/admin suite")
}
