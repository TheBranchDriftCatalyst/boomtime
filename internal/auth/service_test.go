// service_test.go — gaka-se2.4 SECURITY-CRITICAL integration coverage for
// the auth.CreateUser / CreateAPIToken / VerifyUserCredentials trio that
// both the CLI and HTTP register/login paths compose over.
//
// Uses the shared BOOM_TEST_DATABASE_URL Postgres and provisions the
// isolated `boomtime_test` DB on first use — mirrors the pattern in
// internal/testutil (which can't be imported here because it depends on
// internal/auth). Every spec Skips cleanly if Postgres is unreachable.
//
// Invariants pinned:
//
//   - CreateUser stores the ARGON2ID HASH, never the plaintext password
//     (verified by reading `hashed_password` back and asserting
//     bytes.Equal(hashed, plaintext-bytes) is FALSE + VerifyPassword
//     against the stored hash+salt succeeds).
//   - CreateUser refuses duplicate usernames with ErrUserExists.
//   - CreateAPIToken returns a non-empty raw token AND persists ONLY the
//     SHA-256 of it (raw bytes never land in the DB — GetUserByToken
//     resolves the raw against the stored hash).
//   - VerifyUserCredentials returns the SAME ErrInvalidCredentials
//     sentinel for both "unknown user" and "wrong password" paths — no
//     user-enumeration oracle via error differentiation.
//   - Correct credentials → nil error.
package auth

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

const (
	serviceTestDefaultDBURL = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
)

var (
	svcProvisionOnce sync.Once
	svcProvisioned   bool
	svcProvisionErr  error
)

// serviceTestDatabaseURL resolves the isolated test DB DSN (BOOM_TEST_DATABASE_URL override).
func serviceTestDatabaseURL() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return serviceTestDefaultDBURL
}

// ensureServiceTestDB provisions + migrates the isolated test DB exactly once
// per test binary. Idempotent and safe under parallel Its.
func ensureServiceTestDB() error {
	svcProvisionOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		url := serviceTestDatabaseURL()
		if err := ensureSvcDatabase(ctx, url); err != nil {
			svcProvisionErr = err
			return
		}
		if err := db.MigrateURL(ctx, url); err != nil {
			svcProvisionErr = err
			return
		}
		svcProvisioned = true
	})
	return svcProvisionErr
}

