// admin_suite_test.go — Ginkgo suite entry point for the admin domain
// (both internal `package admin` tests and external `package admin_test`
// specs run under this one TestAdminSuite bootstrapper). Mirrors the
// meta / spaces / goals / widgets / identity / awards / ingest /
// curation / stats suite files.
package admin

import (
	"testing"

	// boom-zp2s: the domain-coupled CLI-runner commands ("hardcover dedup-reads",
	// "backfill github-stats") register into climeta's allowlist from the books /
	// github packages' init(). The cli_http suite drives them end-to-end, so pull
	// those domains in test-side to trigger registration (production stays
	// domain-clean: shared/admin imports only the domain-free climeta framework).
	_ "github.com/TheBranchDriftCatalyst/boomtime/internal/books"
	_ "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/github"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAdminSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/admin suite")
}
