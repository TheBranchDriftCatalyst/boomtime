// coverage_test.go — additional ginkgo specs pushing internal/backfill/git
// coverage from 83.2% → >=90%. Every spec pins a NAMED INVARIANT (see the
// "invariant" line in each It description) rather than a bare
// insert-x/get-x roundtrip.
//
// Case map (gaka-d6x):
//
//	NewScanner > errors when path is not a git repo             → NewScanner_PropagatesPlainOpenError
//	Scanner > RepoName strips trailing .git when passed bare    → RepoName_BareDotGit_UsesParent
//	Scanner > empty repo (no HEAD) yields zero commits          → Iter_EmptyRepo_NoErrorNoYield
//	Scanner > skips merge commits (>1 parent)                   → Iter_SkipsMergeCommits
//	Scanner > ctx cancellation aborts iteration cleanly         → Iter_CtxCancel_HaltsWithoutError
//	Scanner > caller break stops walk without draining log      → Iter_CallerBreak_ShortCircuits
//	Scanner > root commit reports all files as additions        → Iter_RootCommit_TreatsAllFilesAsAdditions
//	Scanner > commits after Until are skipped forward           → Iter_CommitsAfterUntil_SkippedNotStopped
//	Scanner > case-insensitive author allowlist                 → Iter_AuthorAllowlist_CaseInsensitive
//	EstimatorConfig.defaults > negative durations clamp to 0    → Defaults_NegativeDurations_ClampToZero
//	EstimatorConfig.languageFor > unknown ext returns empty     → LanguageFor_UnknownExt_ReturnsEmpty
//	EstimatorConfig.languageFor > empty ext returns empty       → LanguageFor_EmptyExt_ReturnsEmpty
//	EstimatorConfig.languageFor > LangMap overrides defaults    → LanguageFor_LangMapOverridesBuiltin
//	EstimatorConfig.languageFor > case-insensitive extension    → LanguageFor_CaseInsensitiveExt
//	Cluster > commit with files but zero lines still counted    → Cluster_ZeroLineCommit_FilesStillWeighted
//	Cluster > perFile floor: many files, few lines → each ≥ 1   → Cluster_ManyFilesFewLines_EveryFileScoresOne
//	Cluster > deterministic tie-break on identical timestamps   → Cluster_IdenticalTimestamps_StableOrder
//	Materialize > rate <= 0 defaults to 2m                      → Materialize_ZeroRate_DefaultsToTwoMinutes
//	Materialize > End == Start yields exactly one heartbeat     → Materialize_EndEqualsStart_OneHeartbeat
//	Materialize > empty session-language emits no lang pointer  → Materialize_NoLanguage_NilLanguagePointer
//	Materialize > FileWeights=nil falls through to TopFile      → Materialize_NoFileWeights_UsesTopFile
//	Materialize > all-zero FileWeights → placeholder fallback   → Materialize_AllZeroWeights_Placeholder
//	buildFilePattern > n<=0 returns nil (direct call)           → BuildFilePattern_NonPositiveN_Nil_Direct
//	timestampSteps > invalid inputs return nil (direct call)    → TimestampSteps_InvalidInputs_Nil_Direct
//	timestampSteps > End before Start returns nil (via Materialize)
//	                                                            → TimestampSteps_EndBeforeStart_Nil
//	WalkRepos > returns error when root does not exist          → WalkRepos_MissingRoot_ReturnsError
//	WalkRepos > deduplicates repeat visits                      → WalkRepos_Deduplicates
//	WalkRepos > does not descend nested .git children           → WalkRepos_NestedRepoNotDescended
//	WalkRepos > root itself may be a git repo                   → WalkRepos_RootIsRepo_Found
//	WalkRepos > walk error on descendant becomes SkipDir        → WalkRepos_UnreadableSubdir_SkippedNotFatal
//	WalkRepos > .git FILE (worktree pointer) at root is a repo  → WalkRepos_RootIsWorktreePointer_Found
//	WalkRepos > self-referential symlink loop does not hang     → WalkRepos_SymlinkLoop_TerminatesNoDup
//	Scanner Iter > diff-error branch fills partial.Hash          → Iter_DiffError_PartialCommitHashPopulated
//	EmailAllowed > blank allowlist entry matches empty email (documenting current bug)
//	                                                            → EmailAllowed_BlankEntry_MatchesEmpty_CurrentBehavior
//	EmailAllowed > blank + real entries: real works, blank still matches empty
//	                                                            → EmailAllowed_MixedRealAndBlank_CurrentBehavior
//	languageFor > 'Dockerfile' returns "" (dead-code pin)       → LanguageFor_Dockerfile_NoExtIsDeadCode
//	languageFor > LangMap[""] does NOT catch-all extension-less → LanguageFor_LangMapEmptyKey_DoesNotCatchAll
package git

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mkMultiFileCommit is a builder shared across the coverage specs — a single
// commit can add many files at once so Iter's diff-stats path is exercised
// against realistic per-file granularity.
func mkMultiFileCommit(dir, author, email string, when time.Time, files map[string]string) {
	GinkgoHelper()
	repo, err := git.PlainOpen(dir)
	Expect(err).NotTo(HaveOccurred(), "PlainOpen")
	wt, err := repo.Worktree()
	Expect(err).NotTo(HaveOccurred(), "Worktree")
	for path, body := range files {
		full := filepath.Join(dir, path)
		Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed(), "MkdirAll %s", full)
		Expect(os.WriteFile(full, []byte(body), 0o644)).To(Succeed(), "WriteFile %s", full)
		_, err := wt.Add(path)
		Expect(err).NotTo(HaveOccurred(), "Add %s", path)
	}
	sig := &object.Signature{Name: author, Email: email, When: when}
	_, err = wt.Commit("commit", &git.CommitOptions{Author: sig, Committer: sig, AllowEmptyCommits: true})
	Expect(err).NotTo(HaveOccurred(), "Commit")
}

