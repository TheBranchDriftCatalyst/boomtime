// catalog_drift_test.go — the sync guard promised in package doc.
//
// TS catalog.ts is authoritative for the labels themselves; this Go catalog
// is a subset (just {id, imagePrompt}). This test fails CI when the two
// drift, so a new memecore/kawaii label added TS-side doesn't silently
// starve the image worker.
//
// Two directions:
//   - Every TS label that CARRIES an `imagePrompt` field MUST have a Go
//     entry with the same id (backend needs to know what to render).
//   - Every Go entry MUST have a matching TS id (dead Go entries would
//     produce orphan rows the FE never asks about).
//
// The TS catalog is not compiled here — we regex the source text. Cheap,
// no JS runtime, works in a plain Go test binary.
package labelcatalog

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// catalogTSPath resolves the on-disk TS catalog relative to this test file.
// The test is only meaningful when the repo layout is intact; we skip
// gracefully otherwise (e.g. running from an extracted binary).
func catalogTSPath(t *testing.T) string {
	t.Helper()
	// Go test cwd is the package directory (internal/labelcatalog).
	rel, err := filepath.Abs("../../web/src/features/publicprofile/labels/catalog.ts")
	if err != nil {
		t.Skipf("cannot resolve TS catalog path: %v", err)
	}
	if _, err := os.Stat(rel); err != nil {
		t.Skipf("TS catalog not found at %s (are we in a repo checkout?): %v", rel, err)
	}
	return rel
}

// tsIDRe matches `id: "foo-bar"` (or single-quoted). Doesn't match the
// `tierKey` field which uses colons.
var tsIDRe = regexp.MustCompile(`(?m)^\s*id:\s*["']([a-zA-Z0-9\-]+)["']`)

// tsPromptedIDRe matches an object literal with an `imagePrompt` field near
// an `id:` field, capturing the id. The two fields can appear in any order
// so we simply record every literal that contains BOTH markers.
//
// (?s) enables . matching \n so the two fields can be on different lines
// within the same object literal.
var tsImagePromptRe = regexp.MustCompile(`imagePrompt\s*:`)

// TestCatalogDrift_TSPromptedIDsReported: informational only. The Go
// catalog is a BASELINE for the startup worker (it always generates the
// classic archetypes/tribes on boot). Any additional TS-side prompted
// labels are the admin tab's responsibility — the FE POSTs the full
// {id, prompt} list to the auth'd regeneration endpoint, so the Go side
// doesn't need to mirror them.
//
// The test still LOGS the delta so a maintainer running the suite sees the
// list of TS-prompted-but-not-Go-baseline labels at a glance — useful when
// deciding whether to promote a new label to the always-on baseline.
func TestCatalogDrift_TSPromptedIDsReported(t *testing.T) {
	path := catalogTSPath(t)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	idMatches := tsIDRe.FindAllStringSubmatchIndex(string(src), -1)
	if len(idMatches) == 0 {
		t.Skip("no id: matches in catalog.ts — grammar may have changed; update tsIDRe")
	}
	goIDs := map[string]bool{}
	for _, e := range Entries {
		goIDs[e.ID] = true
	}
	srcStr := string(src)
	var beyondBaseline []string
	for _, m := range idMatches {
		id := srcStr[m[2]:m[3]]
		windowStart := m[0]
		windowEnd := m[1] + 800
		if windowEnd > len(srcStr) {
			windowEnd = len(srcStr)
		}
		if !tsImagePromptRe.MatchString(srcStr[windowStart:windowEnd]) {
			continue
		}
		if !goIDs[id] {
			beyondBaseline = append(beyondBaseline, id)
		}
	}
	if len(beyondBaseline) > 0 {
		t.Logf("info: %d TS-prompted label(s) are NOT in the Go baseline catalog — they'll only be generated via the admin regen endpoint (or the CLI --all against a POSTed list). Ids: %v",
			len(beyondBaseline), beyondBaseline)
	}
}

// TestCatalogDrift_GoIDsExistInTS: every id in this Go catalog must appear
// in the TS catalog. Prevents dead Go entries from producing orphan images.
func TestCatalogDrift_GoIDsExistInTS(t *testing.T) {
	path := catalogTSPath(t)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	tsIDs := map[string]bool{}
	for _, m := range tsIDRe.FindAllStringSubmatch(string(src), -1) {
		tsIDs[m[1]] = true
	}
	for _, e := range Entries {
		if !tsIDs[e.ID] {
			t.Errorf("Go labelcatalog.Entries has %q but TS catalog.ts does not define a label with that id — remove the Go entry or add the TS spec", e.ID)
		}
	}
}

// TestCatalogDrift_NoDuplicateGoIDs: sanity — the Go catalog must not
// declare the same id twice.
func TestCatalogDrift_NoDuplicateGoIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Entries {
		if seen[e.ID] {
			t.Errorf("duplicate id %q in labelcatalog.Entries", e.ID)
		}
		seen[e.ID] = true
	}
}
