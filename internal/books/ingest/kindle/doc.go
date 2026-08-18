// Package books — status curation + three-way sync pointer.
//
// Kindle status produced in this package is only ever DERIVED
// (want/reading/read from percentageRead + the sidecar LPR). Amazon (Kindle +
// Audible) is a READ-ONLY source: we never sync TO it, and it is not a
// last-write-wins participant. DNF/Paused, rating, and finished-date OVERRIDES
// live in a separate override layer (reading_items.status_override /
// rating_override / finished_at_override, stamped by curation_updated_at),
// written only by the user PATCH endpoint or the Hardcover pull. Effective value
// = COALESCE(override, derived). Ingest in this package MUST NOT write the
// override columns — that invariant is what keeps a fresh-timestamp Amazon sync
// from clobbering a user's DNF override.
//
// The full model — field-ownership + sync-direction matrix, why Amazon is not a
// LWW participant (the clobber trap), the canonical 1:1
// want/reading/read/paused/dnf ↔ Hardcover status_id vocabulary (filter labels ==
// group values == pill labels == Hardcover names), the derivation heuristic
// (>95% audio / 100% kindle = read), Amazon-finish promotion, echo-suppression
// via hardcover_pushed_at, and the dry-run gate (BOOM_HARDCOVER_DRYRUN, default
// true) — is documented in
// docs/design/catalyst-books-domain-architecture.md §4A. Read it before touching
// status, rating, or finished-date sync.
package kindle
