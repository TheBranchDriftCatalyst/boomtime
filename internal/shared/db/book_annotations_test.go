// book_annotations_test.go — pins the ANNOTATION CORPUS seams (boom-siwi.5).
//
// Three of these are load-bearing rather than descriptive, and each was written
// to go RED against a specific defect before the code was written:
//
//   - the two provenance tests: an Amazon re-sync must not overwrite a
//     transcript, and a transcription must not overwrite the highlighted
//     passage. The schema cannot express that; only the disjoint accessor column
//     sets can, so the tests are the contract.
//   - the tombstone guard: a fetch that returned nothing must retire nothing.
//     Amazon publishes no delete signal, so a broken parser and a user who
//     deleted every highlight produce identical evidence — and only one of those
//     should empty the corpus.
package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mkAnnotationOwner mints an isolated user for one test. The corpus is
// owner-scoped and the harness DB is shared, so every test gets its own owner
// rather than trying to clean up a shared one.
func mkAnnotationOwner(t *testing.T, d *DB, ctx context.Context) string {
	t.Helper()
	owner := fmt.Sprintf("annot_%d", time.Now().UnixNano())
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00')`,
		owner); err != nil {
		t.Fatalf("mint user: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner) })
	return owner
}

func highlight(owner, asin, key string, start int64, body string) BookAnnotation {
	captured := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	return BookAnnotation{
		Owner: owner, Source: "kindle", ExternalID: asin,
		Kind: AnnotationKindHighlight, AnnotationKey: key,
		PositionUnit: PositionUnitLocation, PositionStart: start,
		Body: body, CapturedAt: &captured,
	}
}

func TestBookAnnotations_UpsertIsIdempotentAndUpdatesInPlace(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	a := highlight(owner, "B0TEST0001", "highlight:100:140", 100, "first text")
	for i := 0; i < 3; i++ {
		if _, err := d.UpsertBookAnnotation(ctx, a); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, err := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0001")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("re-syncing the same annotation produced %d rows, want 1", len(got))
	}

	// Amazon corrected the text: same key, new body, still one row.
	a.Body = "corrected text"
	if _, err := d.UpsertBookAnnotation(ctx, a); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0001")
	if len(got) != 1 || got[0].Body != "corrected text" {
		t.Fatalf("got %d rows, body=%q; want 1 row with the corrected body", len(got), got[0].Body)
	}
}

// The provenance contract, half one: a whisper transcript survives every
// subsequent Amazon sync. Mutation that must turn this red: add
// `transcript = EXCLUDED.transcript` to UpsertBookAnnotation's ON CONFLICT.
func TestBookAnnotations_UpsertNeverClobbersTranscript(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	a := highlight(owner, "B0TEST0002", "clip:5000:9000", 5000, "")
	a.Kind = AnnotationKindClip
	a.PositionUnit = PositionUnitMillis
	if _, err := d.UpsertBookAnnotation(ctx, a); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	ok, err := d.SetBookAnnotationTranscript(ctx, owner, "kindle", "B0TEST0002", "clip:5000:9000",
		"the transcribed words", "whisper:large-v3")
	if err != nil || !ok {
		t.Fatalf("set transcript: ok=%v err=%v", ok, err)
	}

	// A later sweep re-upserts the same annotation from Amazon.
	if _, err := d.UpsertBookAnnotation(ctx, a); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	got, _ := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0002")
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].Transcript != "the transcribed words" {
		t.Fatalf("the Amazon re-sync destroyed a transcript that cost real GPU time: transcript=%q", got[0].Transcript)
	}
	if got[0].TranscriptSource != "whisper:large-v3" || got[0].TranscriptAt == nil {
		t.Fatalf("transcript provenance lost: source=%q at=%v", got[0].TranscriptSource, got[0].TranscriptAt)
	}
}

// The provenance contract, half two: a transcription never rewrites the words a
// human actually selected. Mutation: add body to SetBookAnnotationTranscript's
// SET list.
func TestBookAnnotations_SetTranscriptNeverClobbersBodyOrNote(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	a := highlight(owner, "B0TEST0003", "highlight:1:2", 1, "what the human highlighted")
	a.Note = "and what they wrote about it"
	if _, err := d.UpsertBookAnnotation(ctx, a); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := d.SetBookAnnotationTranscript(ctx, owner, "kindle", "B0TEST0003", "highlight:1:2",
		"a machine guess", "whisper:large-v3"); err != nil {
		t.Fatalf("set transcript: %v", err)
	}

	got, _ := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0003")
	if got[0].Body != "what the human highlighted" || got[0].Note != "and what they wrote about it" {
		t.Fatalf("transcription overwrote human text: body=%q note=%q", got[0].Body, got[0].Note)
	}
}

