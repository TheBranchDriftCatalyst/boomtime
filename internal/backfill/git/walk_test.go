package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkRepos_FindsGitDirs(t *testing.T) {
	root := t.TempDir()
	// Create:
	//   root/a/.git/    (real repo dir)
	//   root/b/         (not a repo)
	//   root/c/.git     (worktree pointer file)
	//   root/node_modules/x/.git/  (should be skipped)
	//   root/.hidden/x/.git/       (should be skipped — hidden top-level dir)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "a", ".git"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "b"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "c"), 0o755))
	must(os.WriteFile(filepath.Join(root, "c", ".git"), []byte("gitdir: /nowhere"), 0o644))
	must(os.MkdirAll(filepath.Join(root, "node_modules", "x", ".git"), 0o755))
	must(os.MkdirAll(filepath.Join(root, ".hidden", "x", ".git"), 0o755))

	repos, err := WalkRepos(root)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// Convert to a set of basenames for order-insensitive assertion.
	got := map[string]bool{}
	for _, r := range repos {
		got[filepath.Base(r)] = true
	}
	if !got["a"] || !got["c"] {
		t.Errorf("missing expected repos, got: %v", got)
	}
	if got["x"] {
		t.Errorf("did not skip excluded dirs, got: %v", got)
	}
}
