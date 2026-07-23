// Package auth handles password hashing (Argon2id), API-token encoding, and
// refresh-token cookie construction/parsing. Ports PasswordUtils.hs + parts of Utils.hs.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

// Argon2id parameters, versioned.
//
// gaka-awh.6 (Bravo MEDIUM): the original params (time=1, parallelism=4) were
// BELOW OWASP ASVS L1 2025's floor on time and ABOVE its recommendation on
// parallelism (=1 keeps CPU-cache contention working against GPU crackers).
// We can't force existing users to re-enter their password, so we keep the
// original params as version 1 (verify-only) and mint every new hash under
// version 2. Login transparently re-hashes any v1 credential to v2 on
// successful auth (see handler.Login → DB.UpgradeArgonVersion).
//
// KEEP the constants named argonTime/argonMem/argonPar bound to the CURRENT
// version so unit tests can pin the current-generation params without
// touching the per-version tables. The pinned unit test
// TestArgon2Params_LockedToOWASPFloor_BravoRegression asserts EXACT values.
const (
	saltLen = 64 // bytes (hashSaltLen in PasswordUtils.hs)
	keyLen  = 64 // bytes (hashOutputLen in PasswordUtils.hs)

	// ArgonVersionLegacy is version 1 — the pre-Bravo params retained for
	// verification of existing user rows until they log in and get bumped.
	ArgonVersionLegacy = 1
	// ArgonVersionCurrent is version 2 — the OWASP ASVS L1 2025 floor.
	// New user creations + successful legacy logins land here.
	ArgonVersionCurrent = 2

	// Version 1 (legacy) parameters. DO NOT change these values — rows minted
	// under v1 can only be verified with EXACTLY these params.
	argonTimeV1 = 1
	argonMemV1  = 64 * 1024 // KiB (64 MiB)
	argonParV1  = 4

	// Version 2 (current) parameters — OWASP ASVS L1 2025.
	argonTimeV2 = 2
	argonMemV2  = 64 * 1024 // KiB (64 MiB)
	argonParV2  = 1

	// Aliases pinned to the CURRENT generation. The internal callers below
	// (HashPassword, VerifyPassword, BurnSentinelVerify) route through
	// argonParamsFor(ArgonVersionCurrent); these constants stay for the unit
	// test that asserts we didn't drift back below the OWASP floor.
	argonTime = argonTimeV2
	argonMem  = argonMemV2
	argonPar  = argonParV2
)

// argonParamsFor returns the (time, memKiB, parallelism) triple for the given
// argon version. Unknown versions fall through to the CURRENT generation — a
// defensive choice so a DB row inserted by a NEWER binary still gets a
// verification attempt (which will simply fail if the params really are
// wrong) rather than a nil-deref or panic.
func argonParamsFor(version int) (time uint32, memKiB uint32, parallelism uint8) {
	switch version {
	case ArgonVersionLegacy:
		return argonTimeV1, argonMemV1, argonParV1
	case ArgonVersionCurrent:
		return argonTimeV2, argonMemV2, argonParV2
	default:
		return argonTimeV2, argonMemV2, argonParV2
	}
}

// sentinelPassword is the fixed dummy plaintext whose Argon2id hash+salt is
// used by BurnSentinelVerify to make the "no such user" branch of Login take
// the SAME wall-clock time as the "user exists / wrong password" branch.
// Value is arbitrary (never accepted anywhere) — only the CPU cost matters.
const sentinelPassword = "gaka-imm-constant-time-sentinel-do-not-accept"

var (
	sentinelOnce sync.Once
	sentinelHash []byte
	sentinelSalt []byte
	// sentinelVerifyCount is a test-only counter incremented every time
	// BurnSentinelVerify runs. Handlers use BurnSentinelVerify on the
	// user-not-found branch so a spy test can assert the constant-time
	// path fires even when the system's timing is too noisy to measure
	// the ~10ms delta directly. Package-private + read via
	// SentinelVerifyCount so tests can't accidentally reset it.
	sentinelVerifyCount uint64
	sentinelCountMu     sync.Mutex
)

// initSentinel computes the sentinel Argon2id hash+salt exactly once. Called
// lazily on the first BurnSentinelVerify — pushes the ~10ms init cost off the
// process-startup path and onto the first unauth'd login attempt. The salt is
// random per-process (never persisted), so no cross-process oracle is created.
func initSentinel() {
	sentinelOnce.Do(func() {
		salt := make([]byte, saltLen)
		if _, err := rand.Read(salt); err != nil {
			// Fatal here would kill the process on the first bad login;
			// fall back to a zero salt — the sentinel still burns argon2
			// time (its only job) and is never compared against real data.
			salt = make([]byte, saltLen)
		}
		sentinelSalt = salt
		sentinelHash = argon2.IDKey([]byte(sentinelPassword), sentinelSalt,
			argonTime, argonMem, argonPar, keyLen)
	})
}

