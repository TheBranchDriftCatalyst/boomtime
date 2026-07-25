package db

import (
	"bytes"
	"context"
	"testing"
)

// TestLabelImages_Roundtrip: save an image, read it back with matching bytes,
// mime, and provenance. Non-tautological: exercises the full INSERT + SELECT
// path (including the NULLIF empty-string coercions on model/prompt).
func TestLabelImages_Roundtrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	id := "test-late-night-coder"
	t.Cleanup(func() { _ = d.DeleteLabelImage(ctx, id) })

	img := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	var seed int64 = 12345
	if err := d.SaveLabelImage(ctx, id, img, "image/png", "flux_schnell_fast", "a distinctive emblem", &seed); err != nil {
		t.Fatalf("SaveLabelImage: %v", err)
	}

	got, ok, err := d.GetLabelImage(ctx, id)
	if err != nil {
		t.Fatalf("GetLabelImage: %v", err)
	}
	if !ok {
		t.Fatal("GetLabelImage: expected row, got miss")
	}
	if !bytes.Equal(got.ImageBytes, img) {
		t.Errorf("bytes mismatch: got %d bytes want %d bytes", len(got.ImageBytes), len(img))
	}
	if got.MimeType != "image/png" {
		t.Errorf("MimeType=%q want image/png", got.MimeType)
	}
	if got.Model != "flux_schnell_fast" {
		t.Errorf("Model=%q", got.Model)
	}
	if got.Prompt != "a distinctive emblem" {
		t.Errorf("Prompt=%q", got.Prompt)
	}
	if got.Seed == nil || *got.Seed != 12345 {
		t.Errorf("Seed=%v want 12345", got.Seed)
	}
	if got.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero — the DEFAULT now() didn't fire")
	}
}

// TestLabelImages_Upsert: saving twice for the same id overwrites the row and
// bumps generated_at forward. Non-tautological: the second save uses different
// bytes + model + no seed, and we verify the row reflects the second save.
func TestLabelImages_Upsert(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	id := "test-upsert"
	t.Cleanup(func() { _ = d.DeleteLabelImage(ctx, id) })

	if err := d.SaveLabelImage(ctx, id, []byte("v1"), "image/png", "flux_schnell_fast", "prompt v1", nil); err != nil {
		t.Fatalf("first SaveLabelImage: %v", err)
	}
	first, _, err := d.GetLabelImage(ctx, id)
	if err != nil {
		t.Fatalf("first GetLabelImage: %v", err)
	}

	// Second save with different content; MUST overwrite, MUST bump generated_at.
	if err := d.SaveLabelImage(ctx, id, []byte("v2-different-content"), "image/webp", "sdxl_illustrious_xl", "prompt v2", nil); err != nil {
		t.Fatalf("second SaveLabelImage: %v", err)
	}
	second, _, err := d.GetLabelImage(ctx, id)
	if err != nil {
		t.Fatalf("second GetLabelImage: %v", err)
	}
	if string(second.ImageBytes) != "v2-different-content" {
		t.Errorf("bytes not overwritten; got %q", string(second.ImageBytes))
	}
	if second.MimeType != "image/webp" {
		t.Errorf("mime not overwritten; got %q", second.MimeType)
	}
	if second.Model != "sdxl_illustrious_xl" {
		t.Errorf("model not overwritten; got %q", second.Model)
	}
	if !second.GeneratedAt.After(first.GeneratedAt) && !second.GeneratedAt.Equal(first.GeneratedAt) {
		// generated_at may be equal on a very fast test; the point is it never
		// went backwards.
		t.Errorf("generated_at regressed: first=%v second=%v", first.GeneratedAt, second.GeneratedAt)
	}
}

// TestLabelImages_NotFound: GET on a missing id returns (nil, false, nil) so
// the handler can render a 404 without an internal-error branch.
func TestLabelImages_NotFound(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	li, ok, err := d.GetLabelImage(ctx, "does-not-exist-xyz")
	if err != nil {
		t.Fatalf("GetLabelImage: %v", err)
	}
	if ok || li != nil {
		t.Errorf("expected miss for unknown id; got ok=%v li=%+v", ok, li)
	}
}

// TestLabelImages_HasLabelImage: HasLabelImage returns true after Save, false
// after Delete. The worker uses this to skip labels that already have a row.
func TestLabelImages_HasLabelImage(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	id := "test-has"
	t.Cleanup(func() { _ = d.DeleteLabelImage(ctx, id) })

	has, err := d.HasLabelImage(ctx, id)
	if err != nil {
		t.Fatalf("HasLabelImage pre: %v", err)
	}
	if has {
		t.Fatal("HasLabelImage true before Save")
	}

	if err := d.SaveLabelImage(ctx, id, []byte("x"), "image/png", "", "", nil); err != nil {
		t.Fatalf("Save: %v", err)
	}
	has, err = d.HasLabelImage(ctx, id)
	if err != nil {
		t.Fatalf("HasLabelImage post-save: %v", err)
	}
	if !has {
		t.Error("HasLabelImage false after Save")
	}

	if err := d.DeleteLabelImage(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	has, err = d.HasLabelImage(ctx, id)
	if err != nil {
		t.Fatalf("HasLabelImage post-delete: %v", err)
	}
	if has {
		t.Error("HasLabelImage true after Delete")
	}
}
