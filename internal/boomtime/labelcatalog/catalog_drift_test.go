// catalog_drift_ginkgo_test.go — ginkgo mirror of catalog_drift_test.go (gaka-0vp).
// 1:1 case map (3 stdlib TestXxx):
//
//	TestCatalogDrift_TSPromptedIDsReported → drift > "informational: TS-prompted IDs"
//	TestCatalogDrift_GoIDsExistInTS        → drift > "Go IDs exist in TS"
//	TestCatalogDrift_NoDuplicateGoIDs      → drift > "no duplicate Go IDs"
//
// Note: since gaka-hc6.5 deleted the TS catalog, the first two Its
// currently Skip in normal runs — same behavior as the stdlib version.
// The third It (duplicate check) still runs.
package labelcatalog

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// resolveTSCatalogPath is the ginkgo equivalent of catalogTSPath — Skips
// the current spec when the TS catalog is missing.
func resolveTSCatalogPath() string {
	rel, err := filepath.Abs("../../web/shared/features/publicprofile/labels/catalog.ts")
	if err != nil {
		ginkgo.Skip("cannot resolve TS catalog path: " + err.Error())
	}
	if _, err := os.Stat(rel); err != nil {
		ginkgo.Skip("TS catalog not found at " + rel)
	}
	return rel
}

var (
	tsIDReGinkgo          = regexp.MustCompile(`(?m)^\s*id:\s*["']([a-zA-Z0-9\-]+)["']`)
	tsImagePromptReGinkgo = regexp.MustCompile(`imagePrompt\s*:`)
)

var _ = ginkgo.Describe("labelcatalog drift", func() {

	// Informational: log TS-prompted IDs beyond the Go baseline.
	ginkgo.It("reports TS-prompted IDs not in the Go baseline (informational)", func() {
		path := resolveTSCatalogPath()
		src, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())

		idMatches := tsIDReGinkgo.FindAllStringSubmatchIndex(string(src), -1)
		if len(idMatches) == 0 {
			ginkgo.Skip("no id: matches in catalog.ts — grammar may have changed")
		}

		goIDs := map[string]bool{}
		for _, e := range Entries {
			goIDs[e.ID] = true
		}

		srcStr := string(src)
		var beyondBaseline []string
		for _, m := range idMatches {
			id := srcStr[m[2]:m[3]]
			windowStart := m[0]
			windowEnd := m[1] + 800
			if windowEnd > len(srcStr) {
				windowEnd = len(srcStr)
			}
			if !tsImagePromptReGinkgo.MatchString(srcStr[windowStart:windowEnd]) {
				continue
			}
			if !goIDs[id] {
				beyondBaseline = append(beyondBaseline, id)
			}
		}
		if len(beyondBaseline) > 0 {
			ginkgo.GinkgoWriter.Printf("info: %d TS-prompted label(s) not in Go baseline: %v\n",
				len(beyondBaseline), beyondBaseline)
		}
	})

	ginkgo.It("every Go ID exists in the TS catalog", func() {
		path := resolveTSCatalogPath()
		src, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())

		tsIDs := map[string]bool{}
		for _, m := range tsIDReGinkgo.FindAllStringSubmatch(string(src), -1) {
			tsIDs[m[1]] = true
		}
		for _, e := range Entries {
			Expect(tsIDs).To(HaveKey(e.ID),
				"Go labelcatalog.Entries has %q but TS catalog.ts does not — remove the Go entry or add the TS spec",
				e.ID)
		}
	})

	ginkgo.It("has no duplicate Go IDs", func() {
		seen := map[string]bool{}
		for _, e := range Entries {
			Expect(seen).NotTo(HaveKey(e.ID),
				"duplicate id %q in labelcatalog.Entries", e.ID)
			seen[e.ID] = true
		}
	})
})
