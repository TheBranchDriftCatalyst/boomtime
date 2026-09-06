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

// recordTimeout bounds the detached writes that record a run's OUTCOME. Short,
// because these run while the process may already be shutting down: a few seconds
// is enough for two UPDATEs and does not delay a SIGTERM meaningfully.
const recordTimeout = 10 * time.Second

// workDirPrefix names every scratch directory this package creates, so
// sweepStaleWorkDirs can recognise its own leftovers and nothing else.
const workDirPrefix = "liberate-"

// staleWorkDirAge is how long a scratch directory must sit untouched before the
// sweep treats it as abandoned. Generous on purpose: a live 600 MB download over
// a slow CDN is hours at worst, never a day, so nothing in range can still be in
// use — and the cost of being wrong is deleting a running job's work.
const staleWorkDirAge = 24 * time.Hour

// ErrPathCollision means the rendered library path already belongs to a DIFFERENT
// library item and no disambiguated path was free either. Committing anyway would
// overwrite another book's audio, so the run fails instead.
var ErrPathCollision = errors.New("liberate: library path already belongs to another title")

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
	// finish records the outcome on the attempt row. It runs under a context
	// DETACHED from the job's (recordCtx) for the same reason recordFailure does:
	// the outcome most worth recording is the one caused by that context being
	// cancelled.
	finish := func(status, reason string) {
		res.Status = status
		res.Duration = time.Since(started)
		if attemptID > 0 {
			rctx, cancel := recordCtx(ctx)
			defer cancel()
			if ferr := s.Store.FinishAttempt(rctx, attemptID, status, res.Bytes, res.Duration, res.ContentFormat, reason); ferr != nil {
				log.Warn("could not close attempt row", "err", ferr)
			}
		}
	}
	// fail records one failed outcome on BOTH the item row and the attempt row
	// and returns the values LiberateBook hands back to its caller.
	fail := func(status string, cause error) (Result, error) {
		status, reason := s.recordFailure(ctx, owner, asin, status, cause, res.ContentFormat)
		finish(status, reason)
		return res, cause
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
		return fail(status, lerr)
	}
	ref := lic.ContentLicense.ContentMetadata.ContentReference
	res.ContentFormat = ref.ContentFormat

	// --- 2. voucher -------------------------------------------------------
	key, verr := DecryptVoucher(cred, asin, lic.ContentLicense.LicenseResponse)
	if verr != nil {
		return fail(StatusFailed, verr)
	}

	// Work files live in a per-RUN directory so a failed run cleans up in one
	// call and no two runs can collide on a temp name.
	workDir, werr := s.makeWorkDir(asin)
	if werr != nil {
		return fail(StatusFailed, werr)
	}
	defer os.RemoveAll(workDir)

	// --- 3. download ------------------------------------------------------
	_ = s.Store.SetStatus(ctx, owner, asin, StatusDownloading)
	srcPath := filepath.Join(workDir, asin+".aaxc")
	n, ferr := s.fetcher()(ctx, cred, lic.ContentLicense.ContentMetadata.ContentURL.OfflineURL, srcPath, opts.Progress)
	if ferr != nil {
		return fail(StatusFailed, ferr)
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
		return fail(s.classifyRemuxFailure(res.ContentFormat), derr)
	}

	// --- 5. commit --------------------------------------------------------
	relPath, rerr := RenderPath(s.Template, meta.BookMeta())
	if rerr != nil {
		return fail(StatusFailed, rerr)
	}
	// The template carries no guaranteed-unique discriminator, and Commit
	// overwrites — so a path another book already owns must be resolved BEFORE
	// the rename, not discovered afterwards from a destroyed audiobook.
	relPath, rerr = s.resolvePathCollision(ctx, owner, asin, relPath)
	if rerr != nil {
		return fail(StatusFailed, rerr)
	}
	// Measure the remux output while it still exists locally: Commit renames (or
	// copies and unlinks) it, so this is the last moment it can be sized without
	// the sink. It is the fallback when the post-commit Stat cannot answer.
	localSize := localFileSize(req.DstPath)
	stored, cerr := s.Sink.Commit(ctx, req.DstPath, relPath)
	if cerr != nil {
		return fail(StatusFailed, cerr)
	}
	size, ok, serr := s.Sink.Stat(ctx, stored)
	if serr != nil || !ok {
		// Discarding this used to record audio_bytes=0, which permanently
		// disables the size-mismatch half of the idempotency contract for the
		// row: a later truncation would then read as "already liberated" on
		// every sweep, forever.
		log.Warn("post-commit stat did not answer; falling back to the remux output size",
			"path", stored, "present", ok, "err", serr, "fallbackBytes", localSize)
		size = localSize
	}
	if size <= 0 {
		return fail(StatusFailed, fmt.Errorf("liberate: committed %q but could not determine its size", stored))
	}

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