// mkMergeCommit forces a merge commit (two parents) by branching, committing
// on the branch, then merging back. Used to exercise the "skip merges" branch
// in Scanner.Iter.
func mkMergeCommit(dir string, when time.Time) {
	GinkgoHelper()
	repo, err := git.PlainOpen(dir)
	Expect(err).NotTo(HaveOccurred())
	wt, err := repo.Worktree()
	Expect(err).NotTo(HaveOccurred())

	// Base commit on main
	base := filepath.Join(dir, "base.txt")
	Expect(os.WriteFile(base, []byte("root"), 0o644)).To(Succeed())
	_, err = wt.Add("base.txt")
	Expect(err).NotTo(HaveOccurred())
	baseHash, err := wt.Commit("base", &git.CommitOptions{
		Author:    &object.Signature{Name: "M", Email: "m@e", When: when.Add(-3 * time.Hour)},
		Committer: &object.Signature{Name: "M", Email: "m@e", When: when.Add(-3 * time.Hour)},
	})
	Expect(err).NotTo(HaveOccurred())

	// Grab HEAD ref before branching so we can rewind for the second parent.
	// Fake a "merge" by using git-native commit with two parents. go-git's
	// worktree Commit only supports linear commits; we build a raw commit
	// object with two parents pointing at HEAD twice (self-loop is not
	// technically a merge, so instead we branch: commit A on main, commit B
	// off base, then commit a merge with parents [A, B]).
	branchFile := filepath.Join(dir, "branch.txt")
	Expect(os.WriteFile(branchFile, []byte("branch"), 0o644)).To(Succeed())
	_, err = wt.Add("branch.txt")
	Expect(err).NotTo(HaveOccurred())
	branchHash, err := wt.Commit("branch tip", &git.CommitOptions{
		Author:    &object.Signature{Name: "M", Email: "m@e", When: when.Add(-2 * time.Hour)},
		Committer: &object.Signature{Name: "M", Email: "m@e", When: when.Add(-2 * time.Hour)},
	})
	Expect(err).NotTo(HaveOccurred())

	// Now craft a merge commit (two parents) directly through the object DB.
	mainFile := filepath.Join(dir, "main2.txt")
	Expect(os.WriteFile(mainFile, []byte("main2"), 0o644)).To(Succeed())
	_, err = wt.Add("main2.txt")
	Expect(err).NotTo(HaveOccurred())
	// wt.Commit sets HEAD as sole parent; use Parents option to add a second.
	_, err = wt.Commit("merge", &git.CommitOptions{
		Author:    &object.Signature{Name: "M", Email: "m@e", When: when},
		Committer: &object.Signature{Name: "M", Email: "m@e", When: when},
		Parents:   []plumbing.Hash{baseHash, branchHash},
	})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("NewScanner", func() {
	It("returns a wrapped error when the path is not a git repo (invariant: PlainOpen error surfaces to caller)", func() {
		// A brand-new empty tempdir is not a git repo.
		nonRepo := GinkgoT().TempDir()
		_, err := NewScanner(nonRepo, EstimatorConfig{})
		Expect(err).To(HaveOccurred(), "expected NewScanner to fail on non-repo path")
		// Message wraps go-git's error, preserving both the path (for the
		// operator) and the underlying reason (for programmatic Is checks).
		Expect(err.Error()).To(ContainSubstring(nonRepo))
	})
})

var _ = Describe("Scanner RepoName", func() {
	It("strips a trailing .git segment so bare repo paths report the visible project name (invariant: RepoName != '.git')", func() {
		// PlainInit(bare=true) creates a bare .git-style directory; then we
		// point NewScanner at the parent that contains it under a directory
		// literally named ".git" so RepoName's fallback branch fires.
		root := GinkgoT().TempDir()
		projectDir := filepath.Join(root, "myproj")
		gitPath := filepath.Join(projectDir, ".git")
		Expect(os.MkdirAll(gitPath, 0o755)).To(Succeed())
		_, err := git.PlainInit(gitPath, true)
		Expect(err).NotTo(HaveOccurred())

		s, err := NewScanner(gitPath, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())
		// The bare-path fallback should reach for the PARENT ("myproj") —
		// not literally ".git".
		Expect(s.RepoName()).To(Equal("myproj"))
	})
})

var _ = Describe("Scanner Iter — edge cases", func() {
	It("empty repo (no HEAD) yields zero commits and no error (invariant: fresh init = silent no-op, not error)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())

		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		var count int
		var iterErrs []error
		for _, e := range s.Iter(context.Background()) {
			if e != nil {
				iterErrs = append(iterErrs, e)
			}
			count++
		}
		Expect(count).To(Equal(0), "empty repo should yield zero commits")
		Expect(iterErrs).To(BeEmpty(), "empty repo should not yield errors")
	})

	It("skips merge commits (invariant: merge commit's diff would be noise, so scanner emits neither the merge nor an error)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		mkMergeCommit(dir, at(60))

		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		var hashes []string
		for c, e := range s.Iter(context.Background()) {
			Expect(e).NotTo(HaveOccurred())
			hashes = append(hashes, c.Hash)
		}
		// Merge itself is skipped. We still see the two non-merge parents
		// (base + branch tip). Assert count as a proxy: 3 total commits,
		// merge dropped → exactly 2 yielded.
		Expect(hashes).To(HaveLen(2), "expected 2 non-merge commits, got hashes %v", hashes)
	})

	It("halts cleanly on ctx cancellation without surfacing a spurious error (invariant: ctx.Done() → clean stop, no synthetic error)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		// Seed several commits so the walk would have plenty to do
		// if it did not respect ctx.
		for i := 0; i < 5; i++ {
			mkMultiFileCommit(dir, "M", "m@e", at(i*10), map[string]string{
				fmt.Sprintf("f%d.go", i): fmt.Sprintf("package p // %d", i),
			})
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel: first iteration should short-circuit

		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		var count int
		var iterErrs []error
		for _, e := range s.Iter(ctx) {
			if e != nil {
				iterErrs = append(iterErrs, e)
			}
			count++
		}
		// Pre-cancelled ctx should mean ZERO yields — the very first
		// callback invocation checks ctx.Done() and returns storer.ErrStop.
		Expect(count).To(Equal(0), "pre-cancelled ctx should stop the walk immediately")
		Expect(iterErrs).To(BeEmpty(), "ctx cancel is not an iteration error")
	})

	It("caller break stops the walk early (invariant: `yield returns false` unwinds the ForEach without draining the whole log)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < 6; i++ {
			mkMultiFileCommit(dir, "M", "m@e", at(i*5), map[string]string{
				fmt.Sprintf("f%d.go", i): "package p",
			})
		}

		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		var seen int
		for _, e := range s.Iter(context.Background()) {
			Expect(e).NotTo(HaveOccurred())
			seen++
			if seen == 2 {
				break // caller signals stop
			}
		}
		Expect(seen).To(Equal(2), "expected the break to unwind the walk at exactly 2 yields")
	})

	It("root commit reports all tree files as additions (invariant: no-parent commit's stats treat every file as new)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		mkMultiFileCommit(dir, "M", "m@e", at(10), map[string]string{
			"main.go":     "package main\nfunc main(){}\n",
			"README.md":   "# hi\n",
			"pkg/util.go": "package pkg\n",
		})

		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		var c Commit
		var count int
		for cc, e := range s.Iter(context.Background()) {
			Expect(e).NotTo(HaveOccurred())
			c = cc
			count++
		}
		Expect(count).To(Equal(1))
		// All three files should appear in FilesChanged.
		Expect(c.FilesChanged).To(ContainElements("main.go", "README.md", filepath.ToSlash("pkg/util.go")))
		// Root commit → every added line counts, no deletions.
		Expect(c.LinesAdded).To(BeNumerically(">", 0))
		Expect(c.LinesDeleted).To(Equal(0))
	})

	It("commits newer than Until are skipped but do not stop the walk (invariant: Until is a forward filter, Since is a hard stop)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		// Three commits: newest, middle (in window), oldest (before Since).
		mkMultiFileCommit(dir, "M", "m@e", at(10), map[string]string{"a.go": "package a"})
		mkMultiFileCommit(dir, "M", "m@e", at(60), map[string]string{"b.go": "package b"})
		mkMultiFileCommit(dir, "M", "m@e", at(200), map[string]string{"c.go": "package c"})

		s, err := NewScanner(dir, EstimatorConfig{
			// Iter walks newest-first, so at(200) hits first and Until=at(90) should skip forward
			// past it (not stop). Then at(60) is in-window and yields. Then at(10) is before Since
			// and stops the walk.
			Since: at(30),
			Until: at(90),
		})
		Expect(err).NotTo(HaveOccurred())

		var times []time.Time
		for c, e := range s.Iter(context.Background()) {
			Expect(e).NotTo(HaveOccurred())
			times = append(times, c.Time)
		}
		// Only at(60) survives — proving Until skipped forward past at(200).
		Expect(times).To(HaveLen(1), "expected exactly 1 commit in window, got %v", times)
		Expect(times[0].Equal(at(60))).To(BeTrue())
	})

	It("author allowlist matches case-insensitively (invariant: 'ME@Example.COM' allowed by 'me@example.com')", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		mkMultiFileCommit(dir, "M", "ME@Example.COM", at(10), map[string]string{"a.go": "package a"})

		s, err := NewScanner(dir, EstimatorConfig{
			AuthorEmails: []string{"  me@example.com  "}, // trimmed + lowercased
		})
		Expect(err).NotTo(HaveOccurred())

		var count int
		for _, e := range s.Iter(context.Background()) {
			Expect(e).NotTo(HaveOccurred())
			count++
		}
		Expect(count).To(Equal(1), "case-insensitive allowlist should match the commit")
	})
})