// BurnSentinelVerify runs Argon2id against the sentinel hash+salt using the
// caller-supplied password, discards the result, and returns. Callers hit this
// on the "user does not exist" branch of Login so that path takes the same
// ~10ms as the "user exists / wrong password" branch — closing gaka-imm's
// timing oracle. The return value is intentionally ignored; the compiler
// cannot elide the argon2.IDKey call because subtle.ConstantTimeCompare has
// observable side-effects on the sink argument. Callers MUST still return
// InvalidCredentials.
func BurnSentinelVerify(password string) {
	initSentinel()
	sentinelCountMu.Lock()
	sentinelVerifyCount++
	sentinelCountMu.Unlock()
	computed := argon2.IDKey([]byte(password), sentinelSalt, argonTime, argonMem, argonPar, keyLen)
	// ConstantTimeCompare's result is discarded — the side-effect we need
	// is CPU time, not the boolean.
	_ = subtle.ConstantTimeCompare(computed, sentinelHash)
}

// SentinelVerifyCount returns how many times BurnSentinelVerify has run in
// this process. Exposed for gaka-imm's spy test — production callers must not
// depend on this value.
func SentinelVerifyCount() uint64 {
	sentinelCountMu.Lock()
	defer sentinelCountMu.Unlock()
	return sentinelVerifyCount
}

// HashToken returns the SHA-256 digest of a raw session token as bytes.
// gaka-b5x part 2: refresh_tokens.refresh_token and auth_tokens.token used to
// be stored verbatim as the base64(uuid) value the client presents; a DB read
// yielded directly usable sessions. We now store SHA-256 of the token in a
// new bytea column and compare on lookup. No salt: the raw tokens are already
// high-entropy (128 bits of UUIDv4 randomness) so salting adds no bits
// against pre-image; using a plain hash keeps the lookup a single indexed
// equality. Compare with crypto/subtle.ConstantTimeCompare downstream.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// HashPassword generates a 64-byte random salt and an Argon2id hash of the
// password using the CURRENT-generation params (ArgonVersionCurrent). Callers
// that need to pin a specific version (e.g. tests seeding legacy rows) should
// use HashPasswordWithVersion directly.
func HashPassword(password string) (hash, salt []byte, err error) {
	return HashPasswordWithVersion(password, ArgonVersionCurrent)
}

// HashPasswordWithVersion is HashPassword with an explicit argon2id parameter
// generation. version = ArgonVersionCurrent (2) is what every new user + every
// rehash-on-login lands at. version = ArgonVersionLegacy (1) is only used by
// tests planting a pre-Bravo hash so we can exercise the transparent upgrade
// path (gaka-awh.6).
func HashPasswordWithVersion(password string, version int) (hash, salt []byte, err error) {
	salt = make([]byte, saltLen)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, err
	}
	t, m, p := argonParamsFor(version)
	hash = argon2.IDKey([]byte(password), salt, t, m, p, keyLen)
	return hash, salt, nil
}

// VerifyPassword recomputes the Argon2id hash with the stored salt using the
// CURRENT-generation params and compares it in constant time. Kept for
// compatibility with callers that don't have a stored version handy (e.g. the
// sentinel + tests that hash+verify in a single scope).
func VerifyPassword(password string, storedHash, storedSalt []byte) bool {
	return VerifyPasswordWithVersion(password, storedHash, storedSalt, ArgonVersionCurrent)
}

// VerifyPasswordWithVersion is VerifyPassword parameterised by the stored
// argon version. Callers reading a users row should use this and pass the
// version they read from the argon_version column so a v1 hash is verified
// with v1 params (and a v2 hash with v2 params). Cross-version verification
// returns false — a v1 hash checked with v2 params does NOT authenticate.
func VerifyPasswordWithVersion(password string, storedHash, storedSalt []byte, version int) bool {
	t, m, p := argonParamsFor(version)
	computed := argon2.IDKey([]byte(password), storedSalt, t, m, p, keyLen)
	return subtle.ConstantTimeCompare(computed, storedHash) == 1
}

// NewRawToken returns a random UUIDv4 string (the raw token handed to the user).
func NewRawToken() string {
	return uuid.NewString()
}

// ToBase64 base64-encodes a string (Utils.toBase64). Used to derive the stored
// token value from the raw UUID string.
func ToBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// ParseAuthHeader strips the "Basic " prefix from an Authorization header value.
// The remaining value is already base64(uuidString) — i.e. exactly what is stored
// in auth_tokens.token, so it is compared directly.
// Returns the token and true if the header was present and well-formed.
func ParseAuthHeader(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(header, "Basic")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// ParseRefreshCookie extracts the refresh_token value from a raw Cookie header.
func ParseRefreshCookie(cookieHeader string) (string, bool) {
	for _, part := range strings.Split(cookieHeader, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[0] == "refresh_token" {
			return kv[1], true
		}
	}
	return "", false
}
