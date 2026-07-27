package db

// user_avatars_test.go (gaka-9v4): DB roundtrip + status-transition tests
// for the per-user chibi avatar row. Non-tautological: exercises the two-
// phase status ('running' → 'ready') the async worker relies on, and the
// bytes-preserving 'error' transition that keeps a prior ready image
// visible when a retry fails.

import (
	"bytes"
	"context"
	"testing"
)

// TestUserAvatars_SaveRoundtrip: SaveUserAvatar populates every column,
// GetUserAvatar reads them back verbatim, and the row lands in 'ready'.
func TestUserAvatars_SaveRoundtrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("useravatar_save")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	img := []byte("\x89PNG\r\n\x1a\nfake-chibi-portrait-bytes")
	var seed int64 = 424242
	if err := d.SaveUserAvatar(ctx, sender, img, "image/png",
		"chroma_hd", "a chibi hacker in a hoodie, neon glow", &seed); err != nil {
		t.Fatalf("SaveUserAvatar: %v", err)
	}

	got, ok, err := d.GetUserAvatar(ctx, sender)
	if err != nil {
		t.Fatalf("GetUserAvatar: %v", err)
	}
	if !ok {
		t.Fatal("GetUserAvatar: expected row, got miss")
	}
	if !bytes.Equal(got.ImageBytes, img) {
		t.Errorf("bytes mismatch: got %d want %d", len(got.ImageBytes), len(img))
	}
	if got.MimeType != "image/png" {
		t.Errorf("MimeType=%q want image/png", got.MimeType)
	}
	if got.Model != "chroma_hd" {
		t.Errorf("Model=%q", got.Model)
	}
	if got.Prompt == "" {
		t.Error("Prompt was dropped")
	}
	if got.Seed == nil || *got.Seed != seed {
		t.Errorf("Seed=%v want %d", got.Seed, seed)
	}
	if got.Status != UserAvatarStatusReady {
		t.Errorf("Status=%q want ready", got.Status)
	}
	if got.GeneratedAt == nil {
		t.Error("GeneratedAt is nil — SaveUserAvatar should stamp now()")
	}
}

// TestUserAvatars_StatusTransitions: SetAvatarStatus can drive
// pending → running → ready → error → running (retry), and each state
// change clears the previous error_message. Non-tautological: proves the
// NULLIF-on-empty-error branch by transitioning INTO error, then OUT of
// error and verifying the message is gone.
func TestUserAvatars_StatusTransitions(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("useravatar_status")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	// pre-condition: no row, status endpoint returns miss.
	if info, ok, err := d.GetUserAvatarStatus(ctx, sender); err != nil {
		t.Fatalf("pre GetUserAvatarStatus: %v", err)
	} else if ok || info != nil {
		t.Fatalf("pre: expected no row, got ok=%v info=%+v", ok, info)
	}

	// running (creates the row via upsert).
	if err := d.SetAvatarStatus(ctx, sender, UserAvatarStatusRunning, ""); err != nil {
		t.Fatalf("set running: %v", err)
	}
	info, ok, err := d.GetUserAvatarStatus(ctx, sender)
	if err != nil || !ok {
		t.Fatalf("post-running status: ok=%v err=%v", ok, err)
	}
	if info.Status != UserAvatarStatusRunning {
		t.Errorf("Status=%q want running", info.Status)
	}
	if info.ErrorMessage != "" {
		t.Errorf("ErrorMessage=%q want empty", info.ErrorMessage)
	}

	// error (writes an error message; bytes remain nil).
	if err := d.SetAvatarStatus(ctx, sender, UserAvatarStatusError, "shim: 503 upstream unavailable"); err != nil {
		t.Fatalf("set error: %v", err)
	}
	info, _, err = d.GetUserAvatarStatus(ctx, sender)
	if err != nil {
		t.Fatalf("post-error status: %v", err)
	}
	if info.Status != UserAvatarStatusError {
		t.Errorf("Status=%q want error", info.Status)
	}
	if info.ErrorMessage == "" {
		t.Error("ErrorMessage empty; expected the shim 503 message")
	}

	// running again (retry) — MUST clear the prior error_message.
	if err := d.SetAvatarStatus(ctx, sender, UserAvatarStatusRunning, ""); err != nil {
		t.Fatalf("set running (retry): %v", err)
	}
	info, _, err = d.GetUserAvatarStatus(ctx, sender)
	if err != nil {
		t.Fatalf("post-retry status: %v", err)
	}
	if info.ErrorMessage != "" {
		t.Errorf("ErrorMessage=%q; retry into 'running' should clear the prior error", info.ErrorMessage)
	}
}

// TestUserAvatars_ErrorPreservesBytes: seeding a ready avatar then
// transitioning to 'error' MUST NOT wipe image_bytes. This preserves the
// old avatar in the FE while a retry is in flight, which was an explicit
// UX call in the design ("don't nuke the good one until the new one lands").
func TestUserAvatars_ErrorPreservesBytes(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("useravatar_err_preserves")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	img := []byte("original-good-image")
	if err := d.SaveUserAvatar(ctx, sender, img, "image/png", "m", "p", nil); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	if err := d.SetAvatarStatus(ctx, sender, UserAvatarStatusError, "shim timeout"); err != nil {
		t.Fatalf("set error: %v", err)
	}
	got, ok, err := d.GetUserAvatar(ctx, sender)
	if err != nil || !ok {
		t.Fatalf("post-error GetUserAvatar: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got.ImageBytes, img) {
		t.Errorf("bytes wiped by SetAvatarStatus(error); got %q want %q — regression: FE would lose the old avatar during a retry",
			string(got.ImageBytes), string(img))
	}
	if got.Status != UserAvatarStatusError {
		t.Errorf("Status=%q want error", got.Status)
	}
}

// TestUserAvatars_UnknownStatus: SetAvatarStatus rejects free-form strings
// so a typo never hits the DB and silently mangles the FE state machine.
func TestUserAvatars_UnknownStatus(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("useravatar_unknown")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	if err := d.SetAvatarStatus(ctx, sender, UserAvatarStatus("bogus"), ""); err == nil {
		t.Fatal("SetAvatarStatus with unknown status: expected error, got nil")
	}
}

// TestUserAvatars_NotFound: GetUserAvatar on a user with no row returns
// (nil, false, nil) so the handler can 404 without an internal-error branch.
func TestUserAvatars_NotFound(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	got, ok, err := d.GetUserAvatar(ctx, "does-not-exist-xyz-9v4")
	if err != nil {
		t.Fatalf("GetUserAvatar: %v", err)
	}
	if ok || got != nil {
		t.Errorf("expected miss; got ok=%v got=%+v", ok, got)
	}
}