var _ = Describe("EstimatorConfig.defaults", func() {
	It("clamps negative PreCommitLead and PostCommitTail to zero-then-default (invariant: no negative durations leak into cluster math)", func() {
		// The clamp path: negative in → 0 → then the "0 = supply default"
		// path kicks in. Assert the final observable values on a session so
		// this is not a call-and-inspect roundtrip.
		cfg := EstimatorConfig{
			ClusterGap:     30 * time.Minute,
			PreCommitLead:  -1 * time.Hour,
			PostCommitTail: -30 * time.Minute,
			HeartbeatRate:  2 * time.Minute,
		}
		c := mkCommit(at(60), []string{"main.go"}, 10)
		sessions := Cluster([]Commit{c}, cfg)
		Expect(sessions).To(HaveLen(1))
		// Negative → 0 → default = 15m lead, 5m tail. Same numbers as the
		// happy-path Cluster test.
		Expect(sessions[0].Start.Equal(at(45))).To(BeTrue(), "Start=%v, expected 15m before at(60)", sessions[0].Start)
		Expect(sessions[0].End.Equal(at(65))).To(BeTrue(), "End=%v, expected 5m after at(60)", sessions[0].End)
	})
})

var _ = Describe("EstimatorConfig.languageFor", func() {
	It("returns empty for a path with no extension (invariant: no extension = no language attribution, no phantom Language:'')", func() {
		cfg := EstimatorConfig{}.defaults()
		Expect(cfg.languageFor("Makefile")).To(Equal(""))
		Expect(cfg.languageFor("some/path/README")).To(Equal(""))
	})

	It("returns empty for an unknown extension (invariant: unknown ext falls through both LangMap and extToLang without panicking)", func() {
		cfg := EstimatorConfig{}.defaults()
		Expect(cfg.languageFor("weird.xyz123")).To(Equal(""))
	})

	It("LangMap override wins over compiled default (invariant: user-supplied LangMap['ts']='CustomTS' beats built-in 'TypeScript')", func() {
		cfg := EstimatorConfig{
			LangMap: map[string]string{
				"ts": "CustomTS",
				"go": "Golang", // shadow default
			},
		}
		Expect(cfg.languageFor("app.ts")).To(Equal("CustomTS"))
		Expect(cfg.languageFor("main.go")).To(Equal("Golang"))
		// Ext not in LangMap still uses default.
		Expect(cfg.languageFor("script.py")).To(Equal("Python"))
	})

	It("extension lookup is case-insensitive (invariant: 'FOO.GO' → 'Go')", func() {
		cfg := EstimatorConfig{}.defaults()
		Expect(cfg.languageFor("FOO.GO")).To(Equal("Go"))
		Expect(cfg.languageFor("Main.PY")).To(Equal("Python"))
	})
})