// makeWorkDir creates a per-RUN scratch directory, and opportunistically clears
// scratch left behind by runs that never got to clean up after themselves.
//
// PER-RUN, NOT PER-ASIN. The directory used to be "liberate-<asin>", which is
// stable across runs — and two runs for the SAME ASIN are entirely reachable: the
// API's 409 guard reads liberation_status, which a queued-but-unstarted job has
// not set yet, so a double-click enqueues two jobs, and a sweep re-enqueues ASINs
// whose per-book jobs are still queued. With the per-kind cap at 2 both are
// claimed together on one pod, and then they share srcPath: run B's O_TRUNC
// create (fetch.go streamToFile) zeroes run A's half-downloaded AAXC mid-write,
// so A can remux a partially-zeroed file and commit a corrupt M4B marked
// liberated — or whichever run finishes first deletes the other's files from
// under it via the deferred RemoveAll. MkdirTemp gives every run its own dir, so
// the concurrency is simply harmless.
//
// The name keeps the "liberate-<asin>-" prefix so a directory is still
// identifiable by eye and by sweepStaleWorkDirs.
func (s *Service) makeWorkDir(asin string) (string, error) {
	base := s.WorkDir
	if strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("liberate: work dir base: %w", err)
	}
	s.sweepStaleWorkDirs(base)
	// The ASIN is external data; sanitise it before it becomes a directory name.
	dir, err := os.MkdirTemp(base, workDirPrefix+SanitizeSegment(asin)+"-")
	if err != nil {
		return "", fmt.Errorf("liberate: work dir: %w", err)
	}
	return dir, nil
}

// sweepStaleWorkDirs removes scratch directories abandoned by runs that died
// without their deferred cleanup — an OOM kill, a hard eviction, a SIGKILL.
//
// Nothing else collects them. Each one holds up to ~1.5 GB (the AAXC, the M4B and
// a cover), and on a scratch PVC shared by the drain fleet they accumulate across
// incidents until the volume fills — at which point every subsequent liberation
// fails at download with a disk-full error that classifies as a generic retryable
// failure. sink.go's Commit comment already promised this sweep existed.
//
// Age, not ownership, is the criterion, because the pod that owns a directory
// leaves no marker a different pod could read. staleWorkDirAge is generous enough
// that a live run is never in range, and the newest mtime is taken across the
// directory's ENTRIES too, so a long download (whose dir mtime stopped moving
// when the .aaxc was created, but whose file mtime has not) is not mistaken for
// abandoned scratch.
//
// Best-effort by design: this is disk hygiene, never a reason to fail a book.
func (s *Service) sweepStaleWorkDirs(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleWorkDirAge)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), workDirPrefix) {
			continue
		}
		full := filepath.Join(base, e.Name())
		touched, ok := newestMTime(full)
		if !ok || !touched.Before(cutoff) {
			continue
		}
		if rerr := os.RemoveAll(full); rerr != nil {
			s.log().Warn("could not remove stale liberation work dir", "dir", full, "err", rerr)
			continue
		}
		s.log().Info("removed stale liberation work dir", "dir", full,
			"idle", time.Since(touched).Round(time.Minute).String())
	}
}

