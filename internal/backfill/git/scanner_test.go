package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// mkTestRepo creates a real on-disk git repo under t.TempDir(), commits
// three files with the caller-supplied timestamps and authors, and
// returns its path. Uses go-git to init + commit — no shelling to git.
type testCommit struct {
	author string
	email  string
	when   time.Time
	file   string
	body   string
}

func mkTestRepo(t *testing.T, commits []testCommit) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for _, c := range commits {
		fp := filepath.Join(dir, c.file)
		if err := os.WriteFile(fp, []byte(c.body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := wt.Add(c.file); err != nil {
			t.Fatalf("Add: %v", err)
		}
		sig := &object.Signature{
			Name:  c.author,
			Email: c.email,
			When:  c.when,
		}
		_, err := wt.Commit(c.body, &git.CommitOptions{
			Author:    sig,
			Committer: sig,
		})
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	return dir
}

func TestScanner_YieldsAllCommitsWhenAllowlistEmpty(t *testing.T) {
	dir := mkTestRepo(t, []testCommit{
		{"Me", "me@example.com", at(60), "a.go", "package a"},
		{"Other", "other@example.com", at(90), "b.py", "print(1)"},
	})
	s, err := NewScanner(dir, EstimatorConfig{})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	var got []string
	for c, err := range s.Iter(context.Background()) {
		if err != nil {
			t.Fatalf("iter: %v", err)
		}
		got = append(got, c.AuthorEmail)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (got=%v)", len(got), got)
	}
}

func TestScanner_FiltersByAuthorEmail(t *testing.T) {
	dir := mkTestRepo(t, []testCommit{
		{"Me", "me@example.com", at(60), "a.go", "package a"},
		{"Other", "other@example.com", at(90), "b.py", "print(1)"},
	})
	s, err := NewScanner(dir, EstimatorConfig{
		AuthorEmails: []string{"me@example.com"},
	})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	var got []string
	for c, err := range s.Iter(context.Background()) {
		if err != nil {
			t.Fatalf("iter: %v", err)
		}
		got = append(got, c.AuthorEmail)
	}
	if len(got) != 1 || got[0] != "me@example.com" {
		t.Fatalf("got = %v, want [me@example.com]", got)
	}
}

func TestScanner_RepoName_UsesBasename(t *testing.T) {
	dir := mkTestRepo(t, []testCommit{
		{"Me", "me@example.com", at(60), "a.go", "package a"},
	})
	s, err := NewScanner(dir, EstimatorConfig{})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	want := filepath.Base(dir)
	if s.RepoName() != want {
		t.Errorf("RepoName() = %q, want %q", s.RepoName(), want)
	}
}

func TestScanner_SinceUntil_ClampsWindow(t *testing.T) {
	dir := mkTestRepo(t, []testCommit{
		{"Me", "me@example.com", at(10), "a.go", "package a"},
		{"Me", "me@example.com", at(60), "b.go", "package b"},
		{"Me", "me@example.com", at(120), "c.go", "package c"},
	})
	s, err := NewScanner(dir, EstimatorConfig{
		Since: at(30),
		Until: at(90),
	})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	var count int
	for c, err := range s.Iter(context.Background()) {
		if err != nil {
			t.Fatalf("iter: %v", err)
		}
		if c.Time.Before(at(30)) || c.Time.After(at(90)) {
			t.Errorf("commit at %v outside window", c.Time)
		}
		count++
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