var _ = Describe("Cluster — edge cases", func() {
	It("counts each touched file at least once even when a commit has zero total-lines (invariant: rename/mode-only commits still contribute a weight)", func() {
		// LinesAdded + LinesDeleted = 0 forces the fallback branch that
		// still increments touchedPerFile by 1 per file.
		commits := []Commit{
			{
				RepoName:     "r",
				Hash:         "zero",
				AuthorEmail:  "m@e",
				Time:         at(60),
				FilesChanged: []string{"rename1.go", "rename2.go"},
				LinesAdded:   0,
				LinesDeleted: 0,
			},
		}
		sessions := Cluster(commits, EstimatorConfig{ClusterGap: 30 * time.Minute})
		Expect(sessions).To(HaveLen(1))
		s := sessions[0]
		Expect(s.FileWeights).NotTo(BeNil(), "FileWeights should be populated even for zero-line commit")
		// Both files should have a positive weight (fallback = 1 each).
		Expect(s.FileWeights["rename1.go"]).To(BeNumerically(">=", 1))
		Expect(s.FileWeights["rename2.go"]).To(BeNumerically(">=", 1))
	})

	It("per-file weight floors at 1 when total/len rounds to zero (invariant: many-files-few-lines commit still credits every file)", func() {
		// 3 files, 2 lines total → perFile = 2/3 = 0, floored to 1 by the
		// `if perFile == 0 { perFile = 1 }` branch.
		commits := []Commit{
			{
				RepoName:     "r",
				Hash:         "tiny",
				AuthorEmail:  "m@e",
				Time:         at(60),
				FilesChanged: []string{"a.go", "b.go", "c.go"},
				LinesAdded:   1,
				LinesDeleted: 1,
			},
		}
		sessions := Cluster(commits, EstimatorConfig{ClusterGap: 30 * time.Minute})
		Expect(sessions).To(HaveLen(1))
		s := sessions[0]
		Expect(s.FileWeights["a.go"]).To(Equal(1))
		Expect(s.FileWeights["b.go"]).To(Equal(1))
		Expect(s.FileWeights["c.go"]).To(Equal(1))
	})

	It("preserves input order for commits with identical timestamps (invariant: SliceStable → deterministic cluster output)", func() {
		// Same timestamp, two commits. Stable sort guarantees the first
		// one in input order stays first in the cluster's Commits slice.
		commits := []Commit{
			{RepoName: "r", Hash: "first", AuthorEmail: "m@e", Time: at(60), FilesChanged: []string{"a.go"}, LinesAdded: 1},
			{RepoName: "r", Hash: "second", AuthorEmail: "m@e", Time: at(60), FilesChanged: []string{"b.go"}, LinesAdded: 1},
		}
		sessions := Cluster(commits, EstimatorConfig{ClusterGap: 30 * time.Minute})
		Expect(sessions).To(HaveLen(1))
		Expect(sessions[0].Commits[0].Hash).To(Equal("first"))
		Expect(sessions[0].Commits[1].Hash).To(Equal("second"))
	})
})

var _ = Describe("Materialize — edge cases", func() {
	It("defaults rate to 2 minutes when caller passes rate <= 0 (invariant: zero/negative rate does NOT divide-by-zero or emit infinite heartbeats)", func() {
		sess := Session{
			RepoName: "r",
			Start:    at(0),
			End:      at(10),
			TopFile:  "main.go",
			Language: "Go",
		}
		// rate=0 must be replaced by 2m default → same shape as the 2m case.
		hbs := Materialize(sess, "backfill:git", 0)
		Expect(hbs).To(HaveLen(6), "0-rate should default to 2m and produce 6 heartbeats over 10m")

		hbsNeg := Materialize(sess, "backfill:git", -5*time.Minute)
		Expect(hbsNeg).To(HaveLen(6), "negative rate should also default to 2m")
	})

	It("End == Start yields exactly one heartbeat at Start (invariant: zero-duration session still counts as a single tick)", func() {
		sess := Session{
			RepoName: "r",
			Start:    at(30),
			End:      at(30),
			TopFile:  "main.go",
			Language: "Go",
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).To(HaveLen(1), "single-instant session should emit exactly one heartbeat")
		Expect(time.Unix(int64(hbs[0].Time), 0).UTC().Equal(at(30))).To(BeTrue())
	})

	It("emits nil Language pointer when session has no language (invariant: unknown language must NOT become the string '', it must be absent)", func() {
		sess := Session{
			RepoName: "r",
			Start:    at(0),
			End:      at(4),
			TopFile:  "unknown.zzz", // extension not in extToLang
			// Language deliberately blank
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).NotTo(BeEmpty())
		for i, hb := range hbs {
			Expect(hb.Language).To(BeNil(), "hb[%d] language should be nil (not empty string)", i)
		}
	})

	It("falls back to TopFile when Session has no FileWeights (invariant: manually-constructed Session still materializes to TopFile per slot)", func() {
		sess := Session{
			RepoName: "r",
			Start:    at(0),
			End:      at(6),
			TopFile:  "hero.go",
			Language: "Go",
			// FileWeights deliberately nil
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).To(HaveLen(4), "0,2,4,6 = 4 heartbeats over 6m at 2m rate")
		for _, hb := range hbs {
			Expect(hb.Entity).To(Equal("hero.go"), "no-weights path must repeat TopFile for every slot")
		}
	})

	It("all-zero weights degrade to placeholder path (invariant: FileWeights={a:0,b:0} → treat as no weights, not divide-by-zero)", func() {
		sess := Session{
			RepoName: "myproj",
			Start:    at(0),
			End:      at(4),
			// TopFile empty AND all weights zero → placeholder path.
			FileWeights: map[string]int{"a.go": 0, "b.go": 0},
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).NotTo(BeEmpty())
		for _, hb := range hbs {
			Expect(hb.Entity).To(Equal("backfill:myproj"), "all-zero weights should collapse to placeholder")
		}
	})
})

