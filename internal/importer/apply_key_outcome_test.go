// apply_key_outcome_test.go: verifies gaka-6jm.8 (save-on-success) and
// gaka-6jm.10 (key_status wiring) by driving Worker.applyKeyOutcome against
// a real Postgres row and asserting the (encrypted_wakatime_key,
// wakatime_key_status) tuple ends up in the expected state.
//
// Three canonical outcomes (each tested):
//
//	Completed + no 401  + typed token  → row set (blob!=nil, status='valid')
//	Failed    + saw 401                 → row status='invalid' (blob unchanged)
//	Failed    + no 401  (network etc.)  → row untouched
package importer

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

const applyKeyOutcomeDBSfx = "_import_outcome"

func applyKeyOutcomeDSN() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultDriftDSN // reuse the constant from drift_integration_test.go
}

func openImportOutcomeDB(t *testing.T) *db.DB {
	t.Helper()
	base := applyKeyOutcomeDSN()
	url := swapDBName(base, dbNameFromDSN(base)+applyKeyOutcomeDBSfx)

	ctx := context.Background()

	// ensure the dedicated DB exists (idempotent CREATE DATABASE via maint).
	maint := swapDBName(url, "postgres")
	pool, err := pgxpool.New(ctx, maint)
	if err != nil {
		t.Skipf("skipping import-outcome DB (maint connect): %v", err)
	}
	name := dbNameFromDSN(url)
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+quoteIdentLocal(name)); err != nil && !isDupDatabaseErr(err) {
		pool.Close()
		t.Skipf("skipping import-outcome DB (create): %v", err)
	}
	pool.Close()

	if err := db.MigrateURL(ctx, url); err != nil {
		t.Skipf("skipping import-outcome DB (migrate): %v", err)
	}
	database, err := db.New(ctx, url)
	if err != nil {
		t.Skipf("skipping import-outcome DB (connect): %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

// silentLogger is a slog.Logger that drops every record so noisy warn/debug
// lines from applyKeyOutcome don't clutter `go test -v`.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// seedUserWithKey inserts a users row, encrypts+stores a key, and returns the
// initial ciphertext for later comparison. Cleans up on test end.
func seedUserWithKey(t *testing.T, database *db.DB, username, plaintext string, initialStatus db.WakatimeKeyStatus) []byte {
	t.Helper()
	ctx := context.Background()
	hash, salt, err := auth.HashPassword("pw-" + username)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := database.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt, ArgonVersion: auth.ArgonVersionCurrent}); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM users WHERE username=$1`, username)
	})
	if plaintext == "" {
		return nil
	}
	ct, err := auth.Encrypt([]byte(plaintext))
	if err != nil {
		t.Fatalf("encrypt seed key: %v", err)
	}
	if err := database.SetEncryptedWakatimeKey(ctx, username, ct, initialStatus); err != nil {
		t.Fatalf("SetEncryptedWakatimeKey seed: %v", err)
	}
	return ct
}

// seedUserNoKey inserts a bare users row (no encrypted key). Cleaned up.
func seedUserNoKey(t *testing.T, database *db.DB, username string) {
	t.Helper()
	ctx := context.Background()
	hash, salt, err := auth.HashPassword("pw-" + username)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := database.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt, ArgonVersion: auth.ArgonVersionCurrent}); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM users WHERE username=$1`, username)
	})
}

