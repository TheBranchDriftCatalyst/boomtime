// chapters.go — step 5a of liberation: turn Audible's chapter data into an
// ffmetadata file ffmpeg can mux into the M4B.
// See docs/design/catalyst-books-liberation-architecture.md §2.5.
//
// LIVE-VERIFIED 2026-08-24: requesting chapter_titles_type "Tree" does NOT
// guarantee a tree. A 37-chapter title came back entirely FLAT. So the flatten
// must handle both shapes and assume no particular depth — the nesting is a
// possibility to accommodate, not a structure to rely on.
//
// The same run confirmed the branding offsets are real and non-zero (3924 ms
// intro, 4945 ms outro) with is_accurate=true, which means chapter offsets ARE
// measured from the start of the file including the "This is Audible" intro.
// We therefore emit Audible's offsets unmodified: they already line up with the
// audio we downloaded, and "correcting" them by the intro duration would push
// every chapter mark out of sync.
package liberate

import (
	"fmt"
	"sort"
	"strings"
)

// flatChapter is one emitted chapter mark, in milliseconds.
type flatChapter struct {
	Title   string
	StartMs int64
	EndMs   int64
}

// BuildFFMetadata renders an ffmetadata document for ci. The result is written
// to a file and passed to ffmpeg as a second input with -map_metadata.
//
// Returns an empty string when there are no usable chapters — the caller then
// omits the chapter input entirely rather than handing ffmpeg an empty file,
// which it treats as an error.
func BuildFFMetadata(ci ChapterInfo) string {
	chapters := flattenChapters(ci)
	if len(chapters) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, c := range chapters {
		b.WriteString("\n[CHAPTER]\n")
		b.WriteString("TIMEBASE=1/1000\n")
		fmt.Fprintf(&b, "START=%d\n", c.StartMs)
		fmt.Fprintf(&b, "END=%d\n", c.EndMs)
		fmt.Fprintf(&b, "title=%s\n", escapeFFMetadata(c.Title))
	}
	return b.String()
}

// flattenChapters walks the (possibly flat, possibly nested) chapter list into a
// sorted, non-overlapping, monotonically increasing sequence.
//
// Only LEAF chapters are emitted. A parent that contains children is a "part"
// heading; emitting it as well would produce overlapping chapter marks, which
// players render as duplicate or unnavigable entries. The part title is instead
// PREFIXED onto its children — that is the whole reason we ask for Tree rather
// than Flat, so discarding it would make the request pointless.
func flattenChapters(ci ChapterInfo) []flatChapter {
	var out []flatChapter
	var walk func(nodes []Chapter, prefix string)
	walk = func(nodes []Chapter, prefix string) {
		for _, n := range nodes {
			title := strings.TrimSpace(n.Title)
			if len(n.Chapters) > 0 {
				childPrefix := title
				if prefix != "" && title != "" {
					childPrefix = prefix + " · " + title
				} else if title == "" {
					childPrefix = prefix
				}
				walk(n.Chapters, childPrefix)
				continue
			}
			full := title
			if prefix != "" {
				if full == "" {
					full = prefix
				} else {
					full = prefix + " · " + full
				}
			}
			if full == "" {
				full = fmt.Sprintf("Chapter %d", len(out)+1)
			}
			out = append(out, flatChapter{
				Title:   full,
				StartMs: n.StartOffsetMs,
				EndMs:   n.StartOffsetMs + n.LengthMs,
			})
		}
	}
	walk(ci.Chapters, "")
	if len(out) == 0 {
		return nil
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })

	// Repair the boundaries. ffmpeg rejects a chapter whose END precedes its
	// START, and renders overlapping chapters unpredictably — and Audible's
	// length_ms is not always present or consistent, so we cannot simply trust
	// start+length. Each chapter ends where the next begins; the last ends at
	// the declared runtime when we have one.
	for i := range out {
		if i+1 < len(out) {
			out[i].EndMs = out[i+1].StartMs
		} else if ci.RuntimeLengthMs > out[i].StartMs {
			out[i].EndMs = ci.RuntimeLengthMs
		} else if out[i].EndMs <= out[i].StartMs {
			// No runtime and no usable length: give it a nominal second so the
			// mark still exists rather than being silently dropped by ffmpeg.
			out[i].EndMs = out[i].StartMs + 1000
		}
	}

	// Drop degenerate marks left over after the repair (two chapters sharing a
	// start offset would otherwise produce a zero-length entry).
	kept := out[:0]
	for _, c := range out {
		if c.EndMs > c.StartMs {
			kept = append(kept, c)
		}
	}
	return kept
}

// escapeFFMetadata escapes the characters ffmetadata treats as syntax. Chapter
// titles are Amazon-supplied text, so a title containing "=" or a newline would
// otherwise corrupt the document and take the chapter list with it.
func escapeFFMetadata(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		"=", "\\=",
		";", "\\;",
		"#", "\\#",
		"\n", " ",
		"\r", " ",
	)
	return r.Replace(s)
}