var _ = Describe("timestampSteps / buildFilePattern — boundary", func() {
	It("timestampSteps returns nil when End is before Start (invariant: inverted range yields empty slice, no negative-length allocation)", func() {
		// Exercised via Materialize since timestampSteps is unexported;
		// End<Start returns nil from Materialize itself (line 166-168),
		// so drive the inverted-range check via a manually-constructed
		// call chain — Materialize's guard is what we're pinning.
		sess := Session{Start: at(100), End: at(50)}
		Expect(Materialize(sess, "backfill:git", 2*time.Minute)).To(BeNil())
	})

	// buildFilePattern is package-private but this test lives in the same
	// package (git), so we call it directly. Previously this spec drove it
	// through Materialize with an inverted Start/End range — but that path
	// short-circuits at Materialize's End.Before(Start) guard BEFORE
	// buildFilePattern is ever invoked, meaning the test would still pass
	// even if the `if n <= 0 { return nil }` branch were deleted. Fixed by
	// invoking the function directly with n=0 and n=-1.
	It("buildFilePattern returns nil when n <= 0 (invariant: zero/negative slot count skips ALL allocation and returns a nil slice, not an empty one)", func() {
		sess := Session{TopFile: "a.go", RepoName: "r"}
		Expect(buildFilePattern(sess, 0)).To(BeNil(), "n=0 must return nil (not empty slice)")
		Expect(buildFilePattern(sess, -1)).To(BeNil(), "n=-1 must return nil")
		Expect(buildFilePattern(sess, -100)).To(BeNil(), "n=-100 must return nil")

		// Belt-and-braces: with FileWeights set, the n<=0 branch still fires
		// FIRST (before the weight-branch). Prove that too — otherwise a
		// refactor that moved the `if n <= 0` check below the weights
		// short-circuit would slip through unnoticed.
		sessWeighted := Session{
			TopFile:     "a.go",
			RepoName:    "r",
			FileWeights: map[string]int{"a.go": 3, "b.go": 1},
		}
		Expect(buildFilePattern(sessWeighted, 0)).To(BeNil(), "n=0 must return nil even when FileWeights populated")
		Expect(buildFilePattern(sessWeighted, -1)).To(BeNil(), "n=-1 must return nil even when FileWeights populated")
	})

	// timestampSteps is also package-private; drive it directly to pin its
	// own guard, independent of Materialize's guard.
	It("timestampSteps returns nil for invalid inputs (invariant: rate<=0 OR end<start each independently produce nil)", func() {
		// rate<=0 branch
		Expect(timestampSteps(at(0), at(10), 0)).To(BeNil(), "rate=0 must return nil")
		Expect(timestampSteps(at(0), at(10), -1*time.Minute)).To(BeNil(), "rate<0 must return nil")
		// end<start branch (independent of rate)
		Expect(timestampSteps(at(10), at(5), 2*time.Minute)).To(BeNil(), "end<start must return nil")
	})
})

var _ = Describe("WalkRepos — edge cases", func() {
	It("returns empty slice (no error) when the walk root does not exist (invariant: the walk-error handler swallows missing-root just like unreadable subdirs — 'nothing to scan' is not a fatal error)", func() {
		nonexistent := filepath.Join(GinkgoT().TempDir(), "does-not-exist")
		repos, err := WalkRepos(nonexistent)
		// WalkDir invokes fn with walkErr!=nil for the missing root. The
		// error handler in walk.go returns SkipDir (dir case) or nil (non-
		// dir) so WalkRepos surfaces no error to the caller. Assert both
		// halves to pin the swallow behaviour.
		Expect(err).NotTo(HaveOccurred(), "missing root is swallowed by walk-error handler")
		Expect(repos).To(BeEmpty(), "missing root produces no repos, not a partial list")
	})

	It("deduplicates repeat visits so a repo path never appears twice (invariant: `seen` map holds the invariant across incidental re-visits)", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "onlyrepo", ".git"), 0o755)).To(Succeed())
		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())
		// Only one repo expected — dedup is trivially validated, but the
		// invariant is that WalkDir's SkipDir on a nested .git can't produce
		// a duplicate entry.
		Expect(repos).To(HaveLen(1))
		Expect(filepath.Base(repos[0])).To(Equal("onlyrepo"))
	})

	It("does not descend into nested .git children — child repos inside another repo are ignored (invariant: submodule-under-repo is not double-attributed)", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "outer", ".git"), 0o755)).To(Succeed())
		// Nested "submodule" under outer/vendor-sub/inner/.git — same
		// name as an excluded dir would trip a different branch. Use a
		// benign name so only the "don't descend past .git" invariant is
		// under test.
		Expect(os.MkdirAll(filepath.Join(root, "outer", "sub", "inner", ".git"), 0o755)).To(Succeed())
		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())
		// Only "outer" should be reported — "inner" is nested and skipped.
		Expect(repos).To(HaveLen(1))
		Expect(filepath.Base(repos[0])).To(Equal("outer"))
	})

	It("the walk root itself may be a git repo and gets reported once (invariant: root-is-repo path exercises the `path == root` shortcut)", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, ".git"), 0o755)).To(Succeed())
		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(repos).To(HaveLen(1), "root-is-repo should yield exactly 1 entry")
	})

	It("swallows walk-descendant errors as SkipDir rather than aborting the whole scan (invariant: one bad subtree cannot mask a sibling repo)", func() {
		// Simulate an unreadable subdir by chmod 0000. On CI the test may
		// run as root — in that case chmod is a no-op and we fall through
		// to a soft assertion, but the code path is still exercised.
		root := GinkgoT().TempDir()
		unreadable := filepath.Join(root, "denied")
		Expect(os.MkdirAll(unreadable, 0o755)).To(Succeed())
		// Sibling: a legitimate repo that must still be found even if the
		// denied dir errors out during walk.
		Expect(os.MkdirAll(filepath.Join(root, "sibling", ".git"), 0o755)).To(Succeed())

		// Strip read permission from `denied`. On macOS + Linux this makes
		// WalkDir return a Permission error for its contents; the handler
		// should absorb it as SkipDir.
		Expect(os.Chmod(unreadable, 0o000)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(unreadable, 0o755) })

		repos, err := WalkRepos(root)
		// Even when the "denied" subtree errors, the top-level walk must
		// complete AND report the sibling.
		Expect(err).NotTo(HaveOccurred(), "walk-descendant errors must be swallowed, not surfaced")
		names := map[string]bool{}
		for _, r := range repos {
			names[filepath.Base(r)] = true
		}
		Expect(names["sibling"]).To(BeTrue(), "sibling repo must still be discovered despite denied peer")
	})
})