// THE corpus-safety test. A sweep whose fetch yielded nothing — the shape a
// broken HTML parser produces — must retire nothing. Mutation: drop the
// `len(seenKeys) == 0` early return in TombstoneMissingBookAnnotations.
func TestBookAnnotations_TombstoneRequiresANonEmptyFetch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	for i, key := range []string{"highlight:10:20", "highlight:30:40", "highlight:50:60"} {
		if _, err := d.UpsertBookAnnotation(ctx, highlight(owner, "B0TEST0004", key, int64(10+i*20), "text")); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// The parser broke: it understood nothing, so it saw nothing.
	//
	// BOTH empty shapes are exercised, and the second one is the whole point. A
	// sweep builds its seen-set with make([]string, 0, n), so a fetch that parsed
	// nothing hands back an EMPTY slice, not a nil one — and the two do not behave
	// alike in SQL. pgx marshals nil to NULL, and `key <> ALL(NULL)` is NULL, so
	// the nil case matches no rows even with the guard removed. The empty slice
	// marshals to '{}', and `key <> ALL('{}')` is vacuously TRUE, which retires
	// EVERY annotation for the book. Testing only nil would pass against a
	// completely unguarded implementation — verified by mutation.
	for _, seen := range [][]string{nil, {}} {
		n, err := d.TombstoneMissingBookAnnotations(ctx, owner, "kindle", "B0TEST0004", seen)
		if err != nil {
			t.Fatalf("tombstone(%#v): %v", seen, err)
		}
		if n != 0 {
			t.Fatalf("an empty fetch (%#v) retired %d annotations — a DOM change would empty the whole corpus", seen, n)
		}
		live, _ := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0004")
		if len(live) != 3 {
			t.Fatalf("want all 3 annotations intact after an empty fetch (%#v), got %d", seen, len(live))
		}
	}
}

func TestBookAnnotations_TombstonePrunesOnlyTheGenuinelyMissing(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	keys := []string{"highlight:10:20", "highlight:30:40", "highlight:50:60"}
	for i, key := range keys {
		if _, err := d.UpsertBookAnnotation(ctx, highlight(owner, "B0TEST0005", key, int64(10+i*20), "text")); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// A healthy fetch that genuinely saw only two of the three.
	n, err := d.TombstoneMissingBookAnnotations(ctx, owner, "kindle", "B0TEST0005", keys[:2])
	if err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	if n != 1 {
		t.Fatalf("tombstoned %d, want exactly the 1 missing annotation", n)
	}
	live, _ := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0005")
	if len(live) != 2 {
		t.Fatalf("want 2 live annotations, got %d", len(live))
	}

	// Amazon shows it again — a re-upsert must resurrect it rather than
	// duplicating under the same key.
	if _, err := d.UpsertBookAnnotation(ctx, highlight(owner, "B0TEST0005", keys[2], 50, "text")); err != nil {
		t.Fatalf("resurrect: %v", err)
	}
	live, _ = d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0005")
	if len(live) != 3 {
		t.Fatalf("a tombstoned annotation should come back live on re-sync, got %d rows", len(live))
	}
}

// A row with no idempotency key would duplicate on every single sync; a row with
// no declared unit is a position nobody can interpret. Both are refused rather
// than stored.
func TestBookAnnotations_UpsertRefusesUnkeyableRows(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	noKey := highlight(owner, "B0TEST0006", "", 1, "text")
	noUnit := highlight(owner, "B0TEST0006", "highlight:1:2", 1, "text")
	noUnit.PositionUnit = ""

	for name, a := range map[string]BookAnnotation{"no key": noKey, "no unit": noUnit} {
		wrote, err := d.UpsertBookAnnotation(ctx, a)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		if wrote {
			t.Fatalf("%s: should be a silent no-op, but a row was written", name)
		}
	}
	live, _ := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0006")
	if len(live) != 0 {
		t.Fatalf("want no rows, got %d", len(live))
	}
}

func TestBookAnnotations_ListIsOrderedByPositionAndCounts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	// Inserted out of order on purpose.
	for _, p := range []int64{500, 100, 300} {
		key := fmt.Sprintf("highlight:%d:%d", p, p+10)
		if _, err := d.UpsertBookAnnotation(ctx, highlight(owner, "B0TEST0007", key, p, "text")); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	note := highlight(owner, "B0TEST0007", "note:200:200", 200, "")
	note.Kind = AnnotationKindNote
	note.Note = "a note"
	if _, err := d.UpsertBookAnnotation(ctx, note); err != nil {
		t.Fatalf("seed note: %v", err)
	}

	got, err := d.ListBookAnnotations(ctx, owner, "kindle", "B0TEST0007")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var last int64 = -1
	for _, a := range got {
		if a.PositionStart < last {
			t.Fatalf("annotations came back out of reading order: %d after %d", a.PositionStart, last)
		}
		last = a.PositionStart
	}

	counts, err := d.CountBookAnnotations(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts[AnnotationKindHighlight] != 3 || counts[AnnotationKindNote] != 1 {
		t.Fatalf("counts = %v, want 3 highlights + 1 note", counts)
	}
}

func TestBookAnnotations_DeleteScopedAndCascade(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkAnnotationOwner(t, d, ctx)

	k := highlight(owner, "B0TEST0008", "highlight:1:2", 1, "kindle text")
	audible := highlight(owner, "B0TEST0009", "clip:1000:2000", 1000, "")
	audible.Source = "audible"
	audible.Kind = AnnotationKindClip
	audible.PositionUnit = PositionUnitMillis
	for _, a := range []BookAnnotation{k, audible} {
		if _, err := d.UpsertBookAnnotation(ctx, a); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Scoped delete leaves the other source alone.
	n, err := d.DeleteBookAnnotations(ctx, owner, "kindle")
	if err != nil || n != 1 {
		t.Fatalf("scoped delete: n=%d err=%v, want 1", n, err)
	}
	if counts, _ := d.CountBookAnnotations(ctx, owner, ""); counts[AnnotationKindClip] != 1 {
		t.Fatalf("the audible clip should have survived a kindle-scoped delete: %v", counts)
	}

	// Dropping the user cascades the rest away — the silo invariant.
	if _, err := d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var remaining int
	if err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM book_annotations WHERE owner = $1`, owner).Scan(&remaining); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("%d annotations outlived their user — the ON DELETE CASCADE is not doing its job", remaining)
	}
}
