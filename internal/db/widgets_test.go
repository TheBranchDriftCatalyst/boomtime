package db

import (
	"sort"
	"testing"
)

// mkRenames builds a RenameSets with only exact-rename rules for the given
// axis (test helper mirroring the shape LoadRenameSets produces).
func mkRenames(axis string, exact map[string]string) RenameSets {
	rs := RenameSets{byAxis: map[string]axisRenames{}}
	a := rs.byAxis[axis]
	if a.exact == nil {
		a.exact = map[string]string{}
	}
	for src, tgt := range exact {
		a.exact[src] = tgt
	}
	rs.byAxis[axis] = a
	return rs
}

// TestExactSourcesFor: the reverse-lookup used by ProjectMemberSetWithRenames
// must return every raw name that renames to the given target, and nothing
// for a target that no exact rule points at.
func TestExactSourcesFor(t *testing.T) {
	rs := mkRenames("project", map[string]string{
		"hakatime":  "boomtime",
		"boomtime":  "boomtime", // idempotent (identity rename)
		"catalyst":  "boomtime", // merged into boomtime too
		"unrelated": "other",
	})

	got := rs.ExactSourcesFor("project", "boomtime")
	sort.Strings(got)
	want := []string{"boomtime", "catalyst", "hakatime"}
	if len(got) != len(want) {
		t.Fatalf("ExactSourcesFor len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("ExactSourcesFor[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Target that no rule maps to → nil (widget mint 404 stays 404).
	if got := rs.ExactSourcesFor("project", "no-such-target"); got != nil {
		t.Errorf("ExactSourcesFor(no-such-target) = %v, want nil", got)
	}

	// Wrong axis → nil (mint 404 stays 404).
	if got := rs.ExactSourcesFor("language", "boomtime"); got != nil {
		t.Errorf("ExactSourcesFor(wrong-axis) = %v, want nil", got)
	}
}

// TestProjectMemberSetWithRenamesExpands: gaka-xuc regression. A scope pinned
// to a renamed/merged project name must expand to include every raw source
// name so on-disk heartbeats (which carry the RAW value) match the filter.
// Without expansion the widget renders empty even though the user's data is
// there under the source name.
func TestProjectMemberSetWithRenamesExpands(t *testing.T) {
	rs := mkRenames("project", map[string]string{
		"hakatime": "boomtime",
		"catalyst": "boomtime",
	})

	ms := ProjectMemberSetWithRenames("boomtime", rs)
	got := ms.byAxis["project"].exact
	sort.Strings(got)
	want := []string{"boomtime", "catalyst", "hakatime"}
	if len(got) != len(want) {
		t.Fatalf("expanded exact = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("expanded exact[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// No renames touching this axis → member set is exactly the scope-ref.
	empty := ProjectMemberSetWithRenames("boomtime", RenameSets{})
	got = empty.byAxis["project"].exact
	if len(got) != 1 || got[0] != "boomtime" {
		t.Errorf("no-rename expansion = %v, want [boomtime]", got)
	}

	// The scope-ref itself doesn't appear as a rename source but IS the
	// target → the source list dedupes it so we don't send ["b","b"].
	self := mkRenames("project", map[string]string{
		"boomtime": "boomtime",
	})
	got = ProjectMemberSetWithRenames("boomtime", self).byAxis["project"].exact
	if len(got) != 1 || got[0] != "boomtime" {
		t.Errorf("identity-rename expansion = %v, want [boomtime]", got)
	}
}
