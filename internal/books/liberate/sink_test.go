package liberate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write work file: %v", err)
	}
	return p
}

func newTestSink(t *testing.T) *FSSink {
	t.Helper()
	s, err := NewFSSink(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSSink: %v", err)
	}
	return s
}

// NewFSSink must refuse a root that does not exist rather than creating it.
// A typo'd or unmounted BOOM_BOOKS_LIBRARY_PATH would otherwise fill the
// container's ephemeral layer with audiobooks that vanish on the next deploy —
// the single most expensive silent failure this package can have.
func TestNewFSSinkRefusesMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")

	_, err := NewFSSink(missing)
	if err == nil {
		t.Fatal("NewFSSink accepted a nonexistent root")
	}
	if !strings.Contains(err.Error(), "not accessible") {
		t.Errorf("err = %v, want an accessibility complaint", err)
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("NewFSSink created the root directory; it must never do that")
	}
}

func TestNewFSSinkRefusesNonDirectoryAndEmpty(t *testing.T) {
	file := writeWorkFile(t, t.TempDir(), "a-file", "x")

	if _, err := NewFSSink(file); err == nil {
		t.Error("NewFSSink accepted a regular file as the library root")
	}
	if _, err := NewFSSink("   "); err == nil {
		t.Error("NewFSSink accepted an empty root")
	}
}

func TestCommitPlacesFileAndCreatesParents(t *testing.T) {
	sink := newTestSink(t)
	work := writeWorkFile(t, t.TempDir(), "book.m4b", "AUDIOBOOK")
	rel := "Neal Stephenson/Snow Crash/Snow Crash.m4b"

	got, err := sink.Commit(context.Background(), work, rel)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got != rel {
		t.Errorf("Commit returned %q, want %q", got, rel)
	}
	body, err := os.ReadFile(filepath.Join(sink.Root, rel))
	if err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	if string(body) != "AUDIOBOOK" {
		t.Errorf("committed content = %q", body)
	}
	if _, err := os.Stat(work); !errors.Is(err, os.ErrNotExist) {
		t.Error("work file survived a same-filesystem commit; it should have been renamed away")
	}
}

