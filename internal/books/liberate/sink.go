// sink.go — the storage half of Libation's FileManager: getting a finished M4B
// into the library ATOMICALLY. See docs/design/catalyst-books-liberation-architecture.md §6.
//
// WHY ATOMICITY IS THE WHOLE POINT. The library root is, by design, being watched
// by something else — Audiobookshelf, Plex, Jellyfin. Those scanners index on
// file-appearance. If a 600 MB M4B becomes visible at its final path while it is
// still being written, the scanner indexes a truncated file, caches bad duration
// and chapter data, and (in Audiobookshelf's case) will not re-read it without a
// manual rescan. So a file becomes visible at its final name only once it is
// complete, via a rename that is atomic within the library filesystem.
package liberate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Sink is where liberated files land. The interface exists so that the S3 sink
// the design doc keeps on the table (via internal/shared/objstore) stays a config
// change rather than a refactor — and so the integration test can drive a
// temp-dir sink without ceremony.
type Sink interface {
	// Commit moves the finished file at workPath to relPath inside the sink,
	// atomically from a reader's point of view. Returns the stored relative path.
	// Creates parent directories as needed. Overwrites an existing file at relPath.
	Commit(ctx context.Context, workPath, relPath string) (string, error)
	// Stat reports the size of relPath. A missing object is (0, false, nil) —
	// absence is a clean miss, not an error, because the idempotency check calls
	// this on every book on every sweep.
	Stat(ctx context.Context, relPath string) (size int64, ok bool, err error)
	// Remove deletes relPath. A missing object is NOT an error (idempotent).
	Remove(ctx context.Context, relPath string) error
}

// FSSink is the filesystem Sink: a library root on a PVC or an NFS mount.
type FSSink struct{ Root string }

var _ Sink = (*FSSink)(nil)

// NewFSSink validates the root and returns a filesystem sink. The root must
// already exist — we deliberately do NOT MkdirAll it, because a typo'd or
// unmounted BOOM_BOOKS_LIBRARY_PATH would otherwise silently create a directory
// on the container's ephemeral layer and quietly fill it with audiobooks that
// vanish on the next deploy. An unmounted NFS volume is exactly this failure.
func NewFSSink(root string) (*FSSink, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("liberate: library root is empty (set BOOM_BOOKS_LIBRARY_PATH)")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("liberate: library root %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("liberate: library root %q is not accessible (is the volume mounted?): %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("liberate: library root %q is not a directory", abs)
	}
	return &FSSink{Root: abs}, nil
}

// resolve joins relPath onto the root and verifies the result stays inside it.
// This is the LAST line of defence behind template.go's sanitisation: anything
// that reaches a syscall goes through here first.
func (s *FSSink) resolve(relPath string) (string, error) {
	if relPath == "" {
		return "", errors.New("liberate: empty relative path")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("%w: %q is absolute", ErrEscapesRoot, relPath)
	}
	full := filepath.Clean(filepath.Join(s.Root, relPath))
	// Compare against Root+separator so that a sibling directory whose name
	// merely PREFIXES the root ("/lib" vs "/library-evil") cannot pass.
	if full != s.Root && !strings.HasPrefix(full, s.Root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q", ErrEscapesRoot, relPath)
	}
	return full, nil
}

// Commit implements Sink.
//
// Fast path: os.Rename straight onto the destination — atomic, instant, no extra
// I/O. That works when the work dir and the library are on the same filesystem.
//
// Slow path: when they are NOT, os.Rename fails with EXDEV ("invalid cross-device
// link"). This is the NORMAL case for the intended deployment — work dir on the
// pod's local disk, library on an NFS mount from truenas00 — so it is a first-class
// path, not an edge case. We copy to a ".partial" file INSIDE the library
// filesystem, fsync it, then rename within that filesystem, which is once again
// atomic. Copying straight to the final name would reintroduce exactly the
// partial-file visibility problem the whole design avoids.
func (s *FSSink) Commit(ctx context.Context, workPath, relPath string) (string, error) {
	dst, err := s.resolve(relPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("liberate: mkdir for %q: %w", relPath, err)
	}
	if err := os.Rename(workPath, dst); err == nil {
		return relPath, nil
	} else if !isCrossDevice(err) {
		return "", fmt.Errorf("liberate: commit %q: %w", relPath, err)
	}
	if err := s.copyThenRename(ctx, workPath, dst); err != nil {
		return "", err
	}
	// The source is now redundant. A failure to clean it up is not a failure to
	// commit — the book IS in the library — so it is swallowed here and the work
	// dir's own sweep deals with the leftover.
	_ = os.Remove(workPath)
	return relPath, nil
}

// copyThenRename handles the cross-filesystem case described on Commit.
func (s *FSSink) copyThenRename(ctx context.Context, workPath, dst string) error {
	partial := dst + ".partial"
	// A .partial left by a killed process would otherwise make O_EXCL fail
	// forever; the work is fully re-derivable, so clearing it is safe.
	_ = os.Remove(partial)

	src, err := os.Open(workPath)
	if err != nil {
		return fmt.Errorf("liberate: open work file: %w", err)
	}
	defer src.Close()

	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("liberate: create %q: %w", partial, err)
	}
	if _, err := copyWithContext(ctx, out, src); err != nil {
		out.Close()
		_ = os.Remove(partial)
		return fmt.Errorf("liberate: copy into library: %w", err)
	}
	// fsync before the rename: without it a host crash can leave the rename
	// durable but the CONTENTS not, which is the truncated-file problem again
	// with extra steps.
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(partial)
		return fmt.Errorf("liberate: fsync %q: %w", partial, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("liberate: close %q: %w", partial, err)
	}
	if err := os.Rename(partial, dst); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("liberate: rename into place: %w", err)
	}
	return nil
}

// Stat implements Sink.
func (s *FSSink) Stat(ctx context.Context, relPath string) (int64, bool, error) {
	full, err := s.resolve(relPath)
	if err != nil {
		return 0, false, err
	}
	info, err := os.Stat(full)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("liberate: stat %q: %w", relPath, err)
	}
	return info.Size(), true, nil
}

// Remove implements Sink.
func (s *FSSink) Remove(ctx context.Context, relPath string) error {
	full, err := s.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("liberate: remove %q: %w", relPath, err)
	}
	return nil
}

// copyWithContext is io.Copy that notices cancellation. A cross-device commit of
// a 600 MB file takes real time; without this, cancelling a liberation job would
// not interrupt it.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	var total int64
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
