// audit_regression_test.go — regression pins for the hardcover-side findings of the
// 2026-09-06 audit (epic boom-l827) that live in this package:
//
//   - match_sweep.go:336 — the shelf rung promoted TITLE-ONLY fuzzy resolutions into
//     the cross-user global hardcover_match_cache, so one user's shelf title
//     collision permanently mislinked that ASIN for every other user.
//   - curation_push.go:60 — DeleteHardcoverRead reported success under dry-run, so
//     the API told the user the read had been pruned on Hardcover when it had not.
//
// The DB-backed cases reuse match_sweep_test.go's isolated-harness helpers.
package hardcover

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// withThrowawayEncryptionKey installs a per-test BOOM_ENCRYPTION_KEY (and resets the
// memoized AEAD) so the token Store's Save/Load round-trips — the same trick the
// kindle ingest harness uses for the Amazon credential.
func withThrowawayEncryptionKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	t.Setenv("BOOM_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)
}

// TestMatchWith_TitleOnlyShelfMatchIsNotGloballyCached is the audit's exact
// scenario: the owner has shelved "Endurance" (Lansing) on Hardcover, and their
// unmatched Audible row is a DIFFERENT book that happens to share the title. Title
// Jaccard is 1.0 — above the 0.75 shelf floor with no runner-up — so the row links,
// but the resolution is title-only: no author corroborated it. Promoting that into
// the global cache (keyed by ASIN, shared by every user) would serve the wrong
// book_id to everyone else with that ASIN, forever, with zero API calls.
//
// Contrast TestMatchWith_ShelfMatchAcceptsStrong, where title AND author agree and
// the promotion is still expected — the gate is corroboration, not "never cache".
func TestMatchWith_TitleOnlyShelfMatchIsNotGloballyCached(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("sweep_shelfpoison_%d", time.Now().UnixNano())
	asin := fmt.Sprintf("B0POISON%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id=$1`, asin)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_shelf_entries WHERE owner=$1`, owner)
	})

	// Our row: a different "Endurance", by a different author.
	mustUpsert(t, d, ctx, db.ReadingItem{
		Owner: owner, Source: "audible", ExternalID: asin, AmazonASIN: asin,
		Title: "Endurance", Authors: "Scott Kelly",
	})
	// Their shelf: Lansing's "Endurance".
	if err := d.UpsertHardcoverShelfEntry(ctx, owner, db.ShelfEntry{
		BookID: 901, Title: "Endurance", Author: "Alfred Lansing", Slug: "endurance", Status: "read",
	}, nil); err != nil {
		t.Fatalf("seed shelf: %v", err)
	}

	fake := &fakeMatcher{hits: map[string]MatchResult{}} // no exact-id / fuzzy hit
	svc := NewSyncService(d, NewStore(d), nil)
	res, err := svc.matchWith(ctx, owner, fake, false)
	if err != nil {
		t.Fatalf("matchWith: %v", err)
	}

	// The per-user link still happens — the shelf rung is not disabled, only its
	// global promotion is gated.
	if res.ShelfHits != 1 || res.Matched != 1 {
		t.Fatalf("ShelfHits=%d Matched=%d, want 1/1 (the per-user shelf link must survive)",
			res.ShelfHits, res.Matched)
	}

	if _, ok, lerr := d.LookupHardcoverMatch(ctx, "asin", asin); lerr != nil {
		t.Fatalf("cache lookup: %v", lerr)
	} else if ok {
		t.Fatalf("a title-only shelf match was promoted into the cross-user global match "+
			"cache for asin %s — every other user with that ASIN now resolves to this "+
			"owner's shelf book", asin)
	}
}

// TestShelfAuthorCorroborated pins the gate predicate itself, including the
// blank-author cases the audit scenario depends on (a row with no author can never
// corroborate, so it can never be promoted).
func TestShelfAuthorCorroborated(t *testing.T) {
	cases := []struct {
		name       string
		rowAuthor  string
		shelfAutho string
		want       bool
	}{
		{"identical", "Andy Weir", "Andy Weir", true},
		{"one shared token of two", "Andy Weir", "Weir, Andy", true},
		{"different authors", "Scott Kelly", "Alfred Lansing", false},
		{"row has no author", "", "Alfred Lansing", false},
		{"shelf entry has no author", "Alfred Lansing", "", false},
		{"both blank", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shelfAuthorCorroborated(
				db.ReadingItem{Authors: c.rowAuthor},
				db.ShelfEntry{Author: c.shelfAutho})
			if got != c.want {
				t.Fatalf("shelfAuthorCorroborated(%q, %q) = %v, want %v",
					c.rowAuthor, c.shelfAutho, got, c.want)
			}
		})
	}
}

