// widgets_test.go: unit coverage for the widget-endpoint public-safe policy
// (bd gaka-6jm.5). The full HTTP path requires a DB — that is exercised by
// integration tests. Here we pin the pure decision helper that mirrors
// applyBadgeCuration for the widget scope-project case.
package handler

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// TestIsWidgetScopeProjectHidden_HiddenProject: a scope-project widget whose
// pinned project is on the owner's hide list must be reported as hidden, so
// the handler can translate that into a 404 (a project-scoped widget's title,
// subtitle, and top-N rows would all leak the curated-away project — there
// is no partially-scrubbed representation, exactly like badges).
func TestIsWidgetScopeProjectHidden_HiddenProject(t *testing.T) {
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	if !isWidgetScopeProjectHidden(hidden, "hakatime") {
		t.Errorf("isWidgetScopeProjectHidden(hidden, %q) = false, want true (project is on hide list)", "hakatime")
	}
	// Case-insensitive: db.LoadHiddenSets lowercases match_value before storing,
	// and exclusionPredicate compares via `lower(col)` — the widget check must
	// match that contract too.
	if !isWidgetScopeProjectHidden(hidden, "HAKATIME") {
		t.Errorf("isWidgetScopeProjectHidden(hidden, %q) case-insensitive check failed", "HAKATIME")
	}
	if !isWidgetScopeProjectHidden(hidden, "Hakatime") {
		t.Errorf("isWidgetScopeProjectHidden(hidden, %q) case-insensitive check failed", "Hakatime")
	}
}

// TestIsWidgetScopeProjectHidden_VisibleProject: a visible project passes
// through — the handler proceeds to render.
func TestIsWidgetScopeProjectHidden_VisibleProject(t *testing.T) {
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	if isWidgetScopeProjectHidden(hidden, "boomtime") {
		t.Errorf("isWidgetScopeProjectHidden(hidden, %q) = true, want false (project not on hide list)", "boomtime")
	}
}

// TestIsWidgetScopeProjectHidden_NoRules: no hide rules is the common case —
// widgets always render.
func TestIsWidgetScopeProjectHidden_NoRules(t *testing.T) {
	hidden := model.HiddenSetsMap{}
	if isWidgetScopeProjectHidden(hidden, "boomtime") {
		t.Errorf("isWidgetScopeProjectHidden(empty, %q) = true, want false", "boomtime")
	}
	// Nil-safe (defensive; handler wires a real HiddenSets, but the helper
	// tolerates nil so a future refactor can't crash the endpoint).
	if isWidgetScopeProjectHidden(nil, "boomtime") {
		t.Errorf("isWidgetScopeProjectHidden(nil, %q) = true, want false", "boomtime")
	}
}

// TestIsWidgetScopeProjectHidden_OtherAxesIgnored: widget scope-project 404
// keys off the project axis only. Hiding a language MUST NOT 404 a widget
// pinned to a project that happens to share a string value with a hidden
// language (contrived, but the contract is axis-scoped by design).
func TestIsWidgetScopeProjectHidden_OtherAxesIgnored(t *testing.T) {
	hidden := model.HiddenSetsMap{"language": {"hakatime"}}
	if isWidgetScopeProjectHidden(hidden, "hakatime") {
		t.Errorf("isWidgetScopeProjectHidden(language-only, %q) = true, want false (widget project-scope is project-axis)", "hakatime")
	}
}
