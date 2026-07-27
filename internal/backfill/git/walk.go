// walk.go: `find`-like discovery of git repos rooted under a dir.
//
// A directory is a "repo" iff a .git entry sits at its top level (either
// a real .git dir, OR a .git file — for git worktrees / submodules).
// We do NOT open the repo here; NewScanner handles that. WalkRepos just
// yields candidate paths.
//
// Traversal is bounded by a small exclude list of directory names that
// commonly hold vendored git repos we don't want to attribute time to
// (node_modules, vendor, .cache, .venv). This list is deliberately
// static and small; if a specific repo needs to be excluded the CLI
// exposes --skip-repo <glob> at the command level.

package git

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// excludedDirs are directory names WalkRepos never descends into. Kept
// short and focused on "developer-machine noise" — anything more
// aggressive (like `.local`) risks skipping intentional roots on some
// setups.
var excludedDirs = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	".cache":       {},
	".venv":        {},
	"venv":         {},
	"__pycache__":  {},
	"target":       {}, // Rust build
	"dist":         {},
	"build":        {},
	".next":        {},
	".terraform":   {},
	".gradle":      {},
}

// dotGitStat is os.Stat by default; tests override it to a stub so a
// synthetic tree can drive WalkRepos without touching the real disk.
var dotGitStat = os.Stat

// WalkRepos returns every git repo path under root, deduped, in a
// deterministic (lexicographic) order so CLI runs against the same tree
// process repos in the same order twice in a row.
//
// A "repo" here is any dir containing a `.git` entry at its top level —
// this catches both regular clones (.git is a dir) AND worktrees /
// submodules (.git is a file with a gitdir pointer). We don't validate
// the pointer; PlainOpen will handle both cases and return a sensible
// error if the pointer is stale.
//
// Symlinks are NOT followed to avoid infinite loops on machines with
// aggressive symlink farms (dotfiles etc.). filepath.WalkDir skips
// symlinks by default; we don't override.
func WalkRepos(root string) ([]string, error) {
	var repos []string
	seen := map[string]struct{}{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A single unreadable dir shouldn't kill the whole scan;
			// skip and continue. Permission-denied under ~/Library on
			// macOS is the main real-world offender here.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()

		// Skip common noise directories. Do NOT skip the walk root
		// itself even if its own name matches (unlikely but possible
		// when a caller points at ~/code/node_modules directly).
		if path != root {
			if _, ex := excludedDirs[name]; ex {
				return filepath.SkipDir
			}
			// Hidden dirs other than .git: skip. Prevents descending
			// into .npm / .yarn / .config which have their own repos
			// as internal state we don't want to attribute to the
			// user.
			if strings.HasPrefix(name, ".") && name != ".git" && name != "." {
				return filepath.SkipDir
			}
		}

		// Is this dir itself a git repo? A .git entry (dir OR file) at
		// the top level marks the answer.
		if _, err := dotGitStat(filepath.Join(path, ".git")); err == nil {
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if _, dup := seen[abs]; !dup {
				seen[abs] = struct{}{}
				repos = append(repos, abs)
			}
			// Don't descend further: nested .git repos inside a repo's
			// worktree are almost always submodules or vendored, both
			// of which we want to skip.
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repos, nil
}
