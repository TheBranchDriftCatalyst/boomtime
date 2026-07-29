// apply_key_outcome_ginkgo_test.go — ginkgo mirror of apply_key_outcome_test.go (gaka-0vp).
// 1:1 case map (3 stdlib TestXxx — one per canonical outcome):
//   TestApplyKeyOutcomeSuccessPersistsTypedToken           → applyKeyOutcome > "completed + no 401 + typed token → persists new blob and status='valid'"
//   TestApplyKeyOutcomeSaw401DoesNotPersistAndMarksInvalid → applyKeyOutcome > "failed + saw 401 → keeps prior blob and flips status to 'invalid'"
//   TestApplyKeyOutcomeNetworkFailureLeavesRowUntouched    → applyKeyOutcome > "failed + no 401 → row untouched (blob + status + checked_at)"
//
// Ginkgo-native openImportOutcomeDBGinkgo, seedUserWithKeyGinkgo,
// seedUserNoKeyGinkgo, and withEncryptionKeyGinkgo helpers mirror the
// stdlib helpers — they use Skip/DeferCleanup instead of *testing.T. The
// small pure helpers (dbNameFromDSN, swapDBName, etc.) are shared with
// the stdlib file.
package importer

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

func openImportOutcomeDBGinkgo() *db.DB {
	base := applyKeyOutcomeDSN()
	url := swapDBName(base, dbNameFromDSN(base)+applyKeyOutcomeDBSfx)

	ctx := context.Background()

	// ensure the dedicated DB exists (idempotent CREATE DATABASE via maint).
	maint := swapDBName(url, "postgres")
	pool, err := pgxpool.New(ctx, maint)
	if err != nil {
		Skip("import-outcome DB (maint connect): " + err.Error())
	}
	name := dbNameFromDSN(url)
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+quoteIdentLocal(name)); err != nil && !isDupDatabaseErr(err) {
		pool.Close()
		Skip("import-outcome DB (create): " + err.Error())
	}
	pool.Close()

	if err := db.MigrateURL(ctx, url); err != nil {
		Skip("import-outcome DB (migrate): " + err.Error())
	}
	database, err := db.New(ctx, url)
	if err != nil {
		Skip("import-outcome DB (connect): " + err.Error())
	}
	DeferCleanup(database.Close)
	return database
}

// silentLoggerGinkgo returns a slog.Logger that drops every record.
func silentLoggerGinkgo() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// seedUserWithKeyGinkgo mirrors seedUserWithKey — inserts a users row,
// encrypts+stores a key, and returns the initial ciphertext. Cleans up on
// spec end via DeferCleanup.
func seedUserWithKeyGinkgo(database *db.DB, username, plaintext string, initialStatus db.WakatimeKeyStatus) []byte {
	ctx := context.Background()
	hash, salt, err := auth.HashPassword("pw-" + username)
	Expect(err).NotTo(HaveOccurred())
	_, err = database.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt, ArgonVersion: auth.ArgonVersionCurrent})
	Expect(err).NotTo(HaveOccurred(), "insert user %s", username)
	DeferCleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM users WHERE username=$1`, username)
	})
	if plaintext == "" {
		return nil
	}
	ct, err := auth.Encrypt([]byte(plaintext))
	Expect(err).NotTo(HaveOccurred(), "encrypt seed key")
	Expect(database.SetEncryptedWakatimeKey(ctx, username, ct, initialStatus)).To(Succeed())
	return ct
}

// seedUserNoKeyGinkgo mirrors seedUserNoKey.
func seedUserNoKeyGinkgo(database *db.DB, username string) {
	ctx := context.Background()
	hash, salt, err := auth.HashPassword("pw-" + username)
	Expect(err).NotTo(HaveOccurred())
	_, err = database.InsertUser(ctx, db.StoredUser{Username: username, HashedPassword: hash, SaltUsed: salt, ArgonVersion: auth.ArgonVersionCurrent})
	Expect(err).NotTo(HaveOccurred(), "insert user %s", username)
	DeferCleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM users WHERE username=$1`, username)
	})
}

