// scanner_ginkgo_test.go — ginkgo mirror of scanner_test.go.
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//
//	TestScanner_YieldsAllCommitsWhenAllowlistEmpty → Scanner > yields all commits when allowlist empty
//	TestScanner_FiltersByAuthorEmail                → Scanner > filters by author email
//	TestScanner_RepoName_UsesBasename               → Scanner > RepoName uses basename
//	TestScanner_SinceUntil_ClampsWindow             → Scanner > Since/Until clamps window
//
// The stdlib mkTestRepo helper in scanner_test.go takes a concrete *testing.T
// (called t.TempDir / t.Fatalf). GinkgoT() returns an interface, so we can't
// pass it directly. Instead this file has its own mkTestRepoG builder that
// uses GinkgoT().TempDir() for scratch space and Expect for error checks so
// failures bubble as spec failures. Same three-commit fixture shape.
package git

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mkTestRepoG is the ginkgo-native twin of scanner_test.go's mkTestRepo. Uses
// GinkgoT().TempDir() for scratch space and Expect for error handling; the
// commit-shape fixture is identical (testCommit is defined in scanner_test.go
// and shared).
func mkTestRepoG(commits []testCommit) string {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	repo, err := git.PlainInit(dir, false)
	Expect(err).NotTo(HaveOccurred(), "PlainInit")
	wt, err := repo.Worktree()
	Expect(err).NotTo(HaveOccurred(), "Worktree")
	for _, c := range commits {
		fp := filepath.Join(dir, c.file)
		Expect(os.WriteFile(fp, []byte(c.body), 0o644)).To(Succeed(), "WriteFile")
		_, err := wt.Add(c.file)
		Expect(err).NotTo(HaveOccurred(), "Add")
		sig := &object.Signature{
			Name:  c.author,
			Email: c.email,
			When:  c.when,
		}
		_, err = wt.Commit(c.body, &git.CommitOptions{
			Author:    sig,
			Committer: sig,
		})
		Expect(err).NotTo(HaveOccurred(), "Commit")
	}
	return dir
}

var _ = Describe("Scanner", func() {
	It("yields all commits when allowlist is empty", func() {
		dir := mkTestRepoG([]testCommit{
			{"Me", "me@example.com", at(60), "a.go", "package a"},
			{"Other", "other@example.com", at(90), "b.py", "print(1)"},
		})
		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		var got []string
		for c, iterErr := range s.Iter(context.Background()) {
			Expect(iterErr).NotTo(HaveOccurred())
			got = append(got, c.AuthorEmail)
		}
		Expect(got).To(HaveLen(2), "got=%v", got)
	})

	It("filters by author email", func() {
		dir := mkTestRepoG([]testCommit{
			{"Me", "me@example.com", at(60), "a.go", "package a"},
			{"Other", "other@example.com", at(90), "b.py", "print(1)"},
		})
		s, err := NewScanner(dir, EstimatorConfig{
			AuthorEmails: []string{"me@example.com"},
		})
		Expect(err).NotTo(HaveOccurred())

		var got []string
		for c, iterErr := range s.Iter(context.Background()) {
			Expect(iterErr).NotTo(HaveOccurred())
			got = append(got, c.AuthorEmail)
		}
		Expect(got).To(Equal([]string{"me@example.com"}))
	})

	It("RepoName uses the directory basename", func() {
		dir := mkTestRepoG([]testCommit{
			{"Me", "me@example.com", at(60), "a.go", "package a"},
		})
		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.RepoName()).To(Equal(filepath.Base(dir)))
	})

	It("Since/Until clamps the emitted window", func() {
		dir := mkTestRepoG([]testCommit{
			{"Me", "me@example.com", at(10), "a.go", "package a"},
			{"Me", "me@example.com", at(60), "b.go", "package b"},
			{"Me", "me@example.com", at(120), "c.go", "package c"},
		})
		s, err := NewScanner(dir, EstimatorConfig{
			Since: at(30),
			Until: at(90),
		})
		Expect(err).NotTo(HaveOccurred())

		var count int
		for c, iterErr := range s.Iter(context.Background()) {
			Expect(iterErr).NotTo(HaveOccurred())
			Expect(c.Time.Before(at(30))).To(BeFalse(), "commit at %v before window", c.Time)
			Expect(c.Time.After(at(90))).To(BeFalse(), "commit at %v after window", c.Time)
			count++
		}
		Expect(count).To(Equal(1))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
type testCommit struct {
	author string
	email  string
	when   time.Time
	file   string
	body   string
}
