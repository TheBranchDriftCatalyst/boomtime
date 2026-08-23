package db

// boom-93f.14: OIDC web users get a SHORT access bearer with NO local refresh
// token, and those bearers are revoked wholesale on logout — so a federated
// session can't be converted into a standalone, unrevocable local credential.

import (
	"context"
	"testing"
)

func TestOIDCAccessToken_ShortLivedAndRevocable(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	const name = "oidcbearer_gaka93f14"
	del := func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name)
	}
	del()
	t.Cleanup(del)

	if ok, err := d.InsertUser(ctx, StoredUser{
		Username: name, HashedPassword: []byte{1}, SaltUsed: []byte{1}, ArgonVersion: 2,
	}); err != nil || !ok {
		t.Fatalf("InsertUser = %v, %v", ok, err)
	}

	// Mint an OIDC access-only bearer.
	const tok = "oidc-bearer-tok"
	if err := d.CreateOIDCAccessToken(ctx, name, tok); err != nil {
		t.Fatalf("CreateOIDCAccessToken: %v", err)
	}

	// It resolves as a bearer...
	if owner, ok, _ := d.GetUserByToken(ctx, tok); !ok || owner != name {
		t.Fatalf("bearer should resolve to %s; got (%q, %v)", name, owner, ok)
	}
	// ...but NO local refresh token was created (that was the escape hatch).
	var refreshCount int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE owner=$1`, name).Scan(&refreshCount); err != nil {
		t.Fatalf("count refresh_tokens: %v", err)
	}
	if refreshCount != 0 {
		t.Errorf("OIDC access mint created %d refresh token(s); want 0 (must not mint an escaping local refresh)", refreshCount)
	}

	// Logout-style revoke kills the bearer immediately.
	if err := d.DeleteUserAccessTokens(ctx, name); err != nil {
		t.Fatalf("DeleteUserAccessTokens: %v", err)
	}
	if _, ok, _ := d.GetUserByToken(ctx, tok); ok {
		t.Error("bearer STILL resolves after DeleteUserAccessTokens — not revoked")
	}
}
