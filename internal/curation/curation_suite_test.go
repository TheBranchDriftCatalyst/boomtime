// curation_suite_test.go — Ginkgo suite entry point for the curation
// domain (both internal `package curation` tests and external
// `package curation_test` specs run under this one TestCurationSuite
// bootstrapper). Mirrors the identity / awards / widgets / ingest
// suite files.
package curation

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCurationSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/curation suite")
}
