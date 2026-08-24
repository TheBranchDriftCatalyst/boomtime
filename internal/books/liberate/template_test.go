package liberate

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderPathLayouts(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		meta BookMeta
		want string
	}{
		{
			name: "standalone book drops the series group entirely",
			meta: BookMeta{Author: "Neal Stephenson", Title: "Snow Crash"},
			want: "Neal Stephenson/Snow Crash/Snow Crash.m4b",
		},
		{
			name: "in-series book keeps the series folder",
			meta: BookMeta{Author: "James S. A. Corey", Title: "Leviathan Wakes", Series: "The Expanse"},
			want: "James S. A. Corey/The Expanse/Leviathan Wakes/Leviathan Wakes.m4b",
		},
		{
			name: "series index available to templates that want it",
			tmpl: "{author}/[{series}/][{series_index} - ]{title}.m4b",
			meta: BookMeta{Author: "James S. A. Corey", Title: "Leviathan Wakes", Series: "The Expanse", SeriesIndex: "01"},
			want: "James S. A. Corey/The Expanse/01 - Leviathan Wakes.m4b",
		},
		{
			// The whole point of optional groups: one template, both cases, no
			// dangling " - " separator when the index is absent.
			name: "optional group with a missing value drops its literal text too",
			tmpl: "{author}/[{series_index} - ]{title}.m4b",
			meta: BookMeta{Author: "Neal Stephenson", Title: "Snow Crash"},
			want: "Neal Stephenson/Snow Crash.m4b",
		},
		{
			name: "whitespace-only value counts as empty for group purposes",
			tmpl: "{author}/[{series}/]{title}.m4b",
			meta: BookMeta{Author: "Author", Title: "Title", Series: "   "},
			want: "Author/Title.m4b",
		},
		{
			name: "empty template falls back to the default",
			tmpl: "",
			meta: BookMeta{Author: "Author", Title: "Title"},
			want: "Author/Title/Title.m4b",
		},
		{
			name: "an empty author segment collapses rather than making //",
			meta: BookMeta{Title: "Orphan Work"},
			want: "Orphan Work/Orphan Work.m4b",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderPath(tc.tmpl, tc.meta)
			if err != nil {
				t.Fatalf("RenderPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("RenderPath = %q, want %q", got, tc.want)
			}
		})
	}
}

// The hostile-input table. Every value here is something Amazon could plausibly
// return (or an attacker could get into a title), and none of it may produce a
// path that escapes, forges a directory level, or breaks a filesystem.
func TestRenderPathHostileInput(t *testing.T) {
	tests := []struct {
		name      string
		meta      BookMeta
		wantErr   bool
		mustNotBe []string
		mustHave  string
		segments  int
	}{
		{
			// ".." surviving INSIDE a filename is harmless — only a whole
			// segment of ".." can traverse, and the shared assertions below
			// check for that. What must not survive is the separator.
			name:      "parent traversal in the title",
			meta:      BookMeta{Author: "A", Title: "../../etc/passwd"},
			mustNotBe: []string{"/etc/"},
			segments:  3,
		},
		{
			name:      "absolute path in the author",
			meta:      BookMeta{Author: "/etc/cron.d", Title: "T"},
			mustNotBe: []string{"/etc/cron.d"},
			segments:  3,
		},
		{
			name:      "backslash separator (SMB-style traversal)",
			meta:      BookMeta{Author: `..\..\windows`, Title: "T"},
			mustNotBe: []string{`\`},
			segments:  3,
		},
		{
			name:      "NUL byte truncation attempt",
			meta:      BookMeta{Author: "A", Title: "Book\x00.sh"},
			mustNotBe: []string{"\x00"},
			segments:  3,
		},
		{
			name:      "RTL override spoofing",
			meta:      BookMeta{Author: "A", Title: "Book‮gpj.exe"},
			mustNotBe: []string{"‮"},
			segments:  3,
		},
		{
			name:     "windows reserved device name",
			meta:     BookMeta{Author: "CON", Title: "T"},
			mustHave: "CON_",
			segments: 3,
		},
		{
			name:      "trailing dots and spaces stripped",
			meta:      BookMeta{Author: "A", Title: "Volume Two. "},
			mustHave:  "Volume Two",
			mustNotBe: []string{"Two. /", "Two./"},
			segments:  3,
		},
		{
			name:      "windows-hostile punctuation neutralised",
			meta:      BookMeta{Author: `A<>:"|?*B`, Title: "T"},
			mustNotBe: []string{"<", ">", ":", `"`, "|", "?", "*"},
			segments:  3,
		},
		{
			// Everything sanitises away, so there is no usable path at all.
			name:    "title of only illegal characters",
			meta:    BookMeta{Author: "...", Title: "   "},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderPath("", tc.meta)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got path %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderPath: %v", err)
			}
			for _, bad := range tc.mustNotBe {
				if strings.Contains(got, bad) {
					t.Errorf("path %q still contains %q", got, bad)
				}
			}
			if tc.mustHave != "" && !strings.Contains(got, tc.mustHave) {
				t.Errorf("path %q missing expected %q", got, tc.mustHave)
			}
			if n := len(strings.Split(got, "/")); tc.segments > 0 && n != tc.segments {
				t.Errorf("path %q has %d segments, want %d — a value forged a directory level", got, n, tc.segments)
			}
			if strings.HasPrefix(got, "/") {
				t.Errorf("path %q is absolute", got)
			}
			// The actual traversal property: no COMPONENT may be "." or "..".
			for _, seg := range strings.Split(got, "/") {
				if seg == "." || seg == ".." {
					t.Errorf("path %q contains a traversal segment %q", got, seg)
				}
			}
		})
	}
}

func TestSanitizeSegmentTruncatesOnByteBoundary(t *testing.T) {
	// CJK runes are 3 bytes each: 200 of them is 600 bytes, well over the cap.
	long := strings.Repeat("日", 200)
	got := SanitizeSegment(long)

	if len(got) > maxSegmentBytes {
		t.Errorf("segment is %d bytes, want <= %d", len(got), maxSegmentBytes)
	}
	if !utf8.ValidString(got) {
		t.Error("truncation split a rune and produced invalid UTF-8")
	}
	if got == "" {
		t.Error("truncation removed everything")
	}
}

func TestSanitizeSegmentASCIITruncation(t *testing.T) {
	got := SanitizeSegment(strings.Repeat("a", 400))
	if len(got) != maxSegmentBytes {
		t.Errorf("len = %d, want exactly %d", len(got), maxSegmentBytes)
	}
}

// A traversal that survives to the sink must still be refused there; and
// RenderPath must never be the thing that produced it.
func TestRenderPathNeverEscapes(t *testing.T) {
	nasty := []string{"../..", "....//....//", "/", "//", "./././.", ".."}
	for _, s := range nasty {
		got, err := RenderPath("", BookMeta{Author: s, Title: s})
		if err != nil {
			// All three are legitimate refusals for degenerate input.
			switch {
			case errors.Is(err, ErrEscapesRoot),
				strings.Contains(err.Error(), "empty path"),
				strings.Contains(err.Error(), "no name"):
			default:
				t.Errorf("input %q: unexpected error %v", s, err)
			}
			continue
		}
		if strings.HasPrefix(got, "/") {
			t.Errorf("input %q produced absolute path %q", s, got)
		}
		for _, seg := range strings.Split(got, "/") {
			if seg == "." || seg == ".." {
				t.Errorf("input %q produced escaping path %q", s, got)
			}
		}
	}
}