// withEncryptionKey installs a deterministic BOOM_ENCRYPTION_KEY for the test
// duration so auth.Encrypt/Decrypt work. Kept package-local since crypto_test
// helpers live in the auth package.
func withEncryptionKey(t *testing.T) {
	t.Helper()
	t.Setenv("BOOM_ENCRYPTION_KEY", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	// force lazy loader to re-parse (crypto's sync.Once is reset via the
	// test-only hook, but that hook is in-package to auth; instead we call
	// LoadKeyFromEnv which is a no-op if already loaded, but since the test
	// process may be first-touch it works).
	auth.ResetForTest()
	if err := auth.LoadKeyFromEnv(); err != nil {
		t.Fatalf("load encryption key: %v", err)
	}
	t.Cleanup(auth.ResetForTest)
}

// TestApplyKeyOutcomeSuccessPersistsTypedToken: end-to-end save-on-success.
// A completed run with saw401=false and a fresh TypedToken must write a NEW
// ciphertext and set status='valid'.
func TestApplyKeyOutcomeSuccessPersistsTypedToken(t *testing.T) {
	database := openImportOutcomeDB(t)
	withEncryptionKey(t)

	user := fmt.Sprintf("okp_success_%d", time.Now().UnixNano())
	seedUserNoKey(t, database, user)

	w := &Worker{db: database, logger: silentLogger(), hub: NewHub()}
	item := QueueItem{
		Requester:  user,
		TypedToken: "waka_success_plaintext",
	}
	w.applyKeyOutcome(item, db.JobStateCompleted, false)

	info, err := database.GetWakatimeKeyInfo(context.Background(), user)
	if err != nil {
		t.Fatalf("read back key info: %v", err)
	}
	if !info.HasSavedKey {
		t.Fatalf("expected save-on-success to persist a key, got none")
	}
	pt, err := auth.Decrypt(info.Blob)
	if err != nil {
		t.Fatalf("decrypt persisted key: %v", err)
	}
	if string(pt) != item.TypedToken {
		t.Fatalf("persisted plaintext = %q, want %q", pt, item.TypedToken)
	}
	if info.Status == nil || *info.Status != string(db.WakatimeKeyStatusValid) {
		t.Fatalf("status = %v, want 'valid'", info.Status)
	}
}

// TestApplyKeyOutcomeSaw401DoesNotPersistAndMarksInvalid: a 401 during the
// run must NEVER save the typed token AND must flip the row's status to
// 'invalid' if a saved key already exists.
func TestApplyKeyOutcomeSaw401DoesNotPersistAndMarksInvalid(t *testing.T) {
	database := openImportOutcomeDB(t)
	withEncryptionKey(t)

	user := fmt.Sprintf("okp_401_%d", time.Now().UnixNano())
	priorCT := seedUserWithKey(t, database, user, "waka_previously_saved", db.WakatimeKeyStatusValid)

	w := &Worker{db: database, logger: silentLogger(), hub: NewHub()}
	item := QueueItem{
		Requester:  user,
		TypedToken: "waka_bad_typed_key", // MUST NOT be persisted
	}
	w.applyKeyOutcome(item, db.JobStateFailed, true)

	info, err := database.GetWakatimeKeyInfo(context.Background(), user)
	if err != nil {
		t.Fatalf("read back key info: %v", err)
	}
	// The prior key is untouched — same ciphertext bytes.
	if !info.HasSavedKey {
		t.Fatalf("prior key should remain saved after 401 (we only flip status)")
	}
	if base64.StdEncoding.EncodeToString(info.Blob) != base64.StdEncoding.EncodeToString(priorCT) {
		t.Fatalf("prior ciphertext was overwritten on 401 — save-on-success violated")
	}
	if info.Status == nil || *info.Status != string(db.WakatimeKeyStatusInvalid) {
		t.Fatalf("status = %v, want 'invalid'", info.Status)
	}
}

// TestApplyKeyOutcomeNetworkFailureLeavesRowUntouched: a non-401 failure
// (state=failed, saw401=false) must NOT touch the row at all. Simulates a
// transient network error or a rate-limit failure.
func TestApplyKeyOutcomeNetworkFailureLeavesRowUntouched(t *testing.T) {
	database := openImportOutcomeDB(t)
	withEncryptionKey(t)

	user := fmt.Sprintf("okp_netfail_%d", time.Now().UnixNano())
	priorCT := seedUserWithKey(t, database, user, "waka_untouched", db.WakatimeKeyStatusValid)

	// Capture status + checked_at BEFORE to compare exactly.
	before, err := database.GetWakatimeKeyInfo(context.Background(), user)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	w := &Worker{db: database, logger: silentLogger(), hub: NewHub()}
	item := QueueItem{
		Requester:  user,
		TypedToken: "waka_typed_but_run_died",
	}
	w.applyKeyOutcome(item, db.JobStateFailed, false)

	after, err := database.GetWakatimeKeyInfo(context.Background(), user)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if base64.StdEncoding.EncodeToString(after.Blob) != base64.StdEncoding.EncodeToString(priorCT) {
		t.Fatalf("blob changed on network-failure outcome")
	}
	if !ptrStrEq(before.Status, after.Status) {
		t.Fatalf("status changed on network-failure outcome: before=%v after=%v", before.Status, after.Status)
	}
	if !ptrTimeEq(before.CheckedAt, after.CheckedAt) {
		t.Fatalf("checked_at changed on network-failure outcome: before=%v after=%v", before.CheckedAt, after.CheckedAt)
	}
}

func ptrStrEq(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func ptrTimeEq(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

// --- tiny DSN helpers local to this test file (avoid dragging in testutil,
// which would cycle since testutil imports importer). Same intent as the
// versions in internal/testutil but small enough to inline.

func dbNameFromDSN(dsn string) string {
	// postgres://user:pass@host:port/name?opts → return name.
	q := indexRuneOr(dsn, '?', len(dsn))
	slash := lastIndexRune(dsn[:q], '/')
	if slash < 0 {
		return ""
	}
	return dsn[slash+1 : q]
}

func swapDBName(dsn, name string) string {
	q := indexRuneOr(dsn, '?', len(dsn))
	slash := lastIndexRune(dsn[:q], '/')
	if slash < 0 {
		return dsn
	}
	return dsn[:slash+1] + name + dsn[q:]
}

func quoteIdentLocal(id string) string {
	// pg identifiers: wrap in double quotes; embedded quotes doubled.
	out := make([]byte, 0, len(id)+2)
	out = append(out, '"')
	for i := 0; i < len(id); i++ {
		if id[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, id[i])
	}
	out = append(out, '"')
	return string(out)
}

func isDupDatabaseErr(err error) bool {
	// pg SQLSTATE 42P04 = duplicate_database. Coarse string check keeps this
	// test file dependency-free — matches internal/testutil's isAlreadyExists.
	return err != nil && (contains(err.Error(), "already exists") || contains(err.Error(), "42P04"))
}

func indexRuneOr(s string, r rune, or int) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return or
}

func lastIndexRune(s string, r rune) int {
	for i := len(s) - 1; i >= 0; i-- {
		if rune(s[i]) == r {
			return i
		}
	}
	return -1
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
