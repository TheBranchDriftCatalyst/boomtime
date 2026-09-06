// robustness_integration_test.go — DB-backed coverage of the four ways a
// liberation could quietly do the wrong thing (audit 2026-09-06):
//
//  1. two library items rendering the SAME path, where Commit overwrites and
//     the first book's audio is destroyed while both rows claim liberated;
//  2. two runs for the same ASIN sharing one scratch directory and clobbering
//     each other's half-downloaded AAXC;
//  3. a run killed by its context leaving the row stranded mid-flight forever,
//     because the failure was recorded under the cancelled context;
//  4. a post-commit Stat that does not answer recording audio_bytes=0, which
//     permanently disables the size-mismatch half of the idempotency contract.
//
// None of these are reachable from a stubbed store: each one is a property of
// the interaction between real SQL, a real filesystem and the orchestrator.
// Reuses the isolated-database harness in service_integration_test.go.
package liberate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
)

// twinASIN is a second Audible item whose author, series and title are identical
// to testASIN's — a re-recorded edition, in library terms. The default template
// has no discriminator, so both render the same path.
const twinASIN = "B0TWIN0001"

// seedTwin inserts that second item.
func seedTwin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	const raw = `{"asin":"B0TWIN0001","title":"The Gate of the Feral Gods","authors":[{"name":"Matt Dinniman"}],
	              "narrators":[{"name":"Jeff Hays"}],"series":[{"title":"Dungeon Crawler Carl","sequence":"4"}],
	              "release_date":"2023-01-01"}`
	_, err := pool.Exec(ctx, `
		INSERT INTO public.reading_items (owner, source, external_id, title, authors, raw_meta)
		VALUES ($1, 'audible', $2, 'The Gate of the Feral Gods', 'Matt Dinniman', $3::jsonb)`,
		testOwner, twinASIN, raw)
	if err != nil {
		t.Fatalf("seed twin: %v", err)
	}
}

// asinDecryptor stands in for the remux, writing content that IDENTIFIES the book
// it came from — which is what lets a test prove whose audio ended up on disk.
type asinDecryptor struct{}

func (asinDecryptor) Available(context.Context) error { return nil }
func (asinDecryptor) Decrypt(ctx context.Context, req liberate.DecryptRequest) error {
	asin := strings.TrimSuffix(filepath.Base(req.DstPath), ".m4b")
	return os.WriteFile(req.DstPath, []byte("M4B-"+asin), 0o644)
}