// openServiceTestDB skips the current spec if Postgres isn't reachable,
// otherwise returns a freshly-opened *db.DB and registers Close cleanup.
func openServiceTestDB() *db.DB {
	if err := ensureServiceTestDB(); err != nil {
		Skip("skipping: isolated test DB unavailable: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := db.New(ctx, serviceTestDatabaseURL())
	if err != nil {
		Skip("skipping: could not open boomtime_test: " + err.Error())
	}
	DeferCleanup(func() { database.Close() })
	return database
}

// uniqueUsername gives every spec a fresh username so parallel packages
// don't race on the shared boomtime_test DB.
func uniqueUsername(prefix string) string {
	return prefix + "_" + time.Now().Format("150405.000000000")
}

// registerUserCleanup deletes every child row + the users row on spec exit.
// Order matters (children first) — FKs to users would otherwise block.
func registerUserCleanup(database *db.DB, username string) {
	DeferCleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM auth_tokens WHERE owner=$1`,
			`DELETE FROM refresh_tokens WHERE owner=$1`,
			`DELETE FROM users WHERE username=$1`,
		} {
			_, _ = database.Pool.Exec(ctx, q, username)
		}
	})
}

var _ = Describe("CreateUser (gaka-se2.4)", func() {
	It("persists the argon2id hash of the password — plaintext never lands in the DB", func() {
		database := openServiceTestDB()
		ctx := context.Background()
		username := uniqueUsername("se2_create_user")
		registerUserCleanup(database, username)

		plaintext := "correct-horse-battery-staple-1"
		Expect(CreateUser(ctx, database, username, plaintext)).To(Succeed())

		// Read the row back directly from Postgres — bypass the service to
		// prove the storage layer holds a hash, not the plaintext.
		var storedHash, storedSalt []byte
		var argonVersion int
		err := database.Pool.QueryRow(ctx,
			`SELECT hashed_password, salt_used, argon_version FROM users WHERE username=$1`,
			username).Scan(&storedHash, &storedSalt, &argonVersion)
		Expect(err).NotTo(HaveOccurred())

		Expect(storedHash).NotTo(BeEmpty())
		Expect(storedSalt).NotTo(BeEmpty())
		Expect(argonVersion).To(Equal(ArgonVersionCurrent),
			"new users must land at the current argon generation (v2), not legacy v1")

		// Non-tautology check: stored bytes must NOT equal the plaintext bytes.
		Expect(storedHash).NotTo(Equal([]byte(plaintext)),
			"CRITICAL: hashed_password holds the plaintext — encryption at rest is broken")
		// And the argon2 hash must actually verify — proves it's the RIGHT hash.
		Expect(VerifyPasswordWithVersion(plaintext, storedHash, storedSalt, argonVersion)).To(BeTrue(),
			"stored hash+salt must verify the plaintext under argon_version=%d", argonVersion)
	})

	It("returns ErrUserExists when the username is already taken (no silent overwrite)", func() {
		database := openServiceTestDB()
		ctx := context.Background()
		username := uniqueUsername("se2_dup_user")
		registerUserCleanup(database, username)

		Expect(CreateUser(ctx, database, username, "first-password-1")).To(Succeed())

		// Capture the first user's hash so we can prove no overwrite happened.
		var firstHash []byte
		Expect(database.Pool.QueryRow(ctx,
			`SELECT hashed_password FROM users WHERE username=$1`, username).
			Scan(&firstHash)).To(Succeed())

		// Second call with a different password must reject.
		err := CreateUser(ctx, database, username, "second-password-2")
		Expect(err).To(MatchError(ErrUserExists),
			"duplicate username must surface as ErrUserExists sentinel")

		// And the row must be unchanged — no silent overwrite.
		var afterHash []byte
		Expect(database.Pool.QueryRow(ctx,
			`SELECT hashed_password FROM users WHERE username=$1`, username).
			Scan(&afterHash)).To(Succeed())
		Expect(afterHash).To(Equal(firstHash),
			"CreateUser on an existing username must NOT overwrite the stored hash")
	})
})

var _ = Describe("CreateAPIToken (gaka-se2.4)", func() {
	It("returns a non-empty raw token AND persists only its SHA-256 (raw never lands in DB)", func() {
		database := openServiceTestDB()
		ctx := context.Background()
		username := uniqueUsername("se2_token")
		registerUserCleanup(database, username)

		// Need a user first (auth_tokens.owner FK → users.username).
		Expect(CreateUser(ctx, database, username, "token-owner-password-1")).To(Succeed())

		raw, err := CreateAPIToken(ctx, database, username, "test-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).NotTo(BeEmpty(), "raw token must be returned so the caller can show it once")

		// Read the stored hashed_token back — must be the SHA-256 of ToBase64(raw),
		// NOT the raw bytes themselves. This is what makes a DB read useless to
		// an attacker (gaka-b5x.2).
		var storedHashed []byte
		var tokenName *string
		Expect(database.Pool.QueryRow(ctx,
			`SELECT hashed_token, token_name FROM auth_tokens WHERE owner=$1`, username).
			Scan(&storedHashed, &tokenName)).To(Succeed())

		Expect(storedHashed).NotTo(BeEmpty())
		Expect(storedHashed).NotTo(Equal([]byte(raw)),
			"CRITICAL: raw token bytes landed in hashed_token column — DB read yields usable session")
		Expect(storedHashed).NotTo(Equal([]byte(ToBase64(raw))),
			"CRITICAL: base64(raw) landed unmodified — must be SHA-256 hashed")
		// And it MUST equal SHA-256(base64(raw)) — the exact contract.
		Expect(storedHashed).To(Equal(HashToken(ToBase64(raw))),
			"hashed_token must be SHA-256(ToBase64(raw)) per gaka-b5x.2")
		Expect(len(storedHashed)).To(Equal(32), "SHA-256 output is 32 bytes")

		// Optional name is stored so the tokens list can render it.
		Expect(tokenName).NotTo(BeNil())
		Expect(*tokenName).To(Equal("test-token"))
	})

	It("accepts an empty display name (stored as NULL, not empty string)", func() {
		database := openServiceTestDB()
		ctx := context.Background()
		username := uniqueUsername("se2_token_noname")
		registerUserCleanup(database, username)
		Expect(CreateUser(ctx, database, username, "token-owner-password-1")).To(Succeed())

		raw, err := CreateAPIToken(ctx, database, username, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).NotTo(BeEmpty())

		var tokenName *string
		Expect(database.Pool.QueryRow(ctx,
			`SELECT token_name FROM auth_tokens WHERE owner=$1`, username).
			Scan(&tokenName)).To(Succeed())
		Expect(tokenName).To(BeNil(),
			"empty name argument must land as NULL, not empty string, so the list UI can filter")
	})
})

var _ = Describe("service error propagation (gaka-se2.4 infra failures)", func() {
	// These specs prove that the composed service functions actually surface
	// the underlying db-layer errors (rather than swallowing / mislabelling
	// them). We close the pool BEFORE the call so every db.* op fails with a
	// pool-closed error — the only cross-boundary way to trigger the
	// non-happy-path branches without an interface.

	It("CreateUser bubbles up the DB error when the pool is unavailable", func() {
		database := openServiceTestDB()
		// Take the pool down; every subsequent Exec / Query returns error.
		database.Close()
		err := CreateUser(context.Background(), database,
			uniqueUsername("se2_createuser_dberr"), "any-password-1")
		Expect(err).To(HaveOccurred(),
			"CreateUser must surface the underlying DB failure, not silently succeed")
		Expect(err).NotTo(MatchError(ErrUserExists),
			"a DB failure must NOT be misreported as ErrUserExists")
	})

	It("CreateAPIToken bubbles up the DB error when the pool is unavailable", func() {
		database := openServiceTestDB()
		database.Close()
		raw, err := CreateAPIToken(context.Background(), database,
			uniqueUsername("se2_token_dberr"), "name")
		Expect(err).To(HaveOccurred(),
			"CreateAPIToken must surface the underlying DB failure")
		Expect(raw).To(Equal(""),
			"on error, CreateAPIToken must NOT leak the raw token — caller may log err+raw and expose an unpersisted secret")
	})

	It("VerifyUserCredentials bubbles up the DB error (does NOT mask as ErrInvalidCredentials)", func() {
		database := openServiceTestDB()
		database.Close()
		err := VerifyUserCredentials(context.Background(), database,
			uniqueUsername("se2_verify_dberr"), "any-password-1")
		Expect(err).To(HaveOccurred())
		Expect(err).NotTo(MatchError(ErrInvalidCredentials),
			"a DB outage must NOT be reported as ErrInvalidCredentials — that would look like a wrong-password lockout to the caller")
	})
})

var _ = Describe("VerifyUserCredentials (gaka-se2.4 user-enumeration defense)", func() {
	It("returns nil for a correct username + password pair", func() {
		database := openServiceTestDB()
		ctx := context.Background()
		username := uniqueUsername("se2_verify_ok")
		registerUserCleanup(database, username)

		plaintext := "verify-happy-path-1"
		Expect(CreateUser(ctx, database, username, plaintext)).To(Succeed())

		Expect(VerifyUserCredentials(ctx, database, username, plaintext)).To(Succeed())
	})

	It("returns ErrInvalidCredentials for the WRONG password on an existing user", func() {
		database := openServiceTestDB()
		ctx := context.Background()
		username := uniqueUsername("se2_verify_bad_pw")
		registerUserCleanup(database, username)

		Expect(CreateUser(ctx, database, username, "the-actual-password-1")).To(Succeed())

		err := VerifyUserCredentials(ctx, database, username, "not-the-password-2")
		Expect(err).To(MatchError(ErrInvalidCredentials),
			"wrong password must surface as ErrInvalidCredentials sentinel")
	})

	It("returns the SAME ErrInvalidCredentials for an unknown username — no user-enumeration oracle via error differentiation", func() {
		// THE user-enumeration defense: the "no such user" branch must NOT
		// surface a different error than the "wrong password" branch, or a
		// caller can probe usernames by inspecting err.
		database := openServiceTestDB()
		ctx := context.Background()

		unknownName := uniqueUsername("se2_no_such_user_never_inserted")
		// Belt+braces: confirm this username genuinely does not exist.
		u, err := database.GetUserByName(ctx, unknownName)
		Expect(err).NotTo(HaveOccurred())
		Expect(u).To(BeNil(), "test precondition: unknown user must not exist")

		verifyErr := VerifyUserCredentials(ctx, database, unknownName, "any-password-1")
		Expect(verifyErr).To(MatchError(ErrInvalidCredentials),
			"unknown user must surface as ErrInvalidCredentials — same sentinel as wrong-password to prevent enumeration")

		// Cross-check against the wrong-password error: the two sentinels
		// must be the EXACT same variable so callers switching on Is(err, X)
		// cannot distinguish.
		existingName := uniqueUsername("se2_verify_cross")
		registerUserCleanup(database, existingName)
		Expect(CreateUser(ctx, database, existingName, "real-password-1")).To(Succeed())
		wrongPwErr := VerifyUserCredentials(ctx, database, existingName, "wrong-password-2")
		Expect(wrongPwErr).To(MatchError(ErrInvalidCredentials))
		Expect(wrongPwErr.Error()).To(Equal(verifyErr.Error()),
			"unknown-user and wrong-password errors must be indistinguishable to the caller")
	})
})

// ---- DB provisioning internals (mirror internal/db/main_test.go /
// internal/testutil provisioning; can't import testutil because it depends
// on internal/auth).

func ensureSvcDatabase(ctx context.Context, targetURL string) error {
	target := svcDBNameFromURL(targetURL)
	if target == "" {
		return errNoDBName
	}
	var lastErr error
	for _, maint := range []string{"postgres", "test"} {
		pool, err := pgxpool.New(ctx, svcMaintenanceURLFor(targetURL, maint))
		if err != nil {
			lastErr = err
			continue
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			lastErr = err
			continue
		}
		_, err = pool.Exec(ctx, `CREATE DATABASE `+svcQuoteIdent(target))
		pool.Close()
		if err == nil || svcIsAlreadyExists(err) {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errNoMaintDB
	}
	return lastErr
}

var (
	errNoDBName  = &svcErr{msg: "could not determine database name from URL"}
	errNoMaintDB = &svcErr{msg: "no reachable maintenance database"}
)

type svcErr struct{ msg string }

func (e *svcErr) Error() string { return e.msg }

func svcMaintenanceURLFor(dsn, maintDB string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	rest := dsn[slash+1:]
	q := strings.Index(rest, "?")
	params := ""
	if q >= 0 {
		params = rest[q:]
	}
	return dsn[:slash+1] + maintDB + params
}

func svcDBNameFromURL(dsn string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return ""
	}
	rest := dsn[slash+1:]
	if q := strings.Index(rest, "?"); q >= 0 {
		rest = rest[:q]
	}
	return rest
}

func svcIsAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "already exists") || strings.Contains(s, "42P04")
}

func svcQuoteIdent(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}
