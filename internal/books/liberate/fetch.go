// fetch.go — step 3 of liberation: stream the AAXC down from Amazon's CDN.
// See docs/design/catalyst-books-liberation-architecture.md §2.3.
//
// This deliberately does NOT go through amazon.SignedGet. That helper caps its
// read at 64 MiB via io.LimitReader — correct for an API response, catastrophic
// for an audiobook, where it would silently truncate a 600 MB file into
// something that looks like a successful download. Liberation gets its own
// unbounded streaming path with an explicit length check instead.
//
// WHAT THE 403 ACTUALLY WAS — corrected 2026-08-25 after a production incident.
//
// The probe established that a bare GET returns 403, and the first version of
// this file INFERRED from that that the ADP headers were the missing ingredient.
// That inference was wrong. With ADP headers attached, every download still
// returned CloudFront's "403 ERROR / Request blocked" page — an AWS WAF block,
// not an auth failure (an Audible auth failure is a JSON error body, not a
// CloudFront interstitial).
//
// Checking what Libation actually does settled it (rmcrackan/Libation,
// Source/AaxDecrypter/AudiobookDownloadBase.cs):
//
//	new NetworkFileStream(tempFilePath, new Uri(DownloadOptions.DownloadUrl), 0,
//	    new() { { "User-Agent", DownloadOptions.UserAgent } });
//
// It sets ONE header — User-Agent — and NO device-auth headers whatsoever. The
// presigned URL carries its own authorization in its query string; attaching ADP
// signing headers to a CDN request is an anomaly a WAF flags, and Go's default
// "Go-http-client/1.1" User-Agent is one it blocks outright.
//
// So this now does exactly what Libation does: the paired Audible-iOS
// User-Agent (amazon.DownloadUserAgent) and nothing else.
package liberate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// ErrShortRead means the transfer ended before Content-Length bytes arrived.
// It is RETRYABLE and must be distinguished from a corrupt-but-complete file:
// silently accepting a short read is how you end up with a library full of
// audiobooks that stop halfway through.
var ErrShortRead = errors.New("liberate: download ended early (short read)")

// downloadTimeout bounds one whole book transfer. An 18-hour audiobook at a few
// hundred MB needs real time on a slow link, so this is generous — the job's
// context cancellation is the responsive stop, not this.
const downloadTimeout = 2 * time.Hour

// fetchBufSize is the copy chunk. 1 MiB balances syscall overhead against how
// often Progress fires (and therefore how often the job can heartbeat).
const fetchBufSize = 1 << 20

// Progress reports transfer advancement. total is -1 when the server sent no
// Content-Length. It is called at most once per chunk, and the liberation job
// uses it to HEARTBEAT — per the deploy-resilience work, a job that stops
// heartbeating gets reaped, and a multi-minute download is exactly long enough
// to be killed mid-flight.
type Progress func(written, total int64)

// fetchClient is the download transport. Separate from the amazon package's
// shared client because that one carries a 30s timeout suitable for API calls
// and hostile to a 600 MB transfer.
var fetchClient = &http.Client{Timeout: downloadTimeout}

// Fetch streams the licensed AAXC at rawURL into destPath.
//
// Returns the number of bytes written. On any failure the partial file is
// removed: a leftover partial is indistinguishable from a complete download to
// the idempotency check, and re-deriving it is cheap compared to shipping a
// truncated book into the library.
func Fetch(ctx context.Context, cred *amazon.DeviceCredential, rawURL, destPath string, progress Progress) (int64, error) {
	if cred == nil {
		return 0, amazon.ErrNotRegistered
	}
	if rawURL == "" {
		return 0, errors.New("liberate: empty download url")
	}
	// Parsed purely to reject junk before we open a connection — nothing else
	// needs the components now that the request carries no signature over them.
	if _, err := url.Parse(rawURL); err != nil {
		return 0, fmt.Errorf("liberate: download url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	// The ONLY header Libation sets, and the only one we set. See the package
	// note for why the ADP headers were removed rather than kept "just in case":
	// they are an anomaly on a presigned CDN URL, and keeping them would leave
	// the incident's actual cause ambiguous.
	req.Header.Set("User-Agent", amazon.DownloadUserAgent)

	resp, err := fetchClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("liberate: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a little of the body for the error message, but never the whole
		// thing — an error response from a CDN edge can still be large.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		// Name a WAF block for what it is. Dumping 500 characters of CloudFront
		// HTML into every job error is how the last incident stayed ambiguous for
		// as long as it did.
		if resp.StatusCode == http.StatusForbidden && strings.Contains(string(snippet), "Request blocked") {
			return 0, fmt.Errorf("liberate: download: HTTP 403 CloudFront WAF block, NOT an auth failure "+
				"(sending User-Agent=%q, no auth headers, matching Libation)", amazon.DownloadUserAgent)
		}
		return 0, fmt.Errorf("liberate: download: HTTP %d: %s", resp.StatusCode, truncate(string(snippet), 512))
	}

	written, err := streamToFile(ctx, destPath, resp.Body, resp.ContentLength, progress)
	if err != nil {
		_ = os.Remove(destPath)
		return written, err
	}
	// The length check is the whole reason this path exists rather than reusing
	// SignedGet. -1 means the server declined to say, in which case we have
	// nothing to verify against and accept what arrived.
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		_ = os.Remove(destPath)
		return written, fmt.Errorf("%w: got %d of %d bytes", ErrShortRead, written, resp.ContentLength)
	}
	return written, nil
}

// streamToFile copies src into a newly created file at destPath, reporting
// progress and honouring cancellation between chunks.
func streamToFile(ctx context.Context, destPath string, src io.Reader, total int64, progress Progress) (int64, error) {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("liberate: create %q: %w", destPath, err)
	}
	defer out.Close()

	buf := make([]byte, fetchBufSize)
	var written int64
	for {
		if cerr := ctx.Err(); cerr != nil {
			return written, cerr
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := out.Write(buf[:n])
			written += int64(w)
			if werr != nil {
				return written, fmt.Errorf("liberate: write: %w", werr)
			}
			if progress != nil {
				progress(written, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			// A mid-transfer network error is a short read, not a corrupt file —
			// classify it as retryable rather than letting it look like a bad book.
			return written, fmt.Errorf("%w: %v", ErrShortRead, rerr)
		}
	}
	// fsync so a host crash between here and the remux cannot leave a file that
	// is present, correctly sized, and full of zeroes.
	if err := out.Sync(); err != nil {
		return written, fmt.Errorf("liberate: fsync %q: %w", destPath, err)
	}
	return written, nil
}
