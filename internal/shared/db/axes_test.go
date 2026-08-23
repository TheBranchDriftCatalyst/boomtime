// axes_ginkgo_test.go — ginkgo mirror of axes_test.go (boom-0vp.13).
// 1:1 case map (1 stdlib TestXxx → 1 It):
//
//	TestAxisRegistryDerivations → "pins every registry-derived value to its literal"
package db

import (
	"reflect"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("axis registry derivations", func() {
	ginkgo.It("pins every registry-derived value to its hand-written literal", func() {
		wantHiddenAxes := []string{
			"project", "language", "editor", "plugin", "machine", "platform", "branch", "category",
		}
		Expect(reflect.DeepEqual(hiddenAxes, wantHiddenAxes)).To(BeTrue(), "hiddenAxes = %v, want %v", hiddenAxes, wantHiddenAxes)

		wantRawHeartbeatCols := map[string]string{
			"project": "project", "language": "language", "editor": "editor",
			"plugin": "plugin", "machine": "machine", "platform": "platform",
			"branch": "branch", "category": "category",
		}
		Expect(reflect.DeepEqual(rawHeartbeatCols, wantRawHeartbeatCols)).To(BeTrue(), "rawHeartbeatCols = %v, want %v", rawHeartbeatCols, wantRawHeartbeatCols)

		wantRollupAxes := map[string]bool{
			"project": true, "language": true, "editor": true, "platform": true, "machine": true,
			"plugin": true, "branch": true, "category": true,
		}
		Expect(reflect.DeepEqual(RollupAxes, wantRollupAxes)).To(BeTrue(), "RollupAxes = %v, want %v", RollupAxes, wantRollupAxes)

		wantRollupCols := map[string]string{
			"project": "project", "language": "language", "editor": "editor",
			"platform": "platform", "machine": "machine",
			"plugin": "plugin", "branch": "branch", "category": "category",
		}
		Expect(reflect.DeepEqual(rollupCols, wantRollupCols)).To(BeTrue(), "rollupCols = %v, want %v", rollupCols, wantRollupCols)

		wantProjectListCols := map[string]string{
			"project": "heartbeats.project", "language": "heartbeats.language",
			"editor": "heartbeats.editor", "plugin": "heartbeats.plugin",
			"machine": "heartbeats.machine", "platform": "heartbeats.platform",
			"branch": "heartbeats.branch", "category": "heartbeats.category",
		}
		Expect(reflect.DeepEqual(projectListCols, wantProjectListCols)).To(BeTrue(), "projectListCols = %v, want %v", projectListCols, wantProjectListCols)

		// exploreColumns is a superset: every registry axis with its raw column, plus
		// the audit-only axes.
		wantExploreColumns := map[string]string{
			"day":       "time_sent::date",
			"project":   "project",
			"language":  "language",
			"editor":    "editor",
			"plugin":    "plugin",
			"platform":  "platform",
			"machine":   "machine",
			"branch":    "branch",
			"category":  "category",
			"type":      "ty",
			"entity":    "entity",
			"isWrite":   "is_write",
			"userAgent": "user_agent",
		}
		Expect(reflect.DeepEqual(exploreColumns, wantExploreColumns)).To(BeTrue(), "exploreColumns = %v, want %v", exploreColumns, wantExploreColumns)
	})
})
