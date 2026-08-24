// crossdevice.go — EXDEV detection for the sink's commit path.
//
// Split into its own file (rather than an inline errors.Is in sink.go) because
// this is the ONE place the cross-filesystem question is answered, and the
// answer decides between the atomic fast path and the copy-then-rename slow
// path. Getting it wrong in the "false negative" direction turns a normal NFS
// deployment into a hard failure on every commit, so it is worth being explicit.
package liberate

import (
	"errors"
	"strings"
	"syscall"
)

// isCrossDevice reports whether err is the kernel refusing to rename across
// filesystems (EXDEV, "invalid cross-device link").
//
// os.Rename wraps its cause in *os.LinkError; errors.Is unwraps that for us.
// The string fallback exists for platforms/filesystems (some FUSE and NFS
// clients) that surface a cross-device rename without mapping it cleanly onto
// EXDEV — cheap insurance against a whole class of "works on my laptop, fails on
// the NFS mount" bug, and harmless when EXDEV already matched.
func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "cross-device")
}
