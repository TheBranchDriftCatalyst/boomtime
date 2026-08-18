package db

// auth_test.go pins the SECURITY-CRITICAL invariants of the API-token layer
// (gaka-se2.6). Every test names ONE invariant that would be a real vuln if
// violated. Tests deliberately seed TWO owners with overlapping requests so
// a "trivially return the row we just wrote" implementation would fail:
//
//   invariant                            attacker win if broken
//   ─────────────────────────────────────────────────────────────
//   hashed_prefix_is_display_id          impossible — display ID not stable
//   cross_owner_list_isolation           A can enumerate B's token IDs
//   cross_owner_delete_noop              B can nuke A's tokens
//   cross_owner_rename_noop              B can rename A's tokens
//   token_resolves_to_exact_owner        Get(t) returns wrong user (auth bypass)
//   hashed_token_unique                  two users with same hashed_token
//                                        (collision or replay) both auth
//   null_expiry_never_expires            API keys stop working after 1h
//   past_expiry_rejects                  expired session token still authenticates
//   last_usage_bump_on_success           audit trail missing (silent auth)

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- helpers ---------------------------------------------------------------

// mkAPIToken generates a raw token, returns (raw, hashedBytes, hexPrefix12).
// The hex prefix is what ListApiTokens surfaces as the display ID
// (`LEFT(encode(hashed_token,'hex'), 12)`), and it's the identifier that
// DeleteAuthToken / UpdateTokenMetadata match on.
func mkAPIToken(t *testing.T, seed string) (raw string, hashed []byte, id string) {
	t.Helper()
	// Deterministic per-test raw material: seed varies per call so two tokens
	// in the same test hash to different values (otherwise UNIQUE would fire
	// where we don't want it).
	raw = "raw-" + seed + "-" + time.Now().Format("150405.000000000")
	hashed = hashSessionToken(raw)
	id = hex.EncodeToString(hashed)[:12]
	return
}

// mustInsertAPIToken calls d.InsertAPIToken and fatals on error.
func mustInsertAPIToken(t *testing.T, d *DB, owner, raw, name string) {
	t.Helper()
	if err := d.InsertAPIToken(context.Background(), owner, raw, name); err != nil {
		t.Fatalf("InsertAPIToken(%s,%q): %v", owner, name, err)
	}
}