// TestDeleteHardcoverRead_DryRunReportsNotDeleted pins the honesty fix: under the
// default BOOM_HARDCOVER_DRYRUN the remote read is NOT deleted, so the call must not
// report success. The API handler's "err != nil → hardcoverDeleted=false" branch is
// what turns this into a truthful response body, so returning ErrDryRun (rather than
// nil) is the whole fix — with nil, the user was told the read was pruned on
// Hardcover and then watched it re-appear on the next pull.
func TestDeleteHardcoverRead_DryRunReportsNotDeleted(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("hc_delread_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	withThrowawayEncryptionKey(t)
	store := NewStore(d)
	if err := store.Save(ctx, owner, "test-token", db.HardcoverKeyStatusValid); err != nil {
		t.Fatalf("seal token: %v", err)
	}
	svc := NewPushService(d, store, nil)

	// NewClient inherits the process-wide dry-run default, which is fail-safe ON.
	if !NewClient("x").DryRun() {
		t.Skip("dry-run default is off in this process — the gate is configured elsewhere")
	}

	err := svc.DeleteHardcoverRead(ctx, owner, 4242)
	if err == nil {
		t.Fatal("DeleteHardcoverRead reported success under dry-run — the API turns that " +
			"into hardcoverDeleted=true for a read that still exists on Hardcover")
	}
	if !errors.Is(err, ErrDryRun) {
		t.Fatalf("want ErrDryRun, got %v", err)
	}
}

// TestDeleteHardcoverRead_NoConnectionIsStillANoop guards the unchanged branches: a
// user who never connected Hardcover, and a non-id, are still silent no-ops (nil) —
// there is nothing remote to delete, so hardcoverDeleted=false is reached through the
// origin check in the handler, not through an error.
func TestDeleteHardcoverRead_NoConnectionIsStillANoop(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("hc_delread_none_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	svc := NewPushService(d, NewStore(d), nil)
	if err := svc.DeleteHardcoverRead(ctx, owner, 0); err != nil {
		t.Fatalf("readID 0 must be a no-op, got %v", err)
	}
	if err := svc.DeleteHardcoverRead(ctx, owner, 77); err != nil {
		t.Fatalf("unconnected user must be a no-op, got %v", err)
	}
}

// TestPushCuration_CachesFreshlyResolvedMatch pins curation_push.go:116: a row with
// no cached hardcover_book_id ran the rate-limited match ladder on EVERY curation
// push and threw the resolution away, so a user PATCHing the same book repeatedly
// re-spent the shared Hardcover budget (up to 3 throttled GraphQL calls each time)
// and the resolved link stayed invisible to the match sweep and the pull reconcile,
// both of which key on hardcover_book_id.
func TestPushCuration_CachesFreshlyResolvedMatch(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("hc_pushcache_%d", time.Now().UnixNano())
	asin := fmt.Sprintf("B0PUSHC%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() {
		cleanupOwner(d, ctx, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hardcover_match_cache WHERE external_id=$1`, asin)
	})
	withThrowawayEncryptionKey(t)

	// An UNMATCHED row (no hardcover_book_id) the user has curated to 'dnf'.
	mustUpsert(t, d, ctx, db.ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: asin, AmazonASIN: asin,
		Title: "Project Hail Mary", Authors: "Andy Weir", Status: "reading",
	})
	if _, err := d.SetReadingItemCuration(ctx, owner, "kindle", asin, db.ReadingItemCurationPatch{
		Status: strPtrHC("dnf"), SetStatus: true,
	}); err != nil {
		t.Fatalf("curate: %v", err)
	}

	rt := &fakeRoundTripper{editionByASIN: &hcEdition{ID: 8802, BookID: 556, ReadingFormatID: FormatEbook}}
	store := NewStore(d)
	store.newClient = func(string) *Client { return newFakeClient(rt) }
	if err := store.Save(ctx, owner, "test-token", db.HardcoverKeyStatusValid); err != nil {
		t.Fatalf("seal token: %v", err)
	}
	svc := NewPushService(d, store, nil)

	if err := svc.PushCuration(ctx, CurationPushPayload{Owner: owner, Source: "kindle", ExternalID: asin}); err != nil {
		t.Fatalf("first PushCuration: %v", err)
	}
	afterFirst := rt.matchQueries
	if afterFirst == 0 {
		t.Fatalf("the first push should have run the match ladder (the row was unmatched)")
	}

	it, err := d.GetReadingItem(ctx, owner, "kindle", asin)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.HardcoverBookID == nil || *it.HardcoverBookID != 556 {
		t.Fatalf("the freshly-resolved match was not cached onto the row: hardcover_book_id = %v, want 556",
			it.HardcoverBookID)
	}

	// The second push must reuse the cached link — zero further match-ladder reads.
	if err := svc.PushCuration(ctx, CurationPushPayload{Owner: owner, Source: "kindle", ExternalID: asin}); err != nil {
		t.Fatalf("second PushCuration: %v", err)
	}
	if rt.matchQueries != afterFirst {
		t.Fatalf("the second push re-ran the rate-limited match ladder (%d -> %d match reads) "+
			"— the resolution is being thrown away", afterFirst, rt.matchQueries)
	}
}

func strPtrHC(s string) *string { return &s }
