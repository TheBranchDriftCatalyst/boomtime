// service_integration_test.go — DB-backed coverage of the liberation
// orchestrator (boom-w20s.13).
//
// This is the layer the unit tests cannot reach: the STATUS MACHINE and the
// IDEMPOTENCY CONTRACT only exist in the interaction between the service, real
// SQL, and a real filesystem sink. Everything is genuine except the two network
// steps (license + download), which target hard-coded Amazon hosts and are
// injected via the Licenser/Fetcher seams.
//
// It provisions its own isolated `boomtime_liberate_test` database (DROP +
// CREATE each run) via the books-only migration set, following the repo's
// LAN-IP test-DB convention, and Skips when Postgres is unreachable unless
// BOOM_REQUIRE_DB=1.
package liberate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	booksdb "github.com/TheBranchDriftCatalyst/boomtime/internal/books/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	shareddb "github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

const liberateDBName = "boomtime_liberate_test"

const testOwner = "dj"
const testASIN = "B09GCYRZRQ"

func baseTestURL() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
}

func swapDBName(dsn, name string) string {
	q := ""
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		q, dsn = dsn[i:], dsn[:i]
	}
	if slash := strings.LastIndexByte(dsn, '/'); slash >= 0 {
		return dsn[:slash+1] + name + q
	}
	return dsn + "/" + name + q
}

// provisionDB gives each run a pristine schema.
func provisionDB(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	base := baseTestURL()
	maint, err := pgxpool.New(ctx, swapDBName(base, "postgres"))
	if err == nil {
		err = maint.Ping(ctx)
	}
	if err != nil {
		if maint != nil {
			maint.Close()
		}
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("BOOM_REQUIRE_DB=1 but Postgres unreachable: %v", err)
		}
		t.Skipf("test Postgres unreachable (%v) — set BOOM_TEST_DATABASE_URL to the LAN-IP test DB", err)
	}
	defer maint.Close()

	_, _ = maint.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, liberateDBName)
	if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS "`+liberateDBName+`"`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := maint.Exec(ctx, `CREATE DATABASE "`+liberateDBName+`"`); err != nil {
		t.Fatalf("create: %v", err)
	}
	dsn := swapDBName(base, liberateDBName)
	if err := shareddb.MigrateURLFS(ctx, dsn, booksdb.MigrationsFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedItem inserts the owner and one Audible library row.
func seedItem(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO public.users (username) VALUES ($1) ON CONFLICT DO NOTHING`, testOwner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	const raw = `{"asin":"B09GCYRZRQ","title":"The Gate of the Feral Gods","authors":[{"name":"Matt Dinniman"}],
	              "narrators":[{"name":"Jeff Hays"}],"series":[{"title":"Dungeon Crawler Carl","sequence":"4"}],
	              "release_date":"2021-09-14"}`
	_, err := pool.Exec(ctx, `
		INSERT INTO public.reading_items (owner, source, external_id, title, authors, raw_meta)
		VALUES ($1, 'audible', $2, 'The Gate of the Feral Gods', 'Matt Dinniman', $3::jsonb)`,
		testOwner, testASIN, raw)
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
}

// --- stubs -----------------------------------------------------------------

type stubCreds struct{}

func (stubCreds) Load(ctx context.Context, username string) (*amazon.DeviceCredential, error) {
	return &amazon.DeviceCredential{
		AdpToken: "tok", DeviceSerial: "SER", CustomerID: "CUST",
		Marketplace: amazon.MarketplaceUS,
	}, nil
}

type stubDecryptor struct{ err error }

func (s stubDecryptor) Available(context.Context) error { return nil }
func (s stubDecryptor) Decrypt(ctx context.Context, req liberate.DecryptRequest) error {
	if s.err != nil {
		return s.err
	}
	// Stand in for the remux by producing a plausible output file.
	return os.WriteFile(req.DstPath, []byte("M4B-OUTPUT-BYTES"), 0o644)
}

