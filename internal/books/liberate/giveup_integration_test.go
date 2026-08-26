// giveup_integration_test.go — DB-backed coverage of the sweep's GIVE-UP rule.
//
// This logic lives entirely in SQL (a counter column, two reset paths, and two
// independent exclusions in ListUnliberated), so a stubbed store would prove
// nothing at all. Reuses the isolated-database harness in
// service_integration_test.go.
//
// What it protects, concretely: three podcasts sat in the retryable `failed`
// status and were re-licensed from Amazon on EVERY sweep, indefinitely, because
// nothing bounded the retries and nothing surfaced them.
package liberate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
)

// attemptsOf reads the consecutive-failure counter straight from the row.
func attemptsOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, asin string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT liberation_attempts FROM public.reading_items WHERE owner=$1 AND external_id=$2`,
		testOwner, asin).Scan(&n)
	if err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	return n
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// The counter must track CONSECUTIVE failures: each failure increments, and a
// success resets. Without the reset a title that fails twice, succeeds, then
// fails once more would be one failure away from a give-up it never earned.
func TestAttemptsCounterIncrementsAndResets(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	store := &liberate.Store{Pool: pool}

	if got := attemptsOf(t, ctx, pool, testASIN); got != 0 {
		t.Fatalf("fresh row attempts = %d, want 0", got)
	}

	for i := 1; i <= 2; i++ {
		if err := store.MarkFailed(ctx, testOwner, testASIN, liberate.StatusFailed, "boom", ""); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		if got := attemptsOf(t, ctx, pool, testASIN); got != i {
			t.Fatalf("after %d failures attempts = %d, want %d", i, got, i)
		}
	}

	if err := store.MarkLiberated(ctx, testOwner, testASIN, "a/b.m4b", 123, "AAX_44_128"); err != nil {
		t.Fatalf("MarkLiberated: %v", err)
	}
	if got := attemptsOf(t, ctx, pool, testASIN); got != 0 {
		t.Fatalf("success left attempts = %d, want 0 — the counter is not CONSECUTIVE", got)
	}
}

// The sweep must stop after MaxAutoAttempts. Verified by walking right up to the
// boundary first: a test that only checks the far side would still pass if the
// comparison were off by one in the lenient direction.
func TestSweepGivesUpAtMaxAutoAttempts(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	store := &liberate.Store{Pool: pool}

	for i := 1; i < liberate.MaxAutoAttempts; i++ {
		if err := store.MarkFailed(ctx, testOwner, testASIN, liberate.StatusFailed, "transient", ""); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		pending, err := store.ListUnliberated(ctx, testOwner, 0)
		if err != nil {
			t.Fatalf("ListUnliberated: %v", err)
		}
		if !contains(pending, testASIN) {
			t.Fatalf("after %d/%d failures the sweep already dropped the title — retries are meant to be tolerated up to the cap",
				i, liberate.MaxAutoAttempts)
		}
	}

	// The failure that reaches the cap.
	if err := store.MarkFailed(ctx, testOwner, testASIN, liberate.StatusFailed, "transient", ""); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	pending, err := store.ListUnliberated(ctx, testOwner, 0)
	if err != nil {
		t.Fatalf("ListUnliberated: %v", err)
	}
	if contains(pending, testASIN) {
		t.Fatalf("the sweep still returns the title after %d consecutive failures — it would retry forever",
			liberate.MaxAutoAttempts)
	}
}

// Terminal statuses are a verdict about the TITLE and must be excluded on the
// very first occurrence, independent of the counter. The podcast case: one 400
// from Amazon is all the evidence there will ever be.
func TestTerminalStatusExcludedImmediately(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	store := &liberate.Store{Pool: pool}

	for _, status := range []string{
		liberate.StatusUnsupportedFormat,
		liberate.StatusDenied,
		liberate.StatusUnsupportedCodec,
		liberate.StatusSkipped,
	} {
		// Reset to a clean, sweep-eligible row each time.
		if _, err := store.ClearLiberation(ctx, testOwner, testASIN); err != nil {
			t.Fatalf("ClearLiberation: %v", err)
		}
		if err := store.MarkFailed(ctx, testOwner, testASIN, status, "terminal reason", ""); err != nil {
			t.Fatalf("MarkFailed(%s): %v", status, err)
		}
		pending, err := store.ListUnliberated(ctx, testOwner, 0)
		if err != nil {
			t.Fatalf("ListUnliberated: %v", err)
		}
		if contains(pending, testASIN) {
			t.Fatalf("status %q is terminal but the sweep still queued it after ONE failure", status)
		}
	}
}

// "Retry" has to actually restore eligibility. If ClearLiberation did not reset
// the counter, a retried title would run once and then be dropped by every
// subsequent sweep — the button would look like it worked and quietly not.
func TestClearLiberationRestoresSweepEligibility(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	store := &liberate.Store{Pool: pool}

	for i := 0; i < liberate.MaxAutoAttempts; i++ {
		if err := store.MarkFailed(ctx, testOwner, testASIN, liberate.StatusFailed, "nope", ""); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
	}
	pending, _ := store.ListUnliberated(ctx, testOwner, 0)
	if contains(pending, testASIN) {
		t.Fatal("precondition failed: the title should already be given up on")
	}

	if _, err := store.ClearLiberation(ctx, testOwner, testASIN); err != nil {
		t.Fatalf("ClearLiberation: %v", err)
	}
	if got := attemptsOf(t, ctx, pool, testASIN); got != 0 {
		t.Fatalf("after retry attempts = %d, want 0", got)
	}
	pending, err := store.ListUnliberated(ctx, testOwner, 0)
	if err != nil {
		t.Fatalf("ListUnliberated: %v", err)
	}
	if !contains(pending, testASIN) {
		t.Fatal("retry did not restore sweep eligibility — the title is stranded")
	}
}

// ListExcluded must report BOTH exclusion classes and label them correctly:
// exhausted attempts are worth retrying, a terminal verdict is not. Getting
// Retryable backwards would send the user to press a button that re-earns the
// same refusal from Amazon.
func TestListExcludedSeparatesTerminalFromExhausted(t *testing.T) {
	ctx := context.Background()
	pool := provisionDB(t, ctx)
	seedItem(t, ctx, pool)
	store := &liberate.Store{Pool: pool}

	// A second title to hold the other exclusion class.
	const otherASIN = "B08K56VS1G"
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.reading_items (owner, source, external_id, title, authors)
		VALUES ($1, 'audible', $2, 'Some Podcast', 'A Host')`, testOwner, otherASIN); err != nil {
		t.Fatalf("seed second item: %v", err)
	}

	// testASIN: exhausted retries, no verdict.
	for i := 0; i < liberate.MaxAutoAttempts; i++ {
		if err := store.MarkFailed(ctx, testOwner, testASIN, liberate.StatusFailed, "flaky cdn", ""); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
	}
	// otherASIN: a terminal verdict on the first try.
	if err := store.MarkFailed(ctx, testOwner, otherASIN, liberate.StatusUnsupportedFormat,
		`HTTP 400: {"message":"Requested asin is a non_audio asset with contentDeliveryType:PodcastParent"}`, ""); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	got, err := store.ListExcluded(ctx, testOwner)
	if err != nil {
		t.Fatalf("ListExcluded: %v", err)
	}
	byASIN := map[string]liberate.ExcludedItem{}
	for _, it := range got {
		byASIN[it.ASIN] = it
	}
	if len(byASIN) != 2 {
		t.Fatalf("ListExcluded returned %d titles, want 2: %+v", len(byASIN), got)
	}

	exhausted, ok := byASIN[testASIN]
	if !ok {
		t.Fatal("the retry-exhausted title is missing from the excluded set — it would be invisible AND unswept")
	}
	if !exhausted.Retryable {
		t.Error("a title excluded only by attempt count must be Retryable=true")
	}
	if exhausted.Attempts != liberate.MaxAutoAttempts {
		t.Errorf("attempts = %d, want %d", exhausted.Attempts, liberate.MaxAutoAttempts)
	}

	terminal, ok := byASIN[otherASIN]
	if !ok {
		t.Fatal("the terminal title is missing from the excluded set")
	}
	if terminal.Retryable {
		t.Error("a terminal verdict must be Retryable=false — retrying re-earns the same refusal")
	}
	if terminal.Title != "Some Podcast" {
		t.Errorf("title = %q, want %q — the UI cannot identify the book without it", terminal.Title, "Some Podcast")
	}
	if terminal.Error == "" {
		t.Error("the terminal reason is empty — 'why did it give up' is the whole point of this list")
	}
}