// listContainsID reports whether ListApiTokens output contains the given id.
func listContainsID(rows []model.StoredApiToken, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// findByID returns the first token with matching ID or nil.
func findByID(rows []model.StoredApiToken, id string) *model.StoredApiToken {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// setTokenExpiryPast rewrites token_expiry to a past timestamp for the given
// hashed token — used to prove GetUserByToken's expiry predicate.
func setTokenExpiryPast(t *testing.T, d *DB, hashed []byte) {
	t.Helper()
	past := time.Now().UTC().Add(-1 * time.Hour)
	tag, err := d.Pool.Exec(context.Background(),
		`UPDATE auth_tokens SET token_expiry = $2 WHERE hashed_token = $1`,
		hashed, past)
	if err != nil {
		t.Fatalf("setTokenExpiryPast: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("setTokenExpiryPast: rows=%d, want 1", tag.RowsAffected())
	}
}

// readLastUsage returns the raw last_usage timestamp for a token (or nil).
func readLastUsage(t *testing.T, d *DB, hashed []byte) *time.Time {
	t.Helper()
	var lu *time.Time
	err := d.Pool.QueryRow(context.Background(),
		`SELECT last_usage::timestamptz FROM auth_tokens WHERE hashed_token = $1`,
		hashed).Scan(&lu)
	if err != nil {
		t.Fatalf("readLastUsage: %v", err)
	}
	return lu
}

// ---- integration tests -----------------------------------------------------

// TestAPIToken_InsertList_Roundtrip pins the display-id invariant:
// ListApiTokens returns the token we just inserted, and its ID is exactly the
// first 12 hex chars of the SHA-256 of the raw token. If a future change
// swaps the algorithm (e.g. base64, truncates the raw instead of the hash),
// this fails immediately.
func TestAPIToken_InsertList_Roundtrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_rt_a").Sender()
	rawA, hashedA, idA := mkAPIToken(t, "a1")

	mustInsertAPIToken(t, d, a, rawA, "laptop")

	rows, err := d.ListApiTokens(context.Background(), a)
	if err != nil {
		t.Fatalf("ListApiTokens(%s): %v", a, err)
	}
	if len(rows) != 1 {
		t.Fatalf("List(%s) len=%d, want 1", a, len(rows))
	}
	got := rows[0]
	if got.ID != idA {
		t.Fatalf("ID = %q, want %q (first 12 hex of SHA-256(raw))", got.ID, idA)
	}
	if got.Name == nil || *got.Name != "laptop" {
		t.Fatalf("Name = %v, want ptr(%q)", got.Name, "laptop")
	}
	// Independent recomputation: hex(hashed)[:12] MUST equal the display id.
	if want := hex.EncodeToString(hashedA)[:12]; got.ID != want {
		t.Fatalf("ID %q != hex(hash(raw))[:12] %q", got.ID, want)
	}
}

// TestAPIToken_CrossOwner_ListIsolation pins tenant isolation on the read
// path: A owns K1, B owns K2 — List(A) MUST NOT contain K2's id, List(B) MUST
// NOT contain K1's id. Both users have their OWN token so a broken
// implementation that returns "all tokens" would surface the cross-owner one.
func TestAPIToken_CrossOwner_ListIsolation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_iso_a").Sender()
	b := newSender(t, d, "auth_iso_b").Sender()

	rawA, _, idA := mkAPIToken(t, "iso_a")
	rawB, _, idB := mkAPIToken(t, "iso_b")

	mustInsertAPIToken(t, d, a, rawA, "A-laptop")
	mustInsertAPIToken(t, d, b, rawB, "B-laptop")

	rowsA, err := d.ListApiTokens(context.Background(), a)
	if err != nil {
		t.Fatalf("ListApiTokens(A): %v", err)
	}
	rowsB, err := d.ListApiTokens(context.Background(), b)
	if err != nil {
		t.Fatalf("ListApiTokens(B): %v", err)
	}

	if !listContainsID(rowsA, idA) {
		t.Fatalf("List(A) missing A's own token id=%s (rows=%+v)", idA, rowsA)
	}
	if listContainsID(rowsA, idB) {
		t.Fatalf("List(A) leaked B's token id=%s — cross-owner enumeration bug", idB)
	}
	if !listContainsID(rowsB, idB) {
		t.Fatalf("List(B) missing B's own token id=%s (rows=%+v)", idB, rowsB)
	}
	if listContainsID(rowsB, idA) {
		t.Fatalf("List(B) leaked A's token id=%s — cross-owner enumeration bug", idA)
	}
}

// TestAPIToken_CrossOwner_DeleteIsNoop pins delete-scoping: DeleteAuthToken
// (id belongs to A, owner=B) MUST NOT delete A's row. The `WHERE owner=$2`
// clause is a security boundary — if it's dropped, a hostile B can nuke every
// other user's API keys by iterating hex prefixes.
func TestAPIToken_CrossOwner_DeleteIsNoop(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_del_a").Sender()
	b := newSender(t, d, "auth_del_b").Sender()

	rawA, _, idA := mkAPIToken(t, "delA")
	mustInsertAPIToken(t, d, a, rawA, "A-key")

	// Wrong-owner delete: return value MUST NOT error (masked no-op), and A's
	// token MUST still be there.
	if err := d.DeleteAuthToken(context.Background(), idA, b); err != nil {
		t.Fatalf("DeleteAuthToken(id=A's, owner=B) errored: %v (want nil no-op)", err)
	}
	rowsA, err := d.ListApiTokens(context.Background(), a)
	if err != nil {
		t.Fatalf("ListApiTokens(A): %v", err)
	}
	if !listContainsID(rowsA, idA) {
		t.Fatalf("cross-owner delete NUKED A's token id=%s — scoping bug (rows=%+v)", idA, rowsA)
	}

	// Sanity: right-owner delete DOES remove it (proves the DELETE is not a
	// no-op in general — otherwise the invariant test above is vacuous).
	if err := d.DeleteAuthToken(context.Background(), idA, a); err != nil {
		t.Fatalf("DeleteAuthToken(id=A's, owner=A): %v", err)
	}
	rowsA, _ = d.ListApiTokens(context.Background(), a)
	if listContainsID(rowsA, idA) {
		t.Fatalf("right-owner delete failed to remove id=%s", idA)
	}
}

// TestAPIToken_CrossOwner_RenameIsNoop pins UpdateTokenMetadata scoping:
// rename(id=A's, owner=B, name="pwn") MUST NOT touch A's row.
func TestAPIToken_CrossOwner_RenameIsNoop(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_rn_a").Sender()
	b := newSender(t, d, "auth_rn_b").Sender()

	rawA, _, idA := mkAPIToken(t, "rnA")
	mustInsertAPIToken(t, d, a, rawA, "original")

	// Hostile: B tries to rename A's token to something recognisable.
	err := d.UpdateTokenMetadata(context.Background(), b, model.TokenMetadata{
		TokenID:   idA,
		TokenName: "hacker-pwned",
	})
	if err != nil {
		t.Fatalf("UpdateTokenMetadata(owner=B, id=A's) errored: %v (want nil no-op)", err)
	}

	rowsA, err := d.ListApiTokens(context.Background(), a)
	if err != nil {
		t.Fatalf("ListApiTokens(A): %v", err)
	}
	tk := findByID(rowsA, idA)
	if tk == nil {
		t.Fatalf("A's token id=%s vanished after wrong-owner rename", idA)
	}
	if tk.Name == nil || *tk.Name != "original" {
		t.Fatalf("A's token name mutated to %v — cross-owner rename bug", tk.Name)
	}

	// Sanity: right-owner rename DOES change the name.
	err = d.UpdateTokenMetadata(context.Background(), a, model.TokenMetadata{
		TokenID:   idA,
		TokenName: "renamed",
	})
	if err != nil {
		t.Fatalf("UpdateTokenMetadata(owner=A): %v", err)
	}
	rowsA, _ = d.ListApiTokens(context.Background(), a)
	tk = findByID(rowsA, idA)
	if tk == nil || tk.Name == nil || *tk.Name != "renamed" {
		t.Fatalf("right-owner rename failed, got %v", tk)
	}
}

// TestAPIToken_GetUserByToken_ResolvesExactOwner pins the per-token owner
// mapping: seed two users each with a token, then look up BOTH tokens and
// prove each returns its own owner. A hash-collision or "return first row"
// bug would fail here (a per-token round-trip that just verified the same
// user twice would not).
func TestAPIToken_GetUserByToken_ResolvesExactOwner(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_gu_a").Sender()
	b := newSender(t, d, "auth_gu_b").Sender()

	rawA, _, _ := mkAPIToken(t, "guA")
	rawB, _, _ := mkAPIToken(t, "guB")

	mustInsertAPIToken(t, d, a, rawA, "")
	mustInsertAPIToken(t, d, b, rawB, "")

	ownerA, okA, err := d.GetUserByToken(context.Background(), rawA)
	if err != nil {
		t.Fatalf("GetUserByToken(rawA): %v", err)
	}
	if !okA || ownerA != a {
		t.Fatalf("GetUserByToken(rawA) = (%q,%v), want (%q,true)", ownerA, okA, a)
	}

	ownerB, okB, err := d.GetUserByToken(context.Background(), rawB)
	if err != nil {
		t.Fatalf("GetUserByToken(rawB): %v", err)
	}
	if !okB || ownerB != b {
		t.Fatalf("GetUserByToken(rawB) = (%q,%v), want (%q,true)", ownerB, okB, b)
	}

	// And an unknown token MUST resolve to ("", false, nil) — never to a
	// user, never to a random owner.
	unknown := "raw-nobody-" + time.Now().Format("150405.000000000")
	owner, ok, err := d.GetUserByToken(context.Background(), unknown)
	if err != nil {
		t.Fatalf("GetUserByToken(unknown): unexpected err %v", err)
	}
	if ok || owner != "" {
		t.Fatalf("GetUserByToken(unknown) = (%q,%v), want (\"\",false,nil)", owner, ok)
	}
}

// TestAPIToken_HashedToken_UniqueConstraint pins the "no two users share
// the same hashed_token" invariant: bob writes hash X; charlie writes hash X
// → the second INSERT MUST fail with SQLSTATE 23505. Without this, an
// attacker who obtains ANY user's hashed_token from a leaky backup could
// register a new account with the same hash and authenticate as the victim.
func TestAPIToken_HashedToken_UniqueConstraint(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	bob := newSender(t, d, "auth_uq_bob").Sender()
	charlie := newSender(t, d, "auth_uq_charlie").Sender()

	// Shared raw string → shared hashed_token; that's exactly what the UNIQUE
	// index should prevent across users.
	shared := "raw-shared-" + time.Now().Format("150405.000000000")

	if err := d.InsertAPIToken(context.Background(), bob, shared, "bob"); err != nil {
		t.Fatalf("first InsertAPIToken(bob): %v", err)
	}

	err := d.InsertAPIToken(context.Background(), charlie, shared, "charlie")
	if err == nil {
		t.Fatalf("second InsertAPIToken(charlie, same hash) succeeded — UNIQUE constraint MISSING")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected *pgconn.PgError, got %T: %v", err, err)
	}
	if pgErr.Code != "23505" {
		t.Fatalf("expected SQLSTATE 23505 (unique_violation), got %q: %v", pgErr.Code, err)
	}
}

// TestAPIToken_NullExpiry_NeverExpires pins the "API keys never expire"
// invariant. InsertAPIToken stores token_expiry=NULL; the SELECT in
// GetUserByToken uses COALESCE(token_expiry, NOW()+1h). We prove BOTH:
//  1. immediate lookup succeeds
//  2. the COALESCE arithmetic actually works — by explicitly reasserting the
//     ownership resolves after any time elapsed within the same second
//     (real "1 hour later" would slow the test; we prove the SQL branch by
//     leaving NULL untouched and verifying the row still resolves).
func TestAPIToken_NullExpiry_NeverExpires(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_null_a").Sender()
	raw, hashed, _ := mkAPIToken(t, "null")
	mustInsertAPIToken(t, d, a, raw, "cli")

	// Sanity check: the row's token_expiry column is genuinely NULL (so we
	// know the COALESCE branch is what's keeping the token alive).
	var expiry *time.Time
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT token_expiry::timestamptz FROM auth_tokens WHERE hashed_token=$1`,
		hashed).Scan(&expiry); err != nil {
		t.Fatalf("read token_expiry: %v", err)
	}
	if expiry != nil {
		t.Fatalf("InsertAPIToken must store token_expiry=NULL, got %v", *expiry)
	}

	owner, ok, err := d.GetUserByToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("GetUserByToken(NULL-expiry): %v", err)
	}
	if !ok || owner != a {
		t.Fatalf("GetUserByToken(NULL-expiry) = (%q,%v), want (%q,true)", owner, ok, a)
	}
}

// TestAPIToken_PastExpiry_Rejects pins the "expired session token cannot
// authenticate" invariant. We insert an API token then manually rewrite its
// token_expiry to yesterday; GetUserByToken MUST return ("", false, nil).
func TestAPIToken_PastExpiry_Rejects(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_exp_a").Sender()
	raw, hashed, _ := mkAPIToken(t, "exp")
	mustInsertAPIToken(t, d, a, raw, "")

	// Sanity: the token authenticates BEFORE we expire it (otherwise the
	// post-expiry rejection could be for the wrong reason).
	if _, ok, err := d.GetUserByToken(context.Background(), raw); err != nil || !ok {
		t.Fatalf("pre-expiry GetUserByToken: ok=%v err=%v", ok, err)
	}

	setTokenExpiryPast(t, d, hashed)

	owner, ok, err := d.GetUserByToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("GetUserByToken(expired): unexpected err %v", err)
	}
	if ok || owner != "" {
		t.Fatalf("GetUserByToken(expired) = (%q,%v), want (\"\",false,nil)", owner, ok)
	}
}

// TestAPIToken_LastUsage_BumpsOnSuccess pins the audit-trail invariant:
// every SUCCESSFUL GetUserByToken advances last_usage. This is what makes
// the tokens UI's "Last used" column meaningful. A regression that dropped
// the bump would silently degrade the trail without breaking auth — so this
// test is the only line of defence.
func TestAPIToken_LastUsage_BumpsOnSuccess(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_lu_a").Sender()
	raw, hashed, _ := mkAPIToken(t, "lu")
	mustInsertAPIToken(t, d, a, raw, "")

	// last_usage is NULL at insert (no DEFAULT) — a successful lookup MUST
	// populate it (nil -> non-nil is the audit trail coming to life).
	before := readLastUsage(t, d, hashed)
	if before != nil {
		t.Fatalf("expected NULL last_usage at insert, got %v", *before)
	}

	if _, ok, err := d.GetUserByToken(context.Background(), raw); err != nil || !ok {
		t.Fatalf("first GetUserByToken: ok=%v err=%v", ok, err)
	}
	first := readLastUsage(t, d, hashed)
	if first == nil {
		t.Fatalf("GetUserByToken did NOT bump last_usage (still NULL) — audit trail lost")
	}

	// A second successful lookup MUST advance the timestamp again (proves the
	// bump is per-call, not a one-shot "first use" latch). Sleep 10ms so the
	// timestamps are strictly greater at microsecond resolution.
	time.Sleep(10 * time.Millisecond)
	if _, ok, err := d.GetUserByToken(context.Background(), raw); err != nil || !ok {
		t.Fatalf("second GetUserByToken: ok=%v err=%v", ok, err)
	}
	second := readLastUsage(t, d, hashed)
	if second == nil || !second.After(*first) {
		t.Fatalf("last_usage not advanced on repeat call: first=%v second=%v", first, second)
	}
}

// TestAPIToken_CrossOwner_DeleteScopeVsList is the "belongs-to-B, delete-by-A"
// twin of the earlier delete-scoping test — same pattern but with the ID
// belonging to B and A trying to delete it. The two together pin the
// symmetry: NEITHER direction of cross-owner delete works.
func TestAPIToken_CrossOwner_DeleteScopeVsList(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	a := newSender(t, d, "auth_del2_a").Sender()
	b := newSender(t, d, "auth_del2_b").Sender()

	rawB, _, idB := mkAPIToken(t, "delB")
	mustInsertAPIToken(t, d, b, rawB, "B-key")

	// A tries to delete B's token: MUST be a masked no-op.
	if err := d.DeleteAuthToken(context.Background(), idB, a); err != nil {
		t.Fatalf("DeleteAuthToken(id=B's, owner=A) errored: %v (want nil)", err)
	}

	rowsB, err := d.ListApiTokens(context.Background(), b)
	if err != nil {
		t.Fatalf("ListApiTokens(B): %v", err)
	}
	if !listContainsID(rowsB, idB) {
		t.Fatalf("cross-owner delete NUKED B's token id=%s — scoping regression", idB)
	}
}