// grantedLicense returns a license carrying a voucher that unseals to a valid
// key under the real derivation, so the service's voucher step is EXERCISED
// rather than stubbed — only the transport is faked.
func grantedLicense(contentFormat string) func(context.Context, *amazon.DeviceCredential, string) (*liberate.LicenseResponse, []byte, error) {
	return func(ctx context.Context, cred *amazon.DeviceCredential, asin string) (*liberate.LicenseResponse, []byte, error) {
		lr := &liberate.LicenseResponse{}
		lr.ContentLicense.StatusCode = "Granted"
		lr.ContentLicense.LicenseResponse = sealForTest(cred, asin)
		lr.ContentLicense.ContentMetadata.ContentReference.ContentFormat = contentFormat
		lr.ContentLicense.ContentMetadata.ContentURL.OfflineURL = "https://cds.example/x.aaxc"
		lr.ContentLicense.ContentMetadata.ChapterInfo = liberate.ChapterInfo{
			RuntimeLengthMs: 20000,
			Chapters: []liberate.Chapter{
				{Title: "One", StartOffsetMs: 0, LengthMs: 10000},
				{Title: "Two", StartOffsetMs: 10000, LengthMs: 10000},
			},
		}
		return lr, nil, nil
	}
}

// okFetch writes n bytes to the destination, standing in for the download.
func okFetch(payload string) func(context.Context, *amazon.DeviceCredential, string, string, liberate.Progress) (int64, error) {
	return func(ctx context.Context, cred *amazon.DeviceCredential, rawURL, dest string, p liberate.Progress) (int64, error) {
		if err := os.WriteFile(dest, []byte(payload), 0o644); err != nil {
			return 0, err
		}
		if p != nil {
			p(int64(len(payload)), int64(len(payload)))
		}
		return int64(len(payload)), nil
	}
}

func newService(t *testing.T, pool *pgxpool.Pool, libRoot string) *liberate.Service {
	t.Helper()
	sink, err := liberate.NewFSSink(libRoot)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	return &liberate.Service{
		Store:     liberate.NewStore(pool),
		Amazon:    stubCreds{},
		Sink:      sink,
		Decryptor: stubDecryptor{},
		WorkDir:   t.TempDir(),
		Licenser:  grantedLicense("AAX_44_128"),
		Fetcher:   okFetch("AAXC-PAYLOAD"),
	}
}

func itemRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (status, path string, bytes int64, contentFormat string) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(liberation_status,''), COALESCE(audio_path,''),
		       COALESCE(audio_bytes,0), COALESCE(content_format,'')
		FROM public.reading_items WHERE owner=$1 AND external_id=$2`, testOwner, testASIN).
		Scan(&status, &path, &bytes, &contentFormat)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	return
}

// --- tests -----------------------------------------------------------------

func TestLiberateBookHappyPath(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	libRoot := t.TempDir()
	svc := newService(t, pool, libRoot)

	res, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
	if err != nil {
		t.Fatalf("LiberateBook: %v", err)
	}
	if res.Status != liberate.StatusLiberated || res.Skipped {
		t.Fatalf("result = %+v, want liberated and not skipped", res)
	}

	// The file lands at the TEMPLATED path, from metadata read out of raw_meta.
	want := "Matt Dinniman/Dungeon Crawler Carl/The Gate of the Feral Gods/The Gate of the Feral Gods.m4b"
	if res.RelPath != want {
		t.Errorf("relPath = %q, want %q", res.RelPath, want)
	}
	if _, statErr := os.Stat(filepath.Join(libRoot, want)); statErr != nil {
		t.Errorf("file not in library: %v", statErr)
	}

	status, path, bytes, cf := itemRow(t, ctx, pool)
	if status != liberate.StatusLiberated {
		t.Errorf("row status = %q", status)
	}
	if path != want {
		t.Errorf("row audio_path = %q", path)
	}
	if bytes != int64(len("M4B-OUTPUT-BYTES")) {
		t.Errorf("row audio_bytes = %d, want the committed size", bytes)
	}
	// content_format must be persisted even on success — it is the input to the
	// ffmpeg-vs-native decision.
	if cf != "AAX_44_128" {
		t.Errorf("row content_format = %q", cf)
	}

	// The attempt row must be closed, not left dangling.
	var attemptStatus string
	var finished *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, finished_at FROM public.book_liberation_attempts WHERE owner=$1 AND asin=$2`,
		testOwner, testASIN).Scan(&attemptStatus, &finished); err != nil {
		t.Fatalf("attempt row: %v", err)
	}
	if attemptStatus != liberate.StatusLiberated || finished == nil {
		t.Errorf("attempt = %q finished=%v, want a closed liberated attempt", attemptStatus, finished)
	}
}

