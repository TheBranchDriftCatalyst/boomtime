// naming_test.go — the two naming defects the audit found, pinned.
//
// Both are about the LAST path segment, which is the only one assembled by
// concatenation (a byte-capped value plus template text) and therefore the only
// one where the cap can eat something that is not the title.
package liberate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A title long enough to fill the segment budget must not pay for it with its
// EXTENSION. Before the fix the filename segment was "<120-byte title>.m4b"
// (124 bytes) and the whole-segment cap chopped it back to 120 — amputating
// ".m4b" exactly. Audiobookshelf/Plex skip a file with no audio extension, so
// the book was invisible in every player while the row said liberated.
func TestRenderPathKeepsTheExtensionAtTheByteCap(t *testing.T) {
	cases := []struct {
		name  string
		title string
	}{
		// 120 bytes of CJK: 40 runes at 3 bytes each — a real 40-character
		// Japanese title, not a synthetic edge case.
		{"CJK title exactly at the cap", strings.Repeat("日", 40)},
		// The sneakier one: 118 bytes leaves room for ".m4" but not ".m4b",
		// so the old code produced a plausible-looking WRONG extension.
		{"ASCII title two bytes under the cap", strings.Repeat("a", 118)},
		{"ASCII title far over the cap", strings.Repeat("b", 400)},
		{"CJK title far over the cap", strings.Repeat("語", 300)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderPath("", BookMeta{Author: "A", Title: tc.title})
			if err != nil {
				t.Fatalf("RenderPath: %v", err)
			}
			segs := strings.Split(got, "/")
			file := segs[len(segs)-1]
			if !strings.HasSuffix(file, ".m4b") {
				t.Errorf("filename %q lost its .m4b extension — no library scanner will index this file", file)
			}
			if len(file) > maxSegmentBytes {
				t.Errorf("filename is %d bytes, want <= %d", len(file), maxSegmentBytes)
			}
			if !utf8.ValidString(file) {
				t.Error("truncation split a rune and produced invalid UTF-8")
			}
			if stem := strings.TrimSuffix(file, ".m4b"); stem == "" {
				t.Error("the whole stem was truncated away, leaving a bare dotfile")
			}
		})
	}
}

// Ordinary names must come out byte-identical to the old whole-segment rule —
// the extension carve-out is a cap fix, not a renaming.
func TestSanitizeFilenameLeavesShortNamesAlone(t *testing.T) {
	for _, name := range []string{
		"Snow Crash.m4b",
		"Mr. Penumbra's 24-Hour Bookstore.m4b",
		"S.P.Q.R.m4b",
		"no extension at all",
		"Vol. 2",
	} {
		if got, want := sanitizeFilename(name), SanitizeSegment(name); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q (unchanged)", name, got, want)
		}
	}
}

// DisambiguatePath is the collision escape hatch: it must produce a path that is
// actually DIFFERENT, keep the extension, stay inside the byte cap, and be a
// no-op the second time so a caller can detect "nothing left to try".
func TestDisambiguatePath(t *testing.T) {
	const asin = "B09GCYRZRQ"

	t.Run("default template shape tags the book folder and the file", func(t *testing.T) {
		in := "Matt Dinniman/Dungeon Crawler Carl/The Gate/The Gate.m4b"
		want := "Matt Dinniman/Dungeon Crawler Carl/The Gate [" + asin + "]/The Gate [" + asin + "].m4b"
		if got := DisambiguatePath(in, asin); got != want {
			t.Errorf("DisambiguatePath = %q, want %q", got, want)
		}
	})

	t.Run("a folder not named for the title is left alone", func(t *testing.T) {
		in := "Author/The Expanse/01 - Leviathan Wakes.m4b"
		want := "Author/The Expanse/01 - Leviathan Wakes [" + asin + "].m4b"
		if got := DisambiguatePath(in, asin); got != want {
			t.Errorf("DisambiguatePath = %q, want %q", got, want)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		once := DisambiguatePath("A/T/T.m4b", asin)
		if twice := DisambiguatePath(once, asin); twice != once {
			t.Errorf("second pass changed the path: %q -> %q", once, twice)
		}
	})

	t.Run("a name already at the cap makes room for the tag", func(t *testing.T) {
		long := strings.Repeat("z", 200)
		in, err := RenderPath("", BookMeta{Author: "A", Title: long})
		if err != nil {
			t.Fatalf("RenderPath: %v", err)
		}
		got := DisambiguatePath(in, asin)
		if got == in {
			t.Fatal("a capped name was not disambiguated at all — the caller would have to refuse the book")
		}
		for _, seg := range strings.Split(got, "/") {
			if len(seg) > maxSegmentBytes {
				t.Errorf("segment %q is %d bytes, want <= %d", seg, len(seg), maxSegmentBytes)
			}
		}
		file := got[strings.LastIndex(got, "/")+1:]
		if !strings.HasSuffix(file, "]"+".m4b") {
			t.Errorf("filename %q lost either its tag or its extension", file)
		}
	})

	t.Run("an empty asin cannot disambiguate", func(t *testing.T) {
		if got := DisambiguatePath("A/T.m4b", ""); got != "A/T.m4b" {
			t.Errorf("got %q, want the input unchanged", got)
		}
	})
}
