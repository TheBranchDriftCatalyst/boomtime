// badges_test.go: unit coverage for the badge-endpoint public-safe policy
// (bd gaka-6jm.3). The full HTTP path requires a DB — that is exercised by
// integration tests. Here we just pin the pure decision helper.
package handler

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// TestApplyBadgeCuration_HiddenProject: a project on the user's hide list
// resolves to "hidden", which the handler translates to a 404 (a badge whose
// subject is curated away has no scrubbed representation — it must not render
// the shields.io label or leak activity time).
func TestApplyBadgeCuration_HiddenProject(t *testing.T) {
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	if got := applyBadgeCuration(hidden, "hakatime"); got != "hidden" {
		t.Errorf("applyBadgeCuration(hidden, %q) = %q, want %q", "hakatime", got, "hidden")
	}
	// Case-insensitive: db.LoadHiddenSets lowercases match_value before storing,
	// and exclusionPredicate compares via `lower(col)` — the badge check must
	// match that contract.
	if got := applyBadgeCuration(hidden, "HAKATIME"); got != "hidden" {
		t.Errorf("applyBadgeCuration(hidden, %q) case-insensitive check failed: got %q", "HAKATIME", got)
	}
}

// TestApplyBadgeCuration_VisibleProject: non-hidden projects pass through.
func TestApplyBadgeCuration_VisibleProject(t *testing.T) {
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	if got := applyBadgeCuration(hidden, "boomtime"); got != "boomtime" {
		t.Errorf("applyBadgeCuration(hidden, %q) = %q, want passthrough", "boomtime", got)
	}
}

// TestApplyBadgeCuration_NoRules: no hide rules is the common case; every
// project passes through untouched.
func TestApplyBadgeCuration_NoRules(t *testing.T) {
	hidden := model.HiddenSetsMap{}
	if got := applyBadgeCuration(hidden, "boomtime"); got != "boomtime" {
		t.Errorf("applyBadgeCuration(empty, %q) = %q, want passthrough", "boomtime", got)
	}
	// Nil-safe (defensive; the handler should never pass nil, but the helper
	// tolerates it so a future refactor can't crash the endpoint).
	if got := applyBadgeCuration(nil, "boomtime"); got != "boomtime" {
		t.Errorf("applyBadgeCuration(nil, %q) = %q, want passthrough", "boomtime", got)
	}
}

// TestApplyBadgeCuration_OtherAxesIgnored: only the project axis matters for
// badges (badges are project-scoped). Hiding a language must NOT hide a badge.
func TestApplyBadgeCuration_OtherAxesIgnored(t *testing.T) {
	hidden := model.HiddenSetsMap{"language": {"hakatime"}}
	if got := applyBadgeCuration(hidden, "hakatime"); got != "hakatime" {
		t.Errorf("applyBadgeCuration(language-only, %q) = %q, want passthrough (badges are project-scoped)", "hakatime", got)
	}
}
