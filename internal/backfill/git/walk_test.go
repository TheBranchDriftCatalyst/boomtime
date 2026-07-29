// walk_ginkgo_test.go — ginkgo mirror of walk_test.go.
// 1:1 case map (1 stdlib TestXxx → 1 It):
//   TestWalkRepos_FindsGitDirs → WalkRepos > "finds real .git dirs and worktree pointer files, skips vendored/hidden"
package git

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WalkRepos", func() {
	It("finds real .git dirs and worktree pointer files, skips vendored/hidden top-level dirs", func() {
		root := GinkgoT().TempDir()
		// Create:
		//   root/a/.git/    (real repo dir)
		//   root/b/         (not a repo)
		//   root/c/.git     (worktree pointer file)
		//   root/node_modules/x/.git/  (should be skipped)
		//   root/.hidden/x/.git/       (should be skipped — hidden top-level dir)
		Expect(os.MkdirAll(filepath.Join(root, "a", ".git"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "b"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "c"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "c", ".git"), []byte("gitdir: /nowhere"), 0o644)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "node_modules", "x", ".git"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, ".hidden", "x", ".git"), 0o755)).To(Succeed())

		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())

		got := map[string]bool{}
		for _, r := range repos {
			got[filepath.Base(r)] = true
		}
		Expect(got["a"]).To(BeTrue(), "expected repo 'a' in %v", got)
		Expect(got["c"]).To(BeTrue(), "expected repo 'c' in %v", got)
		Expect(got["x"]).To(BeFalse(), "did not skip excluded dirs, got: %v", got)
	})
})
