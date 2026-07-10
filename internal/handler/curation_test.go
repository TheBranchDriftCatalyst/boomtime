package handler

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
)

// TestCurationAxisWhitelist confirms the create handler shares the Heartbeats
// Explorer whitelist: valid axes resolve, invalid ones do not. (The handler
// returns 400 for any axis where db.ExploreColumn is false.)
func TestCurationAxisWhitelist(t *testing.T) {
	valid := []string{"project", "language", "editor", "plugin", "platform", "machine", "branch", "category", "type", "entity", "day"}
	for _, a := range valid {
		if _, ok := db.ExploreColumn(a); !ok {
			t.Fatalf("axis %q should be whitelisted for curation", a)
		}
	}
	invalid := []string{"sender", "id", "ty", "is_write", "time_sent", "", "DROP TABLE"}
	for _, a := range invalid {
		if _, ok := db.ExploreColumn(a); ok {
			t.Fatalf("axis %q should be rejected by the curation whitelist", a)
		}
	}
}

func TestCurationActionConstants(t *testing.T) {
	if db.CurationHide != "hide" || db.CurationRename != "rename" {
		t.Fatalf("action constants drifted: hide=%q rename=%q", db.CurationHide, db.CurationRename)
	}
}
