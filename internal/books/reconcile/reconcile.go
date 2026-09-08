// Package reconcile is the books domain's SET-DIFF kernel: given the keys a
// source just handed us and the keys we already hold, decide what is new, what
// survives, and — the dangerous part — whether the missing ones may be retired.
//
// WHY THIS IS A PURE FUNCTION AND NOT AN ENGINE THAT OWNS THE WRITES.
//
// The valuable thing to share across sources is the DECISION, not the plumbing.
// reading_items, book_annotations and reading_events have differently shaped
// keys, scopes and delete semantics; a common Store interface would force them
// into one shape and leak each source's storage assumptions into the others.
// What they genuinely have in common is a single rule that is easy to get wrong
// and catastrophic when wrong. So this package owns exactly that rule, in a
// function with no context, no DB and no I/O — which is why it can be exhaustively
// unit-tested and why every source can adopt it without changing how it writes.
//
// THE RULE, and the incident behind it.
//
// None of the Amazon surfaces publish a delete signal. Deletion can only ever be
// INFERRED from absence — and a source that returns nothing is byte-for-byte
// indistinguishable from a user who deleted everything. The Kindle notebook is
// scraped HTML, so "returns nothing" is one DOM rename away at all times. Acting
// on that absence would retire the entire corpus while every HTTP call still
// returned 200 and every log line still said success.
//
// Hence: a fetch that yielded NOTHING retires NOTHING. Not a heuristic — a hard
// precondition, stated once here rather than re-derived per source.
//
// WHAT THIS PACKAGE DELIBERATELY DOES NOT COVER.
//
// Scalar-delta sources (kindle_reading_positions: sample a number, diff
// consecutive samples into sessions) are a DIFFERENT kind of diff. Their unit is
// temporal, not set-shaped, and their failure mode is aliasing — sampling slower
// than the source writes, so an advance is simply never observed. No amount of
// set reconciliation helps with that, and no cadence probe helps with
// absence-as-deletion. Two failure modes, two engines, on purpose. See
// docs/design/change-detection-patterns.md.
package reconcile

import "sort"

// RetirePolicy says whether a source is allowed to infer deletion at all.
type RetirePolicy int

const (
	// RetireNever — absence means nothing for this source; stored rows are never
	// removed by a sync. The correct policy for a source whose feed is PARTIAL or
	// paginated, where "not in this response" is routine rather than meaningful.
	RetireNever RetirePolicy = iota
	// RetireOnCompleteFetch — absence means deletion, but ONLY when the fetch was
	// complete and non-empty. The correct policy for a source that returns the
	// whole set for a scope in one shot, which is what the notebook does per book.
	RetireOnCompleteFetch
)

// Options parameterizes one reconciliation.
type Options struct {
	// Policy governs whether Missing keys may be retired at all.
	Policy RetirePolicy
	// FetchComplete is the caller's assertion that the fetch it is handing over
	// represents the WHOLE set for this scope — every page followed, no error
	// swallowed mid-way. A paginated fetch that bailed after page one must set
	// this false, or the keys it never got to see will be read as deletions.
	FetchComplete bool
}

// Plan is the decision. It carries the counts and the key lists a caller needs
// to apply, plus an explicit Retire verdict and the REASON when that verdict is
// false — so a caller (or a log line, or a test) can say why nothing was retired
// instead of silently doing nothing.
type Plan struct {
	// Seen is every key in the fetch, deduped and sorted. This is what a
	// storage-level retire call should be handed.
	Seen []string
	// New is the keys in the fetch that we did not already hold.
	New []string
	// Missing is the keys we hold that the fetch did not mention. It is populated
	// for observability EVEN WHEN Retire is false — knowing what would have been
	// retired is exactly what tells you a parser broke.
	Missing []string
	// Retire reports whether Missing may actually be acted on.
	Retire bool
	// Reason explains a false Retire in words fit for a log line.
	Reason string
}

// Counts is a small convenience for logging and metrics.
func (p Plan) Counts() (seen, added, missing int) {
	return len(p.Seen), len(p.New), len(p.Missing)
}

// BuildPlan diffs a fetched key set against the stored key set.
//
// `fetched` and `stored` may contain duplicates and may arrive in any order;
// both are deduped, and the output lists are sorted so a Plan is deterministic
// and directly comparable in tests.
func BuildPlan(fetched, stored []string, opts Options) Plan {
	seenSet := toSet(fetched)
	storedSet := toSet(stored)

	p := Plan{Seen: sortedKeys(seenSet)}
	for _, k := range p.Seen {
		if _, held := storedSet[k]; !held {
			p.New = append(p.New, k)
		}
	}
	for _, k := range sortedKeys(storedSet) {
		if _, present := seenSet[k]; !present {
			p.Missing = append(p.Missing, k)
		}
	}

	p.Retire, p.Reason = retireVerdict(len(p.Seen), opts)
	return p
}

// retireVerdict is the rule, isolated so it reads as one thing.
//
// Order matters: the empty-fetch check comes BEFORE the completeness check, so
// an empty fetch is always reported as such even if the caller also forgot to
// assert completeness. That gives the more accurate log line for the case that
// actually happens in production.
func retireVerdict(seen int, opts Options) (bool, string) {
	if opts.Policy == RetireNever {
		return false, "source policy is RetireNever: absence is not evidence of deletion here"
	}
	if seen == 0 {
		// THE guard. A source that returned nothing has told us nothing.
		return false, "the fetch returned no keys at all, which is indistinguishable from a broken parser — retiring on this would empty the scope"
	}
	if !opts.FetchComplete {
		return false, "the fetch was not complete (a page was missed or an error was swallowed), so absence does not imply deletion"
	}
	return true, ""
}

func toSet(keys []string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k == "" {
			continue // an unkeyable row cannot participate in a set diff
		}
		out[k] = struct{}{}
	}
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