// withEncryptionKeyGinkgo installs a deterministic BOOM_ENCRYPTION_KEY for
// the spec duration, mirroring withEncryptionKey but using os.Setenv +
// DeferCleanup instead of t.Setenv/t.Cleanup.
func withEncryptionKeyGinkgo() {
	const key = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	prev, hadPrev := os.LookupEnv("BOOM_ENCRYPTION_KEY")
	os.Setenv("BOOM_ENCRYPTION_KEY", key)
	auth.ResetForTest()
	Expect(auth.LoadKeyFromEnv()).To(Succeed())
	DeferCleanup(func() {
		if hadPrev {
			os.Setenv("BOOM_ENCRYPTION_KEY", prev)
		} else {
			os.Unsetenv("BOOM_ENCRYPTION_KEY")
		}
		auth.ResetForTest()
	})
}

var _ = Describe("Worker.applyKeyOutcome (gaka-6jm.8, gaka-6jm.10)", func() {
	It("completed + no 401 + typed token → persists new blob and status='valid' (save-on-success)", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()

		user := fmt.Sprintf("okp_success_gk_%d", time.Now().UnixNano())
		seedUserNoKeyGinkgo(database, user)

		w := &Worker{db: database, logger: silentLoggerGinkgo(), hub: NewHub()}
		item := QueueItem{
			Requester:  user,
			TypedToken: "waka_success_plaintext",
		}
		w.applyKeyOutcome(item, db.JobStateCompleted, false)

		info, err := database.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue(), "expected save-on-success to persist a key")

		pt, err := auth.Decrypt(info.Blob)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(pt)).To(Equal(item.TypedToken))

		Expect(info.Status).NotTo(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusValid)))
	})

	It("failed + saw 401 → keeps prior blob and flips status to 'invalid' (typed token NEVER persists on 401)", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()

		user := fmt.Sprintf("okp_401_gk_%d", time.Now().UnixNano())
		priorCT := seedUserWithKeyGinkgo(database, user, "waka_previously_saved", db.WakatimeKeyStatusValid)

		w := &Worker{db: database, logger: silentLoggerGinkgo(), hub: NewHub()}
		item := QueueItem{
			Requester:  user,
			TypedToken: "waka_bad_typed_key", // MUST NOT be persisted
		}
		w.applyKeyOutcome(item, db.JobStateFailed, true)

		info, err := database.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue(), "prior key should remain saved after 401 (we only flip status)")
		Expect(base64.StdEncoding.EncodeToString(info.Blob)).To(Equal(base64.StdEncoding.EncodeToString(priorCT)),
			"prior ciphertext was overwritten on 401 — save-on-success violated")
		Expect(info.Status).NotTo(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusInvalid)))
	})

	It("failed + no 401 (network / rate-limit) → row untouched (blob + status + checked_at all preserved)", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()

		user := fmt.Sprintf("okp_netfail_gk_%d", time.Now().UnixNano())
		priorCT := seedUserWithKeyGinkgo(database, user, "waka_untouched", db.WakatimeKeyStatusValid)

		// Capture status + checked_at BEFORE to compare exactly.
		before, err := database.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{db: database, logger: silentLoggerGinkgo(), hub: NewHub()}
		item := QueueItem{
			Requester:  user,
			TypedToken: "waka_typed_but_run_died",
		}
		w.applyKeyOutcome(item, db.JobStateFailed, false)

		after, err := database.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(base64.StdEncoding.EncodeToString(after.Blob)).To(Equal(base64.StdEncoding.EncodeToString(priorCT)),
			"blob changed on network-failure outcome")
		Expect(ptrStrEq(before.Status, after.Status)).To(BeTrue(),
			"status changed on network-failure outcome: before=%v after=%v", before.Status, after.Status)
		Expect(ptrTimeEq(before.CheckedAt, after.CheckedAt)).To(BeTrue(),
			"checked_at changed on network-failure outcome: before=%v after=%v", before.CheckedAt, after.CheckedAt)
	})
})
