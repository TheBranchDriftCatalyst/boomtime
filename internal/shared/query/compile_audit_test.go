// compile_audit_test.go — regression tests for the query-DSL findings in the
// 2026-09-06 audit (api-plumbing lows). Each spec pins the SPECIFIC failure the
// audit described, not adjacent coverage:
//
//	compile.go:127 — a one-sided `between` compiled into an always-false date
//	                 filter (silent empty 200); a non-UTC end offset truncated
//	                 the inclusive upper bound by up to a day.
//	compile.go:190 — rows mode silently dropped sort/limit/having/bucket/rollups.
//	compile.go:336 — ilike did not escape LIKE metacharacters, so '_' and '%' in
//	                 a search value over-matched.
//
// The range/rows-mode specs are pure compile-time (no DB); the ilike + UTC-bound
// specs are DB-backed because the whole claim is about which ROWS come back.
package query_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/query"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// A one-sided between window used to reach Postgres as
// `date >= $start AND date < 0001-01-02` and return a silent empty result.
// It must be a compile error (→ 400) instead.
func TestRange_OneSidedBetweenIsRejected(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		rng  query.Range
	}{
		{"end omitted (the audit's typo'd spec)", query.Between(start, time.Time{})},
		{"start omitted", query.Between(time.Time{}, end)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// aggregate path
			if _, _, err := query.Compile("alice",
				query.Q("reading").Measure("books").Over(query.GranNone, tc.rng)); err == nil {
				t.Errorf("aggregate: Compile accepted a one-sided between window — it compiles to an always-false date filter and returns a silent empty 200")
			} else if !strings.Contains(err.Error(), "BOTH start and end") {
				t.Errorf("aggregate: err = %q, want the one-sided-window message", err)
			}
			// rows path (compiled by a different function — the audit noted the
			// range code is duplicated there)
			if _, _, err := query.Compile("alice",
				query.Q("reading").Rows().Over(query.GranNone, tc.rng)); err == nil {
				t.Errorf("rows: Compile accepted a one-sided between window")
			}
		})
	}
}

// A reversed window is the same class of client typo and equally silent
// (matches nothing) — reject it rather than answer 200/empty.
func TestRange_ReversedBetweenIsRejected(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, _, err := query.Compile("alice",
		query.Q("reading").Measure("books").Over(query.GranNone, query.Between(start, end))); err == nil {
		t.Fatal("Compile accepted a between window whose end precedes its start")
	}
}

// A well-formed window still compiles — the new validation must not have made
// the ordinary case an error.
func TestRange_WellFormedBetweenStillCompiles(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if _, _, err := query.Compile("alice",
		query.Q("reading").Measure("books").Over(query.GranNone, query.Between(start, end))); err != nil {
		t.Fatalf("well-formed between window rejected: %v", err)
	}
	// lastN is untouched by the between rules.
	if _, _, err := query.Compile("alice",
		query.Q("reading").Measure("books").Over(query.GranDay, query.LastN(7))); err != nil {
		t.Fatalf("lastN range rejected: %v", err)
	}
}

