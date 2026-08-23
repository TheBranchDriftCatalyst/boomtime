package db

// Integration tests for the offline user-admin methods (boom-0oe.10) backing
// `boomtime user set-role / disable / enable / list`. Runs against the isolated
// boomtime_test DB (skips when no DB — see harness_test.go).

import (
	"context"
	"testing"
	"time"
)

// boom-93f.15: disabling an account is a KILL SWITCH — it must revoke every
// live credential (local access + refresh tokens, OIDC sessions) in the same
// transaction, so existing sessions die immediately regardless of the feature
// flag. This is the non-tautological half: the previous behavior set only
// disabled_at and left all sessions working.
func TestUserAdmin_DisableRevokesLiveSessions(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	const name = "killswitch_gaka93f15"
	del := func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM oidc_sessions WHERE username=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name)
	}
	del()
	t.Cleanup(del)

	if ok, err := d.InsertUser(ctx, StoredUser{
		Username: name, HashedPassword: []byte{1}, SaltUsed: []byte{1}, ArgonVersion: 2,
	}); err != nil || !ok {
		t.Fatalf("InsertUser(%s) = %v, %v; want true, nil", name, ok, err)
	}

	// Mint a live local access+refresh pair and a live OIDC session.
	td := TokenData{Owner: name, Token: "killswitch-tok", RefreshToken: "killswitch-ref"}
	if err := d.CreateAccessTokens(ctx, td, 24); err != nil {
		t.Fatalf("CreateAccessTokens: %v", err)
	}
	const sid = "killswitch-oidc-session-id"
	if err := d.CreateOIDCSession(ctx, sid, name, time.Now().Add(time.Hour), []byte("killswitch-oidc-ref")); err != nil {
		t.Fatalf("CreateOIDCSession: %v", err)
	}
	// Sanity: all three resolve BEFORE disable.
	if _, ok, _ := d.GetUserByToken(ctx, td.Token); !ok {
		t.Fatal("bearer token should resolve before disable")
	}
	if _, ok, _ := d.GetUserByRefreshToken(ctx, td.RefreshToken); !ok {
		t.Fatal("refresh token should resolve before disable")
	}
	if _, ok, _ := d.GetOIDCSessionUser(ctx, sid); !ok {
		t.Fatal("oidc session should resolve before disable")
	}

	// Disable → every credential revoked in one tx.
	if ok, err := d.SetUserDisabled(ctx, name, true); err != nil || !ok {
		t.Fatalf("SetUserDisabled(true) = %v, %v; want true, nil", ok, err)
	}

	if _, ok, _ := d.GetUserByToken(ctx, td.Token); ok {
		t.Error("bearer token STILL resolves after disable — kill switch leaked a live credential")
	}
	if _, ok, _ := d.GetUserByRefreshToken(ctx, td.RefreshToken); ok {
		t.Error("refresh token STILL resolves after disable")
	}
	if _, ok, _ := d.GetOIDCSessionUser(ctx, sid); ok {
		t.Error("oidc session STILL resolves after disable")
	}
	// The disabled_at flag is set too.
	if got, _ := d.GetUserFullByName(ctx, name); got == nil || got.DisabledAt == nil {
		t.Error("disabled_at not set after disable")
	}
}

func TestUserAdmin_RoleAndDisableLifecycle(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	const name = "roletest_gaka0oe1"
	// Clean slate (the test DB persists across runs).
	del := func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name) }
	del()
	t.Cleanup(del)

	if ok, err := d.InsertUser(ctx, StoredUser{
		Username: name, HashedPassword: []byte{1}, SaltUsed: []byte{1}, ArgonVersion: 2,
	}); err != nil || !ok {
		t.Fatalf("InsertUser(%s) = %v, %v; want true, nil", name, ok, err)
	}

	// Fresh row lands at the migration defaults: role='full', disabled_at=NULL.
	full, err := d.GetUserFullByName(ctx, name)
	if err != nil || full == nil {
		t.Fatalf("GetUserFullByName = %v, %v", full, err)
	}
	if full.Role != "full" {
		t.Errorf("default role = %q, want full", full.Role)
	}
	if full.DisabledAt != nil {
		t.Errorf("fresh user disabled_at = %v, want nil", full.DisabledAt)
	}

	// set-role → light.
	if ok, err := d.SetUserRole(ctx, name, "light"); err != nil || !ok {
		t.Fatalf("SetUserRole = %v, %v; want true, nil", ok, err)
	}
	if got, _ := d.GetUserFullByName(ctx, name); got.Role != "light" {
		t.Errorf("role after set-role = %q, want light", got.Role)
	}

	// set-role on a missing user → (false, nil), not an error.
	if ok, err := d.SetUserRole(ctx, "ghost_gaka0oe1", "light"); err != nil || ok {
		t.Errorf("SetUserRole(ghost) = %v, %v; want false, nil", ok, err)
	}

	// disable → disabled_at set.
	if ok, err := d.SetUserDisabled(ctx, name, true); err != nil || !ok {
		t.Fatalf("SetUserDisabled(true) = %v, %v; want true, nil", ok, err)
	}
	if got, _ := d.GetUserFullByName(ctx, name); got.DisabledAt == nil {
		t.Error("disabled_at is nil after disable")
	}

	// enable → disabled_at cleared.
	if ok, err := d.SetUserDisabled(ctx, name, false); err != nil || !ok {
		t.Fatalf("SetUserDisabled(false) = %v, %v; want true, nil", ok, err)
	}
	if got, _ := d.GetUserFullByName(ctx, name); got.DisabledAt != nil {
		t.Errorf("disabled_at = %v after enable, want nil", got.DisabledAt)
	}

	// disable on a missing user → (false, nil).
	if ok, err := d.SetUserDisabled(ctx, "ghost_gaka0oe1", true); err != nil || ok {
		t.Errorf("SetUserDisabled(ghost) = %v, %v; want false, nil", ok, err)
	}

	// list surfaces the row with its current role.
	rows, err := d.ListUsersAdmin(ctx)
	if err != nil {
		t.Fatalf("ListUsersAdmin: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.Username == name {
			found = true
			if r.Role != "light" {
				t.Errorf("listed role = %q, want light", r.Role)
			}
		}
	}
	if !found {
		t.Errorf("ListUsersAdmin did not include %q", name)
	}
}