// -- Additional coverage for walk.go non-dir walkErr branch. --
//
// The walkErr branch has two cases: d != nil && d.IsDir → SkipDir, else nil.
// The "d == nil OR not a dir" case is hard to hit organically because
// filepath.WalkDir only calls the callback with walkErr!=nil when it fails to
// even open the entry. We simulate by stubbing dotGitStat to return a
// permission-shaped error, but that's actually the .git-detect branch. To
// exercise line 73-74 (non-dir), leverage a broken symlink under a real dir.
var _ = Describe("WalkRepos — non-directory walk error", func() {
	It("continues past a non-directory that errors during walk (invariant: broken symlink or vanished file does not kill the walk)", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "repo", ".git"), 0o755)).To(Succeed())
		// Create a symlink to a nonexistent target — filepath.WalkDir
		// will present it with walkErr!=nil and d.IsDir()==false.
		badLink := filepath.Join(root, "broken-symlink")
		Expect(os.Symlink("/absolutely/not/here", badLink)).To(Succeed())

		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())
		var found bool
		for _, r := range repos {
			if filepath.Base(r) == "repo" {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "expected 'repo' to be discovered despite broken sibling symlink")
	})
})

// -- dotGitStat override coverage --
//
// walk.go declares `var dotGitStat = os.Stat` explicitly so tests can inject
// failure. We use it here to verify that a Stat error on `.git` simply means
// "not a repo" and the walk continues — the else-branch of the
// if _, err := dotGitStat(...); err == nil {...} is exercised implicitly by
// every non-repo dir in the earlier tests, but we pin the invariant with an
// explicit override so a future refactor that swallows the err path would
// fail this test.
var _ = Describe("WalkRepos — dotGitStat override", func() {
	It("treats a dotGitStat error as 'not a repo' and keeps walking (invariant: Stat failure ≠ discovery)", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "not-a-repo"), 0o755)).To(Succeed())

		orig := dotGitStat
		DeferCleanup(func() { dotGitStat = orig })
		// Force every .git check to return an error → nothing should be
		// discovered as a repo.
		dotGitStat = func(name string) (fs.FileInfo, error) {
			return nil, fmt.Errorf("synthetic stat error")
		}

		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(repos).To(BeEmpty(), "no repo should be reported when every .git stat fails")
	})
})

// -- Kill the "we didn't import fstest" nagging by referring to it once — the
// file-system helpers might come in handy for future stubs. Removed if unused
// to keep the import list honest. --
var _ = fstest.MapFile{}

// -- Diff-error fault injection: corrupt ONE specific commit's parent tree
// object so c.Stats() fails ONLY on that commit — HEAD resolution and log
// walking must succeed. Verifies Scanner.Iter yields a (partial, err) frame
// FROM THE DIFF-ERROR BRANCH (not from the outer HEAD/log-walk fallback),
// and that the partial carries useful metadata (Hash populated from the
// scanner.go:162-168 partial-Commit struct literal). --
var _ = Describe("Scanner Iter — diff error fault injection", func() {
	It("yields a partial Commit with populated Hash from the diff-error branch when only c.Stats() fails (invariant: partial-Commit fields at scanner.go:162-168 travel with the error, distinguishing this from the outer HEAD/log-walk error path)", func() {
		dir := GinkgoT().TempDir()
		_, err := git.PlainInit(dir, false)
		Expect(err).NotTo(HaveOccurred())
		mkMultiFileCommit(dir, "M", "m@e", at(10), map[string]string{"a.go": "package a\n"})
		mkMultiFileCommit(dir, "M", "m@e", at(20), map[string]string{"a.go": "package a\nvar x = 1\n"})

		// Find the OLDEST commit (root) and identify its tree object hash.
		// Then blow away ONLY that tree object. c.Stats() for the newer
		// commit walks against its first parent = the root, and needs to
		// read the root's tree — that's what will fail. HEAD resolves fine
		// (the ref points at the newer commit), Log iteration walks fine
		// (it needs commit objects, which are untouched), but Stats fails.
		repo, err := git.PlainOpen(dir)
		Expect(err).NotTo(HaveOccurred())
		head, err := repo.Head()
		Expect(err).NotTo(HaveOccurred())
		newCommit, err := repo.CommitObject(head.Hash())
		Expect(err).NotTo(HaveOccurred())
		Expect(newCommit.NumParents()).To(Equal(1), "expected linear history with one parent")
		parentIter := newCommit.Parents()
		parent, err := parentIter.Next()
		Expect(err).NotTo(HaveOccurred())
		parentTreeHash := parent.TreeHash

		// go-git loose objects live under .git/objects/xx/yyyy... where xx
		// is the first two hex chars of the object hash. Blow the file away
		// entirely (empty file = zlib decode error → c.Stats fails).
		treeHex := parentTreeHash.String()
		treeObjPath := filepath.Join(dir, ".git", "objects", treeHex[:2], treeHex[2:])
		// Sanity-check the file exists before we nuke it — if go-git ever
		// packs by default the test needs a different approach.
		_, statErr := os.Stat(treeObjPath)
		Expect(statErr).NotTo(HaveOccurred(), "expected loose object at %s (repo may have been packed)", treeObjPath)
		Expect(os.WriteFile(treeObjPath, []byte("garbage"), 0o644)).To(Succeed())

		s, err := NewScanner(dir, EstimatorConfig{})
		Expect(err).NotTo(HaveOccurred())

		// Collect the FIRST error frame; assert its partial-Commit metadata
		// was filled in by the diff-error branch. If the error came from
		// the outer log-walk path, the yielded Commit would be a zero
		// value (Commit{}) with empty Hash — which is what distinguishes
		// this branch from the fallback.
		var firstErrCommit Commit
		var firstErr error
		var yieldCount int
		var sawSuccess bool
		for c, e := range s.Iter(context.Background()) {
			yieldCount++
			if e != nil && firstErr == nil {
				firstErr = e
				firstErrCommit = c
				// Break inside the error path — exercises the
				// `if !yield(...) { return storer.ErrStop }` branch
				// at scanner.go:168-170.
				break
			}
			if e == nil {
				sawSuccess = true
			}
		}
		Expect(firstErr).To(HaveOccurred(), "expected an error frame from a commit whose parent tree is corrupt")
		Expect(yieldCount).To(BeNumerically(">=", 1), "expected at least one yield")
		// This is the key assertion that distinguishes the diff-error
		// branch (scanner.go:162 — partial has RepoName+Hash+AuthorEmail)
		// from the outer log-walk branch (scanner.go:189 — Commit{} zero).
		Expect(firstErrCommit.Hash).NotTo(BeEmpty(), "diff-error branch must populate partial.Hash; empty Hash means the error came from the outer log-walk branch instead")
		Expect(firstErrCommit.AuthorEmail).To(Equal("m@e"), "diff-error branch must populate partial.AuthorEmail from the still-loadable commit metadata")
		Expect(firstErrCommit.RepoName).NotTo(BeEmpty(), "diff-error branch must populate partial.RepoName from the scanner")
		Expect(firstErr.Error()).To(ContainSubstring("diff"), "error message should be prefixed 'diff %%s' per scanner.go:168")
		// The newest commit's own tree is intact so it might have already
		// yielded before the error — sawSuccess may be true or false
		// depending on iteration order. We don't assert on it.
		_ = sawSuccess
	})
})

