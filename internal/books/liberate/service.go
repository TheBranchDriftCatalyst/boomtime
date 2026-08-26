// service.go — the orchestrator: license → voucher → download → remux → commit,
// with the idempotency contract and failure classification that make a sweep
// safe to re-run. This is Libation's FileLiberator equivalent.
// See docs/design/catalyst-books-liberation-architecture.md §4.
package liberate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/notify"
)

// EventLiberated is the notification type published on a successful liberation.
const EventLiberated = "book.liberated"

// coverTimeout bounds the cover-art fetch. A cover is a nice-to-have: if the CDN
// is slow we ship the book without it rather than failing a 600 MB download over
// a JPEG.
const coverTimeout = 30 * time.Second

// CredentialLoader is the narrow slice of the Amazon credential store this
// package needs. *amazon.Store satisfies it. Depending on the interface rather
// than the concrete store keeps liberation testable without standing up
// encryption-at-rest (the key loads once per process via sync.Once, which makes
// it awkward to configure from a test), and states plainly that liberation
// reads credentials and never writes them.
type CredentialLoader interface {
	Load(ctx context.Context, username string) (*amazon.DeviceCredential, error)
}

// Service liberates owned Audible titles.
type Service struct {
	Store     *Store
	Amazon    CredentialLoader
	Sink      Sink
	Decryptor Decryptor
	Logger    *slog.Logger

	// WorkDir is scratch for download + convert. Must NOT be the library.
	WorkDir string
	// Template overrides the naming template; empty uses DefaultTemplate.
	Template string
	// Notify (nil-safe) publishes book.liberated events.
	Notify *notify.Hub

	// Licenser and Fetcher are TEST SEAMS, nil in production (see licenser() /
	// fetcher()). RequestLicense and Fetch both target hard-coded Amazon hosts,
	// so without these the orchestration could only ever be tested against the
	// real API. Injecting them lets the integration test drive the whole
	// pipeline — status transitions, idempotency, failure classification —
	// against a stub, which is the same approach the Kindle ingest uses to
	// exercise SyncUser with a fake library.
	Licenser func(ctx context.Context, cred *amazon.DeviceCredential, asin string) (*LicenseResponse, []byte, error)
	Fetcher  func(ctx context.Context, cred *amazon.DeviceCredential, rawURL, destPath string, p Progress) (int64, error)
}

// licenser returns the injected seam or the real implementation.
func (s *Service) licenser() func(context.Context, *amazon.DeviceCredential, string) (*LicenseResponse, []byte, error) {
	if s.Licenser != nil {
		return s.Licenser
	}
	return RequestLicense
}

// fetcher returns the injected seam or the real implementation.
func (s *Service) fetcher() func(context.Context, *amazon.DeviceCredential, string, string, Progress) (int64, error) {
	if s.Fetcher != nil {
		return s.Fetcher
	}
	return Fetch
}

// Options tunes one liberation.
type Options struct {
	// Force re-liberates even when the idempotency check says the file is good.
	Force bool
	// Progress is called as bytes arrive. The JOB passes a heartbeat here — a
	// download long enough to matter is long enough to be reaped without it.
	Progress Progress
}

// Result describes the outcome of one book.
type Result struct {
	ASIN          string
	Status        string
	RelPath       string
	Bytes         int64
	ContentFormat string
	Skipped       bool
	Duration      time.Duration
}

