package db

import (
	"reflect"
	"testing"
)

// TestAxisRegistryDerivations pins every registry-derived value to the exact
// hand-written literal it replaced, so an axes.go edit that would silently
// change SQL column mapping (or predicate ordering) fails loudly.
func TestAxisRegistryDerivations(t *testing.T) {
	wantHiddenAxes := []string{
		"project", "language", "editor", "plugin", "machine", "platform", "branch", "category",
	}
	if !reflect.DeepEqual(hiddenAxes, wantHiddenAxes) {
		t.Fatalf("hiddenAxes = %v, want %v", hiddenAxes, wantHiddenAxes)
	}

	wantRawHeartbeatCols := map[string]string{
		"project": "project", "language": "language", "editor": "editor",
		"plugin": "plugin", "machine": "machine", "platform": "platform",
		"branch": "branch", "category": "category",
	}
	if !reflect.DeepEqual(rawHeartbeatCols, wantRawHeartbeatCols) {
		t.Fatalf("rawHeartbeatCols = %v, want %v", rawHeartbeatCols, wantRawHeartbeatCols)
	}

	wantRollupAxes := map[string]bool{
		"project": true, "language": true, "editor": true, "platform": true, "machine": true,
	}
	if !reflect.DeepEqual(RollupAxes, wantRollupAxes) {
		t.Fatalf("RollupAxes = %v, want %v", RollupAxes, wantRollupAxes)
	}

	wantRollupCols := map[string]string{
		"project": "project", "language": "language", "editor": "editor",
		"platform": "platform", "machine": "machine",
	}
	if !reflect.DeepEqual(rollupCols, wantRollupCols) {
		t.Fatalf("rollupCols = %v, want %v", rollupCols, wantRollupCols)
	}

	wantProjectListCols := map[string]string{
		"project": "heartbeats.project", "language": "heartbeats.language",
		"editor": "heartbeats.editor", "plugin": "heartbeats.plugin",
		"machine": "heartbeats.machine", "platform": "heartbeats.platform",
		"branch": "heartbeats.branch", "category": "heartbeats.category",
	}
	if !reflect.DeepEqual(projectListCols, wantProjectListCols) {
		t.Fatalf("projectListCols = %v, want %v", projectListCols, wantProjectListCols)
	}

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
	if !reflect.DeepEqual(exploreColumns, wantExploreColumns) {
		t.Fatalf("exploreColumns = %v, want %v", exploreColumns, wantExploreColumns)
	}
}