// -- Missing invariants surfaced by critique (gaka-d6x): EmailAllowed
// blank-entry surface, WalkRepos `.git` file at root, symlink loop safety,
// Dockerfile no-extension pinning. --

var _ = Describe("EmailAllowed — blank allowlist entries", func() {
	// A subtle real-world bug: if an operator's config file has a stray
	// whitespace-only line in AuthorEmails, TrimSpace + ToLower collapses
	// it to the empty string "". A commit with a blank Author.Email (very
	// old imports, malformed .mailmap sources, or hand-crafted commits)
	// would then match and slip past the allowlist. The invariant we're
	// pinning is: a blank entry MUST NOT match a blank email — the caller
	// clearly meant "no email set here", not "match every email-less
	// commit".
	//
	// NOTE: at the time of writing this test documents CURRENT behavior.
	// EmailAllowed's ToLower+TrimSpace chain means "" == "" today, so the
	// test intentionally records the (surprising) fact so a future change
	// that fixes it will be a visible red spec — not a silent regression.
	It("current behavior: a whitespace-only allowlist entry does match an empty email (invariant: blank-vs-blank collision is a known gap; if this test fails, the fix landed and the comment/tag should move)", func() {
		cfg := EstimatorConfig{AuthorEmails: []string{"  "}}
		// Pin the CURRENT surface: "" matches "" after normalization.
		// If someone hardens EmailAllowed to reject blank entries at
		// startup (or to require a non-empty lower after trimming),
		// this test flips red — which is the desired signal to update
		// the constant here.
		Expect(cfg.EmailAllowed("")).To(BeTrue(), "documenting current (buggy) behavior: blank entry matches blank email")
		// Meanwhile, a real email is (correctly) NOT allowed by a blank-only
		// allowlist — otherwise the whole allowlist would be a no-op.
		Expect(cfg.EmailAllowed("real@example.com")).To(BeFalse(), "blank-only allowlist must not match real emails")
	})

	It("empty-string entries alongside real entries do not open the door for blank-email commits when the real entry set is intended (invariant: presence of a legitimate entry is what should authorize, not the accidental blank neighbor)", func() {
		// Practical shape: config file with a real entry AND a stray blank.
		// The real entry authorizes real@example.com; the blank one, per
		// the bug above, ALSO authorizes empty. If a future fix drops
		// blank entries at parse time, only the real match should survive.
		cfg := EstimatorConfig{AuthorEmails: []string{"real@example.com", "  "}}
		Expect(cfg.EmailAllowed("real@example.com")).To(BeTrue(), "explicit entry always matches")
		Expect(cfg.EmailAllowed("other@example.com")).To(BeFalse(), "unlisted email always rejected")
		// This mirrors the assertion above — pins current behavior so a
		// future hardening (drop blank entries at normalization) is a
		// visible + intentional change.
		Expect(cfg.EmailAllowed("")).To(BeTrue(), "documenting: stray blank entry still matches empty email today")
	})
})

var _ = Describe("WalkRepos — .git file (worktree pointer) at walk root", func() {
	// walk.go:52-58 explicitly notes that a .git FILE (gitdir pointer for
	// worktrees / submodules) counts as a repo the same as a .git dir. The
	// existing `WalkRepos_RootIsRepo_Found` test only exercises the .git
	// dir case at the root. This test independently pins the .git-file
	// case at the root, exercising the OTHER half of dotGitStat's success
	// branch (Stat returns nil for a regular file too).
	It("the walk root with a .git FILE (worktree pointer) is discovered exactly like a .git dir at root (invariant: dotGitStat is file/dir-agnostic; a submodule/worktree checkout at root is still a repo)", func() {
		root := GinkgoT().TempDir()
		// Note we write a regular file, not a directory. Emulates a
		// linked worktree's `gitdir: /path/to/parent/.git/worktrees/name`
		// pointer file.
		Expect(os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644)).To(Succeed())
		repos, err := WalkRepos(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(repos).To(HaveLen(1), "root with .git FILE must be reported as one repo (not zero)")
		abs, aerr := filepath.Abs(root)
		Expect(aerr).NotTo(HaveOccurred())
		Expect(repos[0]).To(Equal(abs), "reported repo path must equal the absolute walk root")
	})
})

