// awards_coverage_ginkgo_test.go — ginkgo mirror of awards_coverage_test.go
// (gaka-hc6.6).
//
// The stdlib version uses a runtime-driven `for _, row := range dbRows { t.Run
// (row.ID, ...) }`, which reads the label catalog from Postgres. Ginkgo
// requires specs to be registered at compile time, before any test setup
// runs — we cannot fan out one `It` per catalog row that way. The mirror
// therefore packs the entire sweep into ONE It that iterates internally and
// uses Fail() / Expect() for reporting. On the plus side, spec output for
// every label is captured via GinkgoWriter so a failure names the exact
// label. Coverage semantics (skipped/ran/fired counts, per-label diagnostic
// dump on miss) match the stdlib version exactly.
//
// 1:1 case map (1 stdlib TestXxx):
//   TestLabelCoverage → label coverage sweep > "every catalog label fires against its minimum-viable seed"
package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/labels"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("label coverage sweep (gaka-hc6.6)", func() {
	It("every catalog label fires against its minimum-viable seed", func() {
		if testing.Short() {
			Skip("coverage sweep is expensive; -short skips it")
		}
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "labelcovg"))
		e := hz.Router()
		ctx := context.Background()

		dbRows, err := hz.DB.ListLabels(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(dbRows).NotTo(BeEmpty(),
			"catalog empty — check migrations 00036/00039/00040/00043")

		skipped, ran, fired := 0, 0, 0
		var failed []string

		for _, row := range dbRows {
			spec, err := labels.SpecFromDBRow(labels.DBRow{
				ID:            row.ID,
				Kind:          row.Kind,
				Label:         row.Label,
				Glyph:         row.Glyph,
				Description:   row.Description,
				Rank:          row.Rank,
				Tier:          row.Tier,
				PeriodDefault: row.PeriodDefault,
				Condition:     row.Condition,
			})
			Expect(err).NotTo(HaveOccurred(), "decode spec for %s", row.ID)

			// synthesize is package-shared with awards_coverage_test.go — same
			// dispatch, same semantics.
			r := synthesize(spec.Condition, "")
			if r.skip != "" {
				skipped++
				GinkgoWriter.Printf("SKIP %s: %s\n", row.ID, r.skip)
				continue
			}
			ran++

			username, token := hz.MintUser("covg" + strings.ReplaceAll(row.ID, "-", ""))
			sd := hz.Seeder(username).Projects("covproj")
			for _, hb := range r.beats {
				sd.Seed(hb)
			}
			Expect(hz.DB.RefreshRollup(ctx, username, r.at.Add(-24*time.Hour))).
				To(Succeed(), "RefreshRollup for %s", row.ID)

			rec := getJSONG(e, "/api/v1/users/current/awards", token)
			if rec.Code != http.StatusOK {
				failed = append(failed,
					fmt.Sprintf("%s: GET /awards status=%d body=%s", row.ID, rec.Code, rec.Body.String()))
				continue
			}
			var awards []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &awards); err != nil {
				failed = append(failed, fmt.Sprintf("%s: decode: %v body=%s", row.ID, err, rec.Body.String()))
				continue
			}
			if !containsAwardIDG(awards, row.ID) {
				// Diagnostic dump so miss reads as "synth wrong data" vs
				// "evaluator missed condition".
				statsRec := getJSONG(e, "/api/v1/users/current/stats", token)
				var stats map[string]any
				_ = json.Unmarshal(statsRec.Body.Bytes(), &stats)
				var diagAxes []string
				for _, ax := range []string{"projects", "languages", "editors", "platforms", "categories"} {
					if arr, ok := stats[ax].([]any); ok && len(arr) > 0 {
						var entries []string
						for _, p := range arr {
							if m, ok := p.(map[string]any); ok {
								entries = append(entries, fmt.Sprintf("%v=%v", m["name"], m["totalSeconds"]))
							}
						}
						diagAxes = append(diagAxes, ax+": ["+strings.Join(entries, ", ")+"]")
					}
				}
				failed = append(failed,
					fmt.Sprintf("%s did NOT fire.\n  condition: %+v\n  stats: %s\n  awards fired: %v",
						row.ID, spec.Condition,
						strings.Join(diagAxes, "\n         "), awardIDsG(awards)))
				continue
			}
			fired++
		}

		GinkgoWriter.Printf("label coverage summary: %d ran, %d fired, %d skipped (of %d total)\n",
			ran, fired, skipped, len(dbRows))
		if len(failed) > 0 {
			Fail("label coverage failures:\n" + strings.Join(failed, "\n---\n"))
		}
	})
})
