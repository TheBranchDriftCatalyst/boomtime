// jobs.go — the catalyst-go-jobs contract for liberation (boom-w20s.14).
// The kinds and payloads live here, in the domain, so internal/books/jobs only
// wires handlers rather than owning liberation's vocabulary.
// See docs/design/catalyst-books-liberation-architecture.md §5.
package liberate

// Job kinds.
const (
	// LiberateBookKind liberates ONE title end-to-end. Owner-scoped, with an
	// ASIN payload.
	LiberateBookKind = "books-liberate-book"
	// LiberateSweepKind enqueues a LiberateBookKind job per unliberated title
	// for one owner. It does not liberate anything itself: one job per book
	// means each gets its own retry, its own concurrency slot, and its own
	// heartbeat, instead of one giant job that loses 400 books' progress when it
	// dies on book 401.
	LiberateSweepKind = "books-liberate-sweep"
)

// BookPayload is the self-contained payload for LiberateBookKind.
type BookPayload struct {
	Owner string `json:"owner"`
	ASIN  string `json:"asin"`
	Force bool   `json:"force,omitempty"`
}

// SweepPayload is the payload for LiberateSweepKind. Limit caps how many books
// one sweep enqueues; 0 means every pending title.
//
// The cap exists because a first sweep of a large library is hundreds of GB, and
// an operator who wants to dip a toe in should be able to say "just do 5" rather
// than choosing between nothing and everything.
type SweepPayload struct {
	Owner string `json:"owner"`
	Limit int    `json:"limit,omitempty"`
	Force bool   `json:"force,omitempty"`
}