func rowFor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, asin string) (status, path string, bytes int64, errMsg string) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(liberation_status,''), COALESCE(audio_path,''),
		       COALESCE(audio_bytes,0), COALESCE(liberation_error,'')
		FROM public.reading_items WHERE owner=$1 AND external_id=$2`, testOwner, asin).
		Scan(&status, &path, &bytes, &errMsg)
	if err != nil {
		t.Fatalf("read row %s: %v", asin, err)
	}
	return
}

// (1) Two books that render the same path must end up as two files. Before the
// fix the second Commit overwrote the first book's M4B — the user's backup of
// book A silently BECAME book B, while A's row still said liberated at A's size.
func TestLiberateDoesNotOverwriteAnotherBooksFile(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	seedTwin(t, ctx, pool)
	libRoot := t.TempDir()

	svc := newService(t, pool, libRoot)
	svc.Decryptor = asinDecryptor{}

	first, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
	if err != nil {
		t.Fatalf("first book: %v", err)
	}
	second, err := svc.LiberateBook(ctx, testOwner, twinASIN, liberate.Options{})
	if err != nil {
		t.Fatalf("second book: %v", err)
	}

	if first.RelPath == second.RelPath {
		t.Fatalf("both books committed to %q — the second overwrote the first", first.RelPath)
	}
	// The decisive assertion: book A's file still contains book A.
	got, rerr := os.ReadFile(filepath.Join(libRoot, first.RelPath))
	if rerr != nil {
		t.Fatalf("first book's file is gone: %v", rerr)
	}
	if string(got) != "M4B-"+testASIN {
		t.Errorf("%q contains %q — the second liberation destroyed the first book's audio", first.RelPath, got)
	}
	if got2, rerr := os.ReadFile(filepath.Join(libRoot, second.RelPath)); rerr != nil || string(got2) != "M4B-"+twinASIN {
		t.Errorf("second book's file = %q, err %v", got2, rerr)
	}
	// The disambiguated path must carry the ASIN, and both rows must point at
	// their own file at their own size.
	if !strings.Contains(second.RelPath, twinASIN) {
		t.Errorf("disambiguated path %q does not identify the book", second.RelPath)
	}
	for _, tc := range []struct{ asin, want string }{{testASIN, first.RelPath}, {twinASIN, second.RelPath}} {
		status, path, bytes, _ := rowFor(t, ctx, pool, tc.asin)
		if status != liberate.StatusLiberated || path != tc.want {
			t.Errorf("%s: status=%q path=%q, want liberated at %q", tc.asin, status, path, tc.want)
		}
		if bytes != int64(len("M4B-"+tc.asin)) {
			t.Errorf("%s: audio_bytes = %d, want its own file's size", tc.asin, bytes)
		}
	}
}

// (2) Two runs for the same ASIN are reachable (a double-click enqueues two jobs
// before liberation_status is set, and a sweep re-enqueues still-queued ASINs)
// and are claimed together under the cap-2 limiter. With one scratch directory
// per ASIN they shared srcPath, so the second run's O_TRUNC download zeroed the
// first's file mid-write and the first remuxed a partially-zeroed AAXC into a
// corrupt M4B marked liberated.
func TestConcurrentRunsForOneASINDoNotShareScratch(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	sharedWork := t.TempDir()
	libRoot := t.TempDir()

	// Both fetchers park until BOTH have written, so the runs genuinely overlap
	// rather than accidentally serialising.
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})

	newRun := func(payload string) *liberate.Service {
		svc := newService(t, pool, libRoot)
		svc.WorkDir = sharedWork
		svc.Fetcher = func(fctx context.Context, _ *amazon.DeviceCredential, _, dest string, _ liberate.Progress) (int64, error) {
			if err := os.WriteFile(dest, []byte(payload), 0o644); err != nil {
				return 0, err
			}
			arrived <- struct{}{}
			<-release
			return int64(len(payload)), nil
		}
		// The decryptor is the detector: it reads the file this run downloaded
		// and refuses if another run's bytes are in it.
		svc.Decryptor = checkingDecryptor{want: payload}
		return svc
	}

	// Built on the test goroutine: newService reports setup failures with Fatalf.
	runs := []*liberate.Service{newRun("AAXC-RUN-A"), newRun("AAXC-RUN-B")}

	var wg sync.WaitGroup
	errs := make([]error, len(runs))
	for i, svc := range runs {
		wg.Add(1)
		go func(i int, svc *liberate.Service) {
			defer wg.Done()
			_, errs[i] = svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{Force: true})
		}(i, svc)
	}
	<-arrived
	<-arrived
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("run %d failed: %v", i, err)
		}
	}
}

// checkingDecryptor fails the run when the downloaded file is not the one this
// run downloaded.
type checkingDecryptor struct{ want string }

func (d checkingDecryptor) Available(context.Context) error { return nil }
func (d checkingDecryptor) Decrypt(ctx context.Context, req liberate.DecryptRequest) error {
	got, err := os.ReadFile(req.SrcPath)
	if err != nil {
		return errors.New("source file vanished before the remux: " + err.Error())
	}
	if string(got) != d.want {
		return errors.New("source file was clobbered by a concurrent run for the same ASIN: got " +
			string(got) + ", want " + d.want)
	}
	return os.WriteFile(req.DstPath, []byte("M4B-OUTPUT-BYTES"), 0o644)
}

// (3) A run killed by its own context — a deploy, an eviction, a KEDA drain pod
// terminating — must still record its outcome. Recorded under the job context the
// UPDATE never ran, so the row stayed at 'downloading' forever: a phantom
// in-flight book in the UI and a permanent 409 on every retry.
func TestInterruptedRunIsRecordedNotStranded(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	svc := newService(t, pool, t.TempDir())

	runCtx, cancel := context.WithCancel(ctx)
	svc.Fetcher = func(fctx context.Context, _ *amazon.DeviceCredential, _, _ string, _ liberate.Progress) (int64, error) {
		cancel() // the pod is going away mid-download
		return 0, fctx.Err()
	}
	if _, err := svc.LiberateBook(runCtx, testOwner, testASIN, liberate.Options{}); err == nil {
		t.Fatal("want an error from the cancelled download")
	}

	status, _, _, errMsg := rowFor(t, ctx, pool, testASIN)
	if status != liberate.StatusFailed {
		t.Errorf("liberation_status = %q, want %q — an interrupted run left the row stranded mid-flight",
			status, liberate.StatusFailed)
	}
	if !strings.HasPrefix(errMsg, "interrupted") {
		t.Errorf("liberation_error = %q, want it to name the interruption", errMsg)
	}
	// An interruption says nothing about the TITLE, so it must not spend the
	// give-up budget: three deploys must not retire a book.
	if n := attemptsOf(t, ctx, pool, testASIN); n != 0 {
		t.Errorf("liberation_attempts = %d, want 0 — an interruption counted against the give-up rule", n)
	}
	// The attempt row must be closed too, or the history shows a pending attempt
	// for a run that ended.
	var finished *string
	if err := pool.QueryRow(ctx,
		`SELECT to_char(finished_at,'YYYY-MM-DD') FROM public.book_liberation_attempts WHERE owner=$1 AND asin=$2`,
		testOwner, testASIN).Scan(&finished); err != nil {
		t.Fatalf("attempt row: %v", err)
	}
	if finished == nil {
		t.Error("attempt row left pending after an interrupted run")
	}
}

// errStatSink fails the FIRST Stat and then behaves normally — an NFS blip at
// exactly the wrong moment.
type errStatSink struct {
	liberate.Sink
	mu    sync.Mutex
	calls int
}

func (s *errStatSink) Stat(ctx context.Context, relPath string) (int64, bool, error) {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		return 0, false, errors.New("nfs: operation timed out")
	}
	return s.Sink.Stat(ctx, relPath)
}

// (4) When the post-commit Stat cannot answer, audio_bytes must NOT be recorded
// as 0. A zero there is silently permanent: alreadyLiberated's size check is
// guarded on `AudioBytes > 0`, so a later truncation reads as "already liberated"
// on every sweep forever — precisely the state the contract exists to catch.
func TestPostCommitStatFailureStillRecordsTheSize(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	libRoot := t.TempDir()

	svc := newService(t, pool, libRoot)
	svc.Sink = &errStatSink{Sink: svc.Sink}

	res, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
	if err != nil {
		t.Fatalf("LiberateBook: %v", err)
	}
	want := int64(len("M4B-OUTPUT-BYTES"))
	if res.Bytes != want {
		t.Errorf("result bytes = %d, want %d", res.Bytes, want)
	}
	if _, _, bytes, _ := rowFor(t, ctx, pool, testASIN); bytes != want {
		t.Errorf("audio_bytes = %d, want %d — a 0 here disables the size-mismatch check permanently", bytes, want)
	}

	// The property that actually matters: the contract still catches a
	// truncation afterwards.
	if err := os.WriteFile(filepath.Join(libRoot, res.RelPath), []byte("trunc"), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	again, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if again.Skipped {
		t.Error("a truncated file was treated as already liberated — the size-mismatch check is dead for this row")
	}
}

// Scratch left by a run that was SIGKILLed has no other collector: nothing else
// in the tree removes liberate-* directories, and each holds up to ~1.5 GB.
func TestStaleWorkDirsAreSwept(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	work := t.TempDir()

	stale := filepath.Join(work, "liberate-B0DEADBEEF-123456")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	leftover := filepath.Join(stale, "B0DEADBEEF.aaxc")
	if err := os.WriteFile(leftover, []byte("half a download"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := time.Now().Add(-72 * time.Hour)
	for _, p := range []string{leftover, stale} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	// A fresh directory (another pod's live run) and an unrelated one must both
	// survive.
	live := filepath.Join(work, "liberate-B0LIVE0001-987654")
	unrelated := filepath.Join(work, "someone-elses-scratch")
	for _, p := range []string{live, unrelated} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	svc := newService(t, pool, t.TempDir())
	svc.WorkDir = work
	if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err != nil {
		t.Fatalf("LiberateBook: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale work dir survived (err=%v) — abandoned scratch accumulates until the volume fills", err)
	}
	for _, p := range []string{live, unrelated} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%q was removed: %v", p, err)
		}
	}
}