// newestMTime reports the most recent modification time of dir or anything
// directly inside it.
func newestMTime(dir string) (time.Time, bool) {
	info, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, false
	}
	newest := info.ModTime()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return newest, true
	}
	for _, e := range entries {
		ei, ierr := e.Info()
		if ierr != nil {
			// An entry we cannot stat might be live; refuse to judge the dir.
			return time.Time{}, false
		}
		if ei.ModTime().After(newest) {
			newest = ei.ModTime()
		}
	}
	return newest, true
}

// resolvePathCollision returns the path this book should commit to, given that
// another library row may already own the rendered one.
//
// The default template has no unique discriminator, so two items with the same
// author and sanitised title (a re-recording, abridged vs unabridged, a
// plus-catalog duplicate) render the SAME path — and Commit is documented to
// overwrite. Left alone that silently replaces book A's audio with book B's while
// A's row still says liberated at A's size; the next run of A then sees a size
// mismatch, re-liberates, and clobbers B, and the two rows flip-flop over one
// file indefinitely.
//
// A claimed path is retried once with the ASIN appended (DisambiguatePath). If
// that is claimed too, the run FAILS rather than overwriting: losing one book to
// a visible, retryable error is strictly better than losing a different book's
// audio silently.
func (s *Service) resolvePathCollision(ctx context.Context, owner, asin, relPath string) (string, error) {
	claimOwner, claimASIN, found, err := s.Store.PathClaimant(ctx, relPath, owner, asin)
	if err != nil {
		return "", err
	}
	if !found {
		return relPath, nil
	}
	alt := DisambiguatePath(relPath, asin)
	if alt == relPath {
		return "", fmt.Errorf("%w: %q is already the liberated file of %s/%s", ErrPathCollision, relPath, claimOwner, claimASIN)
	}
	_, altASIN, altFound, err := s.Store.PathClaimant(ctx, alt, owner, asin)
	if err != nil {
		return "", err
	}
	if altFound {
		return "", fmt.Errorf("%w: %q and %q are both taken (%s, %s)", ErrPathCollision, relPath, alt, claimASIN, altASIN)
	}
	s.log().Warn("liberation path collision; committing to a disambiguated path",
		"owner", owner, "asin", asin, "collidesWith", claimASIN, "path", alt)
	return alt, nil
}

// localFileSize returns the size of a local file, or 0 when it cannot be read.
func localFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// recordCtx returns a context for RECORDING an outcome, detached from the job's
// cancellation.
//
// The outcome most worth recording is the one caused by the job context being
// cancelled — a deploy, an eviction, a KEDA drain pod's termination. Under the
// job's own context every one of those writes is a no-op, which is how a row ends
// up stranded at 'downloading' with a 'pending' attempt row forever: the UI shows
// a phantom in-flight download and POST /liberate answers 409 indefinitely.
// The timeout keeps a detached write from outliving the shutdown it is racing.
func recordCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
}

// wasInterrupted reports whether a failure is the job being killed rather than
// anything about the title. Checks the context itself as well as the error,
// because a cancelled HTTP or pgx call does not always wrap context.Canceled.
func wasInterrupted(ctx context.Context, cause error) bool {
	return ctx.Err() != nil ||
		errors.Is(cause, context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded)
}

// recordFailure writes a failed outcome to the item row and returns the status
// and reason actually recorded.
//
// An INTERRUPTION is recorded as a plain retryable failure that does NOT count
// against the give-up budget (Store.MarkInterrupted): the classification the
// caller computed describes the title, and a job killed mid-download has said
// nothing about the title at all.
func (s *Service) recordFailure(ctx context.Context, owner, asin, status string, cause error, contentFormat string) (string, string) {
	rctx, cancel := recordCtx(ctx)
	defer cancel()

	if wasInterrupted(ctx, cause) {
		reason := "interrupted: " + cause.Error()
		if err := s.Store.MarkInterrupted(rctx, owner, asin, reason); err != nil {
			s.log().Warn("could not record an interrupted liberation", "owner", owner, "asin", asin, "err", err)
		}
		return StatusFailed, reason
	}
	reason := cause.Error()
	if err := s.Store.MarkFailed(rctx, owner, asin, status, reason, contentFormat); err != nil {
		s.log().Warn("could not record a failed liberation", "owner", owner, "asin", asin, "err", err)
	}
	return status, reason
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