// The idempotency contract, which is the whole reason a sweep is safe to re-run.
func TestLiberateBookIdempotency(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	libRoot := t.TempDir()
	svc := newService(t, pool, libRoot)

	first, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	full := filepath.Join(libRoot, first.RelPath)

	t.Run("second run skips", func(t *testing.T) {
		res, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
		if err != nil {
			t.Fatalf("second run: %v", err)
		}
		if !res.Skipped {
			t.Error("second run re-liberated an unchanged book")
		}
	})

	t.Run("force overrides the skip", func(t *testing.T) {
		res, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{Force: true})
		if err != nil {
			t.Fatalf("forced run: %v", err)
		}
		if res.Skipped {
			t.Error("Force did not override the idempotency skip")
		}
	})

	t.Run("deleted file re-liberates", func(t *testing.T) {
		if err := os.Remove(full); err != nil {
			t.Fatalf("remove: %v", err)
		}
		res, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
		if err != nil {
			t.Fatalf("run after delete: %v", err)
		}
		if res.Skipped {
			t.Error("a missing file was treated as still liberated")
		}
		if _, statErr := os.Stat(full); statErr != nil {
			t.Error("file was not restored")
		}
	})

	t.Run("truncated file re-liberates", func(t *testing.T) {
		// A size mismatch means the recorded download did not survive intact.
		if err := os.WriteFile(full, []byte("trunc"), 0o644); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		res, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{})
		if err != nil {
			t.Fatalf("run after truncate: %v", err)
		}
		if res.Skipped {
			t.Error("a size mismatch was treated as still liberated")
		}
	})
}