// LiberateBook runs the whole pipeline for one title.
//
// IDEMPOTENCY CONTRACT (mirrors BackfillUser/SyncUser in the ingest domain):
// already liberated AND the file is present at the recorded size = skip;
// file missing = re-liberate (someone deleted it); size mismatch = re-liberate
// (truncated). Options.Force overrides all three.
func (s *Service) LiberateBook(ctx context.Context, owner, asin string, opts Options) (Result, error) {
	started := time.Now()
	res := Result{ASIN: asin}
	log := s.log().With("owner", owner, "asin", asin)

	item, err := s.Store.LoadItem(ctx, owner, asin)
	if err != nil {
		return res, err
	}

	if !opts.Force {
		if skip, why := s.alreadyLiberated(ctx, item); skip {
			log.Info("liberation skipped", "reason", why, "path", item.AudioPath)
			res.Status, res.Skipped, res.RelPath = StatusLiberated, true, item.AudioPath
			res.Bytes = item.AudioBytes
			return res, nil
		}
	}

	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return res, fmt.Errorf("liberate: %s: %w", asin, err)
	}

	attemptID, aerr := s.Store.StartAttempt(ctx, owner, asin)
	if aerr != nil {
		// History is diagnostics, not correctness — never block a liberation on it.
		log.Warn("could not open attempt row", "err", aerr)
	}
	// finish records the outcome on both the item row and the attempt row.
	finish := func(status, reason string) {
		res.Status = status
		res.Duration = time.Since(started)
		if attemptID > 0 {
			if ferr := s.Store.FinishAttempt(ctx, attemptID, status, res.Bytes, res.Duration, res.ContentFormat, reason); ferr != nil {
				log.Warn("could not close attempt row", "err", ferr)
			}
		}
	}

	// --- 1. license -------------------------------------------------------
	_ = s.Store.SetStatus(ctx, owner, asin, StatusLicensing)
	lic, _, lerr := s.licenser()(ctx, cred, asin)
	if lerr != nil {
		status := StatusFailed
		switch {
		case errors.Is(lerr, ErrLicenseDenied):
			// TERMINAL. Retrying a Denied title in a loop is how an account gets
			// flagged, so this must never look like a transient failure.
			status = StatusDenied
		case errors.Is(lerr, ErrNotAudiobook):
			// TERMINAL for a different reason: podcasts and other non-audio
			// assets live in the same library but can never be licensed as
			// audiobooks. Marked unsupported_format so ListUnliberated skips
			// them and a re-sweep stops re-requesting them forever.
			status = StatusUnsupportedFormat
		}
		_ = s.Store.MarkFailed(ctx, owner, asin, status, lerr.Error(), "")
		finish(status, lerr.Error())
		return res, lerr
	}
	ref := lic.ContentLicense.ContentMetadata.ContentReference
	res.ContentFormat = ref.ContentFormat

	// --- 2. voucher -------------------------------------------------------
	key, verr := DecryptVoucher(cred, asin, lic.ContentLicense.LicenseResponse)
	if verr != nil {
		_ = s.Store.MarkFailed(ctx, owner, asin, StatusFailed, verr.Error(), res.ContentFormat)
		finish(StatusFailed, verr.Error())
		return res, verr
	}

	// Work files live in a per-ASIN directory so a failed run cleans up in one
	// call and two concurrent books cannot collide on a temp name.
	workDir, werr := s.makeWorkDir(asin)
	if werr != nil {
		_ = s.Store.MarkFailed(ctx, owner, asin, StatusFailed, werr.Error(), res.ContentFormat)
		finish(StatusFailed, werr.Error())
		return res, werr
	}
	defer os.RemoveAll(workDir)

	// --- 3. download ------------------------------------------------------
	_ = s.Store.SetStatus(ctx, owner, asin, StatusDownloading)
	srcPath := filepath.Join(workDir, asin+".aaxc")
	n, ferr := s.fetcher()(ctx, cred, lic.ContentLicense.ContentMetadata.ContentURL.OfflineURL, srcPath, opts.Progress)
	if ferr != nil {
		_ = s.Store.MarkFailed(ctx, owner, asin, StatusFailed, ferr.Error(), res.ContentFormat)
		finish(StatusFailed, ferr.Error())
		return res, ferr
	}
	log.Info("downloaded", "bytes", n, "contentFormat", res.ContentFormat)

	// --- 4. remux ---------------------------------------------------------
	_ = s.Store.SetStatus(ctx, owner, asin, StatusConverting)
	meta := s.metadataFor(item)
	req := DecryptRequest{
		SrcPath: srcPath,
		DstPath: filepath.Join(workDir, asin+".m4b"),
		Key:     key,
		Meta:    meta,
	}
	if doc := BuildFFMetadata(lic.ContentLicense.ContentMetadata.ChapterInfo); doc != "" {
		chapPath := filepath.Join(workDir, "chapters.txt")
		if werr := os.WriteFile(chapPath, []byte(doc), 0o644); werr == nil {
			req.FFMetadataPath = chapPath
		} else {
			// A book without chapter marks is worse than one with, but far better
			// than no book — carry on untagged rather than failing the run.
			log.Warn("could not write chapters file; continuing without chapters", "err", werr)
		}
	}
	if meta.CoverURL != "" {
		if cover, cerr := s.fetchCover(ctx, meta.CoverURL, workDir); cerr == nil {
			req.CoverPath = cover
		} else {
			log.Warn("cover fetch failed; continuing without cover", "err", cerr)
		}
	}

	if derr := s.Decryptor.Decrypt(ctx, req); derr != nil {
		status := s.classifyRemuxFailure(res.ContentFormat)
		_ = s.Store.MarkFailed(ctx, owner, asin, status, derr.Error(), res.ContentFormat)
		finish(status, derr.Error())
		return res, derr
	}

	// --- 5. commit --------------------------------------------------------
	relPath, rerr := RenderPath(s.Template, meta.BookMeta())
	if rerr != nil {
		_ = s.Store.MarkFailed(ctx, owner, asin, StatusFailed, rerr.Error(), res.ContentFormat)
		finish(StatusFailed, rerr.Error())
		return res, rerr
	}
	stored, cerr := s.Sink.Commit(ctx, req.DstPath, relPath)
	if cerr != nil {
		_ = s.Store.MarkFailed(ctx, owner, asin, StatusFailed, cerr.Error(), res.ContentFormat)
		finish(StatusFailed, cerr.Error())
		return res, cerr
	}
	size, _, _ := s.Sink.Stat(ctx, stored)

	res.RelPath, res.Bytes = stored, size
	if merr := s.Store.MarkLiberated(ctx, owner, asin, stored, size, res.ContentFormat); merr != nil {
		finish(StatusFailed, merr.Error())
		return res, merr
	}
	finish(StatusLiberated, "")
	log.Info("liberated", "path", stored, "bytes", size, "took", res.Duration)
	s.publishLiberated(owner, meta, stored)
	return res, nil
}