// The cross-filesystem path is the NORMAL one for the intended deployment (work
// dir on pod-local disk, library on NFS), so it gets a direct test rather than
// being left to whatever the test machine's mount table happens to be. Driving
// copyThenRename directly exercises the exact code Commit falls back to on EXDEV.
func TestCopyThenRenameIsTheCrossDeviceFallback(t *testing.T) {
	sink := newTestSink(t)
	work := writeWorkFile(t, t.TempDir(), "book.m4b", "CROSS DEVICE PAYLOAD")
	dst := filepath.Join(sink.Root, "Author", "Title.m4b")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := sink.copyThenRename(context.Background(), work, dst); err != nil {
		t.Fatalf("copyThenRename: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "CROSS DEVICE PAYLOAD" {
		t.Errorf("content = %q", body)
	}
	// No .partial may survive — a scanner that indexes the directory must not
	// find a half-written sibling.
	if _, err := os.Stat(dst + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Error(".partial file left behind after a successful commit")
	}
}

// A .partial left by a killed process must not wedge the path forever: the work
// is fully re-derivable, so a retry has to clear it.
func TestCopyThenRenameClearsStalePartial(t *testing.T) {
	sink := newTestSink(t)
	work := writeWorkFile(t, t.TempDir(), "book.m4b", "FRESH")
	dst := filepath.Join(sink.Root, "Title.m4b")
	if err := os.WriteFile(dst+".partial", []byte("STALE HALF WRITE"), 0o644); err != nil {
		t.Fatalf("seed stale partial: %v", err)
	}

	if err := sink.copyThenRename(context.Background(), work, dst); err != nil {
		t.Fatalf("copyThenRename over a stale partial: %v", err)
	}
	body, _ := os.ReadFile(dst)
	if string(body) != "FRESH" {
		t.Errorf("content = %q, want the fresh copy", body)
	}
}

func TestCommitOverwritesExisting(t *testing.T) {
	sink := newTestSink(t)
	rel := "Author/Title.m4b"

	first := writeWorkFile(t, t.TempDir(), "v1.m4b", "VERSION ONE")
	if _, err := sink.Commit(context.Background(), first, rel); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	second := writeWorkFile(t, t.TempDir(), "v2.m4b", "VERSION TWO")
	if _, err := sink.Commit(context.Background(), second, rel); err != nil {
		t.Fatalf("second commit: %v", err)
	}

	body, _ := os.ReadFile(filepath.Join(sink.Root, rel))
	if string(body) != "VERSION TWO" {
		t.Errorf("content = %q, want the re-liberated version", body)
	}
}

// resolve is the last line of defence behind template.go. Even though RenderPath
// will not produce these, anything reaching a syscall goes through here — so a
// future caller that builds a path some other way is still contained.
func TestSinkRejectsEscapingPaths(t *testing.T) {
	sink := newTestSink(t)
	escapes := []string{
		"../outside.m4b",
		"a/../../outside.m4b",
		"/etc/passwd",
		"",
	}
	for _, rel := range escapes {
		t.Run(rel, func(t *testing.T) {
			work := writeWorkFile(t, t.TempDir(), "book.m4b", "x")
			if _, err := sink.Commit(context.Background(), work, rel); err == nil {
				t.Errorf("Commit accepted escaping path %q", rel)
			}
			if _, _, err := sink.Stat(context.Background(), rel); err == nil {
				t.Errorf("Stat accepted escaping path %q", rel)
			}
			if err := sink.Remove(context.Background(), rel); err == nil {
				t.Errorf("Remove accepted escaping path %q", rel)
			}
		})
	}
}

// A directory that merely shares a name PREFIX with the root must not be
// reachable — the reason resolve compares against Root+separator.
func TestSinkRejectsSiblingPrefixEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "library-evil"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink, err := NewFSSink(root)
	if err != nil {
		t.Fatalf("NewFSSink: %v", err)
	}

	if _, _, err := sink.Stat(context.Background(), "../library-evil/loot.m4b"); err == nil {
		t.Error("sibling directory sharing the root's name prefix was reachable")
	}
}

// Absence is a clean miss, not an error: the idempotency check calls Stat on
// every book on every sweep, so a missing file is the common case.
func TestStatMissingIsCleanMiss(t *testing.T) {
	sink := newTestSink(t)

	size, ok, err := sink.Stat(context.Background(), "Author/Nope.m4b")
	if err != nil {
		t.Fatalf("Stat on a missing file errored: %v", err)
	}
	if ok || size != 0 {
		t.Errorf("Stat = (%d, %v), want (0, false)", size, ok)
	}
}

func TestStatReportsSizeAndRemoveIsIdempotent(t *testing.T) {
	sink := newTestSink(t)
	rel := "Author/Title.m4b"
	work := writeWorkFile(t, t.TempDir(), "book.m4b", "0123456789")
	if _, err := sink.Commit(context.Background(), work, rel); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	size, ok, err := sink.Stat(context.Background(), rel)
	if err != nil || !ok {
		t.Fatalf("Stat = (%d, %v, %v)", size, ok, err)
	}
	if size != 10 {
		t.Errorf("size = %d, want 10", size)
	}
	if err := sink.Remove(context.Background(), rel); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Second remove must be a no-op, so a retry after a partial failure is safe.
	if err := sink.Remove(context.Background(), rel); err != nil {
		t.Fatalf("second Remove was not idempotent: %v", err)
	}
}

// A cross-device commit of a 600 MB file takes real time; cancelling the
// liberation job has to actually interrupt it.
func TestCopyWithContextHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n, err := copyWithContext(ctx, io_Discard{}, strings.NewReader(strings.Repeat("x", 4<<20)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n != 0 {
		t.Errorf("copied %d bytes after cancellation, want 0", n)
	}
}

// io_Discard is a local sink so the test does not depend on io.Discard's
// WriteTo fast path, which would bypass the loop under test.
type io_Discard struct{}

func (io_Discard) Write(p []byte) (int, error) { return len(p), nil }
