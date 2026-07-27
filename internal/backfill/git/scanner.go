// scanner.go: go-git-based repo iteration for the CLI backfill.
//
// The Scanner opens a repo via go-git.PlainOpen (which just parses the
// .git directory in-process — no shelling to `git`), walks the HEAD log
// oldest-first, filters by author email + optional [Since, Until]
// window, computes the per-commit diff stats against the first parent,
// and yields one Commit per accepted commit.
//
// Merges: skipped. Merge commits typically have no meaningful diff
// against the first parent AND their timestamps skew clustering (a
// squash-merge from three days ago suddenly lands "now"). For the
// backfill purpose — reconstructing time-on-code from commit rhythm —
// merges are noise.
//
// Author-email match is case-insensitive (see EstimatorConfig.EmailAllowed).
//
// The iteration is exposed via a Go 1.23 iter.Seq2[Commit, error] so
// callers can `for commit, err := range scanner.Iter(ctx) { ... }` and
// break out early on ctx cancellation without loading the whole log.

package git

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// Scanner wraps a single go-git repository. Constructed via NewScanner;
// call Iter() to consume the log.
//
// A Scanner is single-use — Iter returns a fresh iterator each call, but
// the underlying repo handle is shared (repo objects are safe to reuse
// for read-only walks per go-git's docs).
type Scanner struct {
	path string
	repo *git.Repository
	cfg  EstimatorConfig
}

// NewScanner opens the repo at path (which may be the working tree OR
// the .git dir itself — go-git.PlainOpen accepts both) and prepares it
// for iteration under cfg.
//
// The repo name is derived from the working tree's basename (or, when a
// bare .git dir was passed, the parent basename). Consumers key session
// aggregates by RepoName so the same-named repo in different roots
// contributes to the same "project" in downstream stats.
func NewScanner(repoPath string, cfg EstimatorConfig) (*Scanner, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", repoPath, err)
	}
	return &Scanner{
		path: repoPath,
		repo: repo,
		cfg:  cfg.defaults(),
	}, nil
}

// RepoName returns the basename used to tag every yielded Commit.
// Exposed so the CLI can print progress like "scanning REPO... N commits"
// without duplicating the derivation.
func (s *Scanner) RepoName() string {
	// PlainOpen normalizes both working-tree and bare .git paths, but the
	// name we want is the visible project name — so strip a trailing
	// ".git" if the caller passed a bare dir directly.
	name := filepath.Base(s.path)
	if name == ".git" {
		name = filepath.Base(filepath.Dir(s.path))
	}
	return name
}

// Iter walks HEAD's log oldest-first (topological, ordered by commit
// time) and yields every commit that matches the configured filters.
// Merge commits (>1 parent) are skipped — see package doc for rationale.
//
// The iter.Seq2 shape lets the caller break on ctx cancel without
// draining the log. Errors from go-git surface as (zero-Commit, err) and
// callers should treat the first non-nil error as a hard stop.
func (s *Scanner) Iter(ctx context.Context) iter.Seq2[Commit, error] {
	return func(yield func(Commit, error) bool) {
		// go-git's Log iteration is "reverse-chronological from HEAD by
		// default". We keep that order because clustering is order-
		// insensitive (it sorts by time), and it's cheaper than
		// building a topological order first.
		//
		// LogOptions.All=false because we only want reachable-from-HEAD
		// commits; a stale branch tip left in a fetch shouldn't add
		// heartbeats to the timeline.
		head, err := s.repo.Head()
		if err != nil {
			// Empty repo / detached-no-branch: treat as "no commits".
			// Callers see zero yields and continue with the next repo.
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				return
			}
			yield(Commit{}, fmt.Errorf("HEAD: %w", err))
			return
		}

		iter, err := s.repo.Log(&git.LogOptions{From: head.Hash()})
		if err != nil {
			yield(Commit{}, fmt.Errorf("log: %w", err))
			return
		}
		defer iter.Close()

		name := s.RepoName()
		err = iter.ForEach(func(c *object.Commit) error {
			// Early-cancel via ctx is the whole reason we accept ctx —
			// a "walk every ~/code repo" run needs to interrupt cleanly.
			select {
			case <-ctx.Done():
				return storer.ErrStop
			default:
			}

			// Skip merges (>1 parent): their diff is meaningless.
			if c.NumParents() > 1 {
				return nil
			}
			// Author email allowlist. Empty allowlist accepts anyone
			// (EstimatorConfig.EmailAllowed enforces the semantics).
			if !s.cfg.EmailAllowed(c.Author.Email) {
				return nil
			}
			// [Since, Until] window. Zero-time on either end means
			// "unbounded on that side".
			t := c.Author.When.UTC()
			if !s.cfg.Since.IsZero() && t.Before(s.cfg.Since) {
				// Log is newest-first: once we're older than Since we can
				// stop, since every subsequent commit is even older.
				return storer.ErrStop
			}
			if !s.cfg.Until.IsZero() && t.After(s.cfg.Until) {
				// Newer than Until: skip forward (not stop — we may not
				// have hit our window yet if the walk started at HEAD
				// and the range excludes recent commits).
				return nil
			}

			// Diff stats against the first parent. Root commit vs no
			// parent → treat every file as added.
			files, added, deleted, derr := diffStatsAgainstFirstParent(c)
			if derr != nil {
				// A single busted commit shouldn't kill the whole walk;
				// yield an error frame but keep going so the caller can
				// log-and-continue if they choose. We yield with the
				// still-useful Commit metadata attached so an operator
				// sees "which commit blew up" not just an opaque error.
				partial := Commit{
					RepoName:    name,
					Hash:        c.Hash.String(),
					AuthorEmail: c.Author.Email,
					Time:        t,
				}
				if !yield(partial, fmt.Errorf("diff %s: %w", c.Hash, derr)) {
					return storer.ErrStop
				}
				return nil
			}

			out := Commit{
				RepoName:     name,
				Hash:         c.Hash.String(),
				AuthorEmail:  c.Author.Email,
				Time:         t,
				FilesChanged: files,
				LinesAdded:   added,
				LinesDeleted: deleted,
			}
			if !yield(out, nil) {
				return storer.ErrStop
			}
			return nil
		})
		if err != nil && !errors.Is(err, storer.ErrStop) {
			yield(Commit{}, fmt.Errorf("log walk: %w", err))
		}
	}
}

// storer.ErrStop is the go-git-blessed "abort the walk" sentinel; we
// return it from the ForEach callback when ctx is cancelled OR the
// caller broke out of their `for ... range` loop. Sniffed via
// errors.Is in the outer error path so it doesn't get surfaced as a
// real error.

// diffStatsAgainstFirstParent returns (filesChanged, linesAdded,
// linesDeleted) for c against its first parent. For root commits (no
// parent) every file in the tree is treated as an addition — this
// matches what `git log --stat` reports for the initial commit.
//
// Uses go-git's tree.Diff + Stats path. The returned []FileStat is a
// list of per-file (name, addition, deletion) which we sum + collect.
func diffStatsAgainstFirstParent(c *object.Commit) ([]string, int, int, error) {
	stats, err := c.Stats()
	if err != nil {
		return nil, 0, 0, err
	}
	files := make([]string, 0, len(stats))
	var added, deleted int
	for _, s := range stats {
		if s.Name == "" {
			continue
		}
		files = append(files, s.Name)
		added += s.Addition
		deleted += s.Deletion
	}
	return files, added, deleted, nil
}