// LiberateAll returns the ASINs that still need liberating, oldest first. It
// does NOT run them — the caller enqueues one job per ASIN so each book gets its
// own retry, heartbeat, and concurrency slot rather than one giant job that
// loses everything when it dies at book 400.
func (s *Service) LiberateAll(ctx context.Context, owner string, limit int) ([]string, error) {
	return s.Store.ListUnliberated(ctx, owner, limit)
}

// alreadyLiberated implements the idempotency contract, returning why.
func (s *Service) alreadyLiberated(ctx context.Context, item Item) (bool, string) {
	if item.LiberationStatus != StatusLiberated || item.AudioPath == "" {
		return false, ""
	}
	size, ok, err := s.Sink.Stat(ctx, item.AudioPath)
	switch {
	case err != nil:
		return false, "stat failed, re-liberating"
	case !ok:
		return false, "file missing, re-liberating"
	case item.AudioBytes > 0 && size != item.AudioBytes:
		return false, "size mismatch, re-liberating"
	default:
		return true, "already liberated"
	}
}

// classifyRemuxFailure decides what a failed remux MEANS.
//
// ffmpeg does not distinguish "I cannot parse this codec" from "the disk filled
// up" in its exit status, so this leans on content_format instead: the AAX_*
// family is the AAC-LC set ffmpeg handles, and a failure on anything else is
// much more likely to be the xHE-AAC problem that motivates the native decoder.
// Being wrong in either direction is cheap — both statuses are visible and both
// are re-runnable — but the unsupported_codec COUNT is what triggers epic D, so
// it is worth attributing rather than lumping everything into 'failed'.
func (s *Service) classifyRemuxFailure(contentFormat string) string {
	if contentFormat != "" && !strings.HasPrefix(contentFormat, "AAX_") {
		return StatusUnsupportedCodec
	}
	return StatusFailed
}

// metadataFor prefers the rich raw_meta blob and falls back to the row's
// denormalised columns, so a stale or malformed blob costs tag richness rather
// than the whole liberation.
func (s *Service) metadataFor(item Item) Metadata {
	if m, ok := MetadataFromRaw(item.RawMeta); ok {
		if m.ASIN == "" {
			m.ASIN = item.ASIN
		}
		return m
	}
	m := Metadata{Title: item.Title, ASIN: item.ASIN}
	if item.Authors != "" {
		m.Authors = []string{item.Authors}
	}
	return m
}

// makeWorkDir creates a per-ASIN scratch directory.
func (s *Service) makeWorkDir(asin string) (string, error) {
	base := s.WorkDir
	if strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	// The ASIN is external data; sanitise it before it becomes a directory name.
	dir := filepath.Join(base, "liberate-"+SanitizeSegment(asin))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("liberate: work dir: %w", err)
	}
	return dir, nil
}

// fetchCover downloads the cover image. Unauthenticated: product images are on a
// public CDN, unlike the content URL.
func (s *Service) fetchCover(ctx context.Context, rawURL, workDir string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, coverTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("cover: HTTP %d", resp.StatusCode)
	}
	dest := filepath.Join(workDir, "cover.jpg")
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// 16 MiB is far more than any cover; the cap stops a redirect to something
	// large from filling the work volume.
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 16<<20)); err != nil {
		return "", err
	}
	return dest, nil
}

func (s *Service) publishLiberated(owner string, meta Metadata, relPath string) {
	if s.Notify == nil {
		return
	}
	s.Notify.Publish(notify.Event{
		Type:  EventLiberated,
		Owner: owner,
		Title: "Liberated: " + meta.Title,
		Body:  meta.AuthorString(),
		Data: map[string]any{
			"asin": meta.ASIN,
			"path": relPath,
		},
		At: time.Now(),
	})
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// compile-time assertion that the production store satisfies the seam.
var _ CredentialLoader = (*amazon.Store)(nil)