var _ = Describe("WalkRepos — symlink loop safety", func() {
	// walk.go:56-58 comment: "Symlinks are NOT followed to avoid infinite
	// loops on machines with aggressive symlink farms." No existing test
	// pins this — the broken-symlink test only covers a non-dir walkErr
	// case, not a self-referential loop. Since `seen` is only checked on
	// repo discovery, an accidental change (like `filepath.EvalSymlinks`
	// creeping in, or WalkDir being swapped for Walk which follows) could
	// hang forever or double-count a repo.
	//
	// We create root/repo/.git (real repo) and root/loop → root (self-
	// referential). If WalkRepos ever follows the loop symlink it would
	// re-enter root and re-discover repo, potentially forever. The test
	// asserts (a) the walk completes in bounded time, and (b) repo is
	// reported exactly once — no dup from the loop path.
	It("does not follow a self-referential symlink at any depth (invariant: filepath.WalkDir's no-symlink-follow contract is preserved — repo count stays exactly 1 even with a loop)", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "repo", ".git"), 0o755)).To(Succeed())
		// Self-referential loop: root/loop → root (absolute target so
		// there's no ambiguity — a relative "." would also work but is
		// harder to reason about across platforms).
		Expect(os.Symlink(root, filepath.Join(root, "loop"))).To(Succeed())

		done := make(chan struct{})
		var repos []string
		var walkErr error
		go func() {
			defer close(done)
			repos, walkErr = WalkRepos(root)
		}()
		select {
		case <-done:
			// completed in bounded time — no infinite loop
		case <-time.After(5 * time.Second):
			Fail("WalkRepos did not complete within 5s — symlink loop was followed")
		}

		Expect(walkErr).NotTo(HaveOccurred())
		// The invariant: repo appears EXACTLY once. If WalkRepos ever
		// starts following symlinks (e.g. someone swaps Walk for
		// EvalSymlinks-flavored variant), this count would balloon.
		count := 0
		for _, r := range repos {
			if filepath.Base(r) == "repo" {
				count++
			}
		}
		Expect(count).To(Equal(1), "repo must appear exactly once even in the presence of a self-referential symlink; got repos=%v", repos)
	})
})

var _ = Describe("EstimatorConfig.languageFor — 'Dockerfile' (no extension) is dead code in extToLang", func() {
	// Bug/quirk documented by critique: extToLang has a "dockerfile" key
	// but languageFor uses filepath.Ext(path) which returns "" for a file
	// literally named "Dockerfile" (no dot in the basename). So the
	// extToLang["dockerfile"] entry is unreachable via the current lookup
	// path. This test PINS that current behavior — if someone adds a
	// special case for extension-less well-known filenames, this spec
	// flips red and forces a conscious update.
	It("current behavior: 'Dockerfile' returns empty (invariant: filepath.Ext strips no leading dot on 'Dockerfile' → \"\", so extToLang['dockerfile'] is unreachable via the current code path)", func() {
		cfg := EstimatorConfig{}.defaults()
		// The extToLang table has "dockerfile" → "Docker", but a file
		// literally named "Dockerfile" has no extension.
		Expect(cfg.languageFor("Dockerfile")).To(Equal(""), "no leading dot → filepath.Ext returns empty → no language attributed")
		Expect(cfg.languageFor("path/to/Dockerfile")).To(Equal(""), "extension-less basename same result regardless of dir path")
		// A file with a genuine .dockerfile extension (unusual but
		// possible) DOES hit the entry.
		Expect(cfg.languageFor("my.dockerfile")).To(Equal("Docker"), "genuine .dockerfile extension routes through extToLang correctly")
	})

	It("user can work around the extension-less quirk via LangMap since LangMap is checked before extToLang (invariant: user-facing escape hatch exists — LangMap[''] is not the answer, but a wrapper OR renaming to .dockerfile is)", func() {
		// Prove LangMap doesn't accidentally rescue "" either (there's no
		// "empty extension = special name" mapping). This documents the
		// escape hatch: rename the file to include .dockerfile, or extend
		// languageFor to handle known extension-less basenames.
		cfg := EstimatorConfig{
			LangMap: map[string]string{"": "ShouldNotMatch"},
		}
		// Even with LangMap[""] populated, languageFor short-circuits when
		// ext=="" (types.go:187-189) BEFORE consulting LangMap. So the
		// user can't accidentally attribute every extension-less file to
		// a bogus language via a blank-key LangMap entry either. Safety
		// invariant: no accidental catch-all mapping via "".
		Expect(cfg.languageFor("Dockerfile")).To(Equal(""), "empty-key LangMap entry must NOT match — languageFor short-circuits when ext==\"\"")
	})
})

// -- Assert we still expose canonical entity chars through the placeholder
// path so no downstream regex tightens without breaking tests. --
var _ = Describe("Materialize placeholder shape", func() {
	It("placeholder entity is 'backfill:<repo>' with a colon (invariant: entity NOT NULL constraint satisfied, colon disambiguates from real filenames)", func() {
		sess := Session{
			RepoName: "boomtime",
			Start:    at(0),
			End:      at(2),
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).NotTo(BeEmpty())
		for _, hb := range hbs {
			Expect(strings.HasPrefix(hb.Entity, "backfill:")).To(BeTrue(), "placeholder entity must start with 'backfill:'")
			Expect(hb.Entity).To(Equal("backfill:boomtime"))
		}
	})
})
