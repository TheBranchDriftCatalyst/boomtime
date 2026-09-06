// export_test.go — in-package test seam (boom-gsnv).
//
// The commit-report attribution helpers are unexported, but the regression
// test that pins them has to be DB-backed and therefore lives in the EXTERNAL
// stats_test package (internal/shared/testutil imports internal/boomtime/stats,
// so an in-package test cannot import the harness without an import cycle).
// This file is compiled into the stats package under test and re-exports the
// two helpers to stats_test — the standard export_test.go pattern.
package stats

import (
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// CommitGapWindowsForTest re-exports commitGapWindows.
func CommitGapWindowsForTest(username, project string, commits []model.CommitPayload) ([]string, []string, []time.Time, []time.Time) {
	return commitGapWindows(username, project, commits)
}

// AttributeCommitSecondsForTest re-exports attributeCommitSeconds.
func AttributeCommitSecondsForTest(commits []model.CommitPayload, timeSpent []int64) (map[string]model.CommitPayload, error) {
	return attributeCommitSeconds(commits, timeSpent)
}
