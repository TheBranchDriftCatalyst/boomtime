// ingest_suite_test.go — Ginkgo suite entry point for the ingest domain
// (both internal `package ingest` tests and external `package ingest_test`
// specs run under this one TestIngestSuite bootstrapper). Mirrors the
// identity / awards / widgets suite files.
package ingest

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIngestSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/ingest suite")
}