// A Denied license is TERMINAL. If it were recorded as a generic failure the
// sweep would retry it forever, which is how an account gets flagged.
func TestLiberateBookDeniedIsTerminal(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	svc := newService(t, pool, t.TempDir())
	// Wrapped exactly as the real RequestLicense wraps it, so the service's
	// errors.Is check is what is under test rather than a bare sentinel.
	svc.Licenser = func(context.Context, *amazon.DeviceCredential, string) (*liberate.LicenseResponse, []byte, error) {
		return nil, nil, fmt.Errorf("%w: not owned", liberate.ErrLicenseDenied)
	}

	if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err == nil {
		t.Fatal("want an error on Denied")
	}
	status, _, _, _ := itemRow(t, ctx, pool)
	if status != liberate.StatusDenied {
		t.Errorf("status = %q, want %q so the sweep stops retrying it", status, liberate.StatusDenied)
	}
	// ListUnliberated must EXCLUDE denied rows, or the sweep re-queues it anyway.
	pending, err := liberate.NewStore(pool).ListUnliberated(ctx, testOwner, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, a := range pending {
		if a == testASIN {
			t.Error("a denied title is still in the unliberated sweep set")
		}
	}
}

// A failed remux on a non-AAX_* format is attributed to the codec, because that
// count is what triggers the native-decoder epic.
func TestLiberateBookRemuxFailureClassification(t *testing.T) {
	ctx := context.Background()

	t.Run("non-AAX format is unsupported_codec", func(t *testing.T) {
		pool := provisionDB(t, ctx)
		seedItem(t, ctx, pool)
		svc := newService(t, pool, t.TempDir())
		svc.Licenser = grantedLicense("MPEG_44_128")
		svc.Decryptor = stubDecryptor{err: errors.New("ffmpeg: could not parse")}

		if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err == nil {
			t.Fatal("want an error")
		}
		status, _, _, cf := itemRow(t, ctx, pool)
		if status != liberate.StatusUnsupportedCodec {
			t.Errorf("status = %q, want unsupported_codec", status)
		}
		if cf != "MPEG_44_128" {
			t.Errorf("content_format = %q — an unsupported_codec row is only useful if it says WHICH codec", cf)
		}
	})

	t.Run("AAX format failure is a plain failure", func(t *testing.T) {
		pool := provisionDB(t, ctx)
		seedItem(t, ctx, pool)
		svc := newService(t, pool, t.TempDir())
		svc.Decryptor = stubDecryptor{err: errors.New("ffmpeg: disk full")}

		if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err == nil {
			t.Fatal("want an error")
		}
		status, _, _, _ := itemRow(t, ctx, pool)
		if status != liberate.StatusFailed {
			t.Errorf("status = %q, want failed", status)
		}
	})
}

// A failed download must leave nothing in the LIBRARY — a scanner must never
// see a partial book.
func TestLiberateBookDownloadFailureLeavesLibraryClean(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	libRoot := t.TempDir()
	svc := newService(t, pool, libRoot)
	svc.Fetcher = func(context.Context, *amazon.DeviceCredential, string, string, liberate.Progress) (int64, error) {
		return 0, liberate.ErrShortRead
	}

	if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err == nil {
		t.Fatal("want an error")
	}
	status, _, _, _ := itemRow(t, ctx, pool)
	if status != liberate.StatusFailed {
		t.Errorf("status = %q, want failed (retryable)", status)
	}
	entries, _ := os.ReadDir(libRoot)
	if len(entries) != 0 {
		t.Errorf("library is not empty after a failed download: %v", entries)
	}
}

func TestLiberateAllListsPendingOldestFirst(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	svc := newService(t, pool, t.TempDir())

	pending, err := svc.LiberateAll(ctx, testOwner, 0)
	if err != nil {
		t.Fatalf("LiberateAll: %v", err)
	}
	if len(pending) != 1 || pending[0] != testASIN {
		t.Fatalf("pending = %v, want [%s]", pending, testASIN)
	}

	// After liberation it drops out of the set.
	if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err != nil {
		t.Fatalf("liberate: %v", err)
	}
	pending, err = svc.LiberateAll(ctx, testOwner, 0)
	if err != nil {
		t.Fatalf("LiberateAll: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %v, want empty after liberation", pending)
	}
}

func TestStatusCounts(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	store := liberate.NewStore(pool)

	counts, err := store.StatusCounts(ctx, testOwner)
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}
	// A never-attempted row reports under the empty-string key.
	if counts[""] != 1 {
		t.Errorf("counts = %v, want one un-attempted row", counts)
	}

	svc := newService(t, pool, t.TempDir())
	if _, err := svc.LiberateBook(ctx, testOwner, testASIN, liberate.Options{}); err != nil {
		t.Fatalf("liberate: %v", err)
	}
	counts, _ = store.StatusCounts(ctx, testOwner)
	if counts[liberate.StatusLiberated] != 1 {
		t.Errorf("counts = %v, want one liberated", counts)
	}
}

func TestLoadItemMissing(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)

	if _, err := liberate.NewStore(pool).LoadItem(ctx, testOwner, "B0NOTOWNED"); !errors.Is(err, liberate.ErrItemNotFound) {
		t.Errorf("err = %v, want ErrItemNotFound", err)
	}
}