// Rows mode reads only range/where/page. Every other aggregate-path field used
// to be dropped in silence, so a client asking for sorted, capped leaf rows got
// a 200 that was neither. `measure` is exempt ON PURPOSE — both books explorer
// configs send it with rows:true because their spec type requires it.
func TestRowsMode_RejectsSilentlyIgnoredFields(t *testing.T) {
	cases := []struct {
		name string
		q    *query.Query
		want string
	}{
		{"sort", query.Q("reading").Rows().Sort("title", false), "does not support sort"},
		{"limit", query.Q("reading").Rows().Limit(10), "does not support limit"},
		{"having", query.Q("reading").Rows().Having(query.HavingCond{Op: ">=", Value: 1}), "having"},
		{"bucket", query.Q("reading").Rows().Bucket(query.BucketPolicy{TopN: 5}), "bucket"},
		{"rollups", query.Q("reading").Rows().Rollups("runtime"), "rollups"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := query.Compile("alice", tc.q)
			if err == nil {
				t.Fatalf("rows mode silently accepted (and then ignored) %s — the caller gets a 200 that does not honour it", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// Guard the deliberate exemption: the books explorers send measure alongside
// rows:true ("ignored in rows mode, but the spec type requires it"), so
// rejecting it would 400 every leaf page in the library.
func TestRowsMode_MeasureStaysAccepted(t *testing.T) {
	if _, _, err := query.Compile("alice",
		query.Q("reading").Measure("books").Rows().Page(1, 50)); err != nil {
		t.Fatalf("rows mode rejected a spec carrying measure — this is what the books explorers send: %v", err)
	}
}

// ilike is documented as "contains this literal substring". '_' and '%' are LIKE
// metacharacters, so an unescaped value turned the search into a wildcard match.
func TestReading_ILikeEscapesLikeMetacharacters(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_ilike_meta")
	fin := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Underscore: "catalyst_ui" is the literal the user typed; "catalyst-ui" and
	// "catalystXui" differ only at the position '_' would wildcard over.
	seedItem(t, hz, owner, "U1", "catalyst_ui", "read", "S", 10, &fin)
	seedItem(t, hz, owner, "U2", "catalyst-ui", "read", "S", 10, &fin)
	seedItem(t, hz, owner, "U3", "catalystXui", "read", "S", 10, &fin)
	// Percent: "50%" must not match "50 shades" / "500 miles".
	seedItem(t, hz, owner, "P1", "50% Faster", "read", "S", 10, &fin)
	seedItem(t, hz, owner, "P2", "500 Miles", "read", "S", 10, &fin)

	titles := func(t *testing.T, needle string) []string {
		t.Helper()
		res, err := query.Run(context.Background(), hz.DB.Pool, owner,
			query.Q("reading").Rows().
				Where(query.Leaf("title", query.OpILike, needle)).Page(1, 50))
		if err != nil {
			t.Fatalf("run ilike %q: %v", needle, err)
		}
		out := make([]string, 0, len(res.Rows))
		for _, r := range res.Rows {
			out = append(out, r["title"].(string))
		}
		return out
	}

	got := titles(t, "catalyst_ui")
	if len(got) != 1 || got[0] != "catalyst_ui" {
		t.Errorf("ilike \"catalyst_ui\" = %v, want exactly [catalyst_ui] — '_' was treated as a single-character wildcard", got)
	}

	got = titles(t, "50%")
	if len(got) != 1 || got[0] != "50% Faster" {
		t.Errorf("ilike \"50%%\" = %v, want exactly [50%% Faster] — '%%' was treated as an any-run wildcard", got)
	}

	// A backslash in the needle is a literal too (and must not corrupt the
	// pattern by escaping the character that follows it).
	if got := titles(t, `catalyst\`); len(got) != 0 {
		t.Errorf(`ilike "catalyst\\" = %v, want empty (no seeded title contains a backslash)`, got)
	}

	// Plain substrings are unaffected by the escaping.
	if got := titles(t, "catalyst"); len(got) != 3 {
		t.Errorf("ilike \"catalyst\" = %v, want all 3 catalyst rows (escaping must not narrow ordinary searches)", got)
	}
}

// The inclusive upper bound is widened by a whole day, so it must be computed
// from the UTC instant — not from the Y/M/D the client's offset happened to
// render. end="2026-01-15T23:00:00-05:00" IS 2026-01-16T04:00Z, so a row
// finished at 02:00Z on Jan 16 is inside the window the caller asked for.
func TestRange_EndBoundUsesUTCInstantNotOffsetLocalDate(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_range_tz")

	inWindow := time.Date(2026, 1, 16, 2, 0, 0, 0, time.UTC)
	seedItem(t, hz, owner, "T1", "EarlyJan16", "read", "S", 10, &inWindow)

	minusFive := time.FixedZone("UTC-5", -5*60*60)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 15, 23, 0, 0, 0, minusFive) // == 2026-01-16T04:00Z

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().
			Over(query.GranNone, query.Between(start, end)).Page(1, 50))
	if err != nil {
		t.Fatalf("run between: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("total = %d, want 1 — a row finished at 2026-01-16T02:00Z is INSIDE [start, 2026-01-16T04:00Z] but the offset-local date (Jan 15) cut the bound at Jan 16 00:00Z", res.Total)
	}
}
