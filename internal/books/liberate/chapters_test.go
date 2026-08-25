package liberate

import (
	"strconv"
	"strings"
	"testing"
)

// parseChapterMarks pulls START/END/title triples back out of an ffmetadata
// document so assertions are on the RENDERED output ffmpeg will actually read,
// not on the intermediate slice.
func parseChapterMarks(t *testing.T, doc string) []flatChapter {
	t.Helper()
	var out []flatChapter
	var cur flatChapter
	inChapter := false
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case line == "[CHAPTER]":
			if inChapter {
				out = append(out, cur)
			}
			cur, inChapter = flatChapter{}, true
		case strings.HasPrefix(line, "START="):
			cur.StartMs, _ = strconv.ParseInt(strings.TrimPrefix(line, "START="), 10, 64)
		case strings.HasPrefix(line, "END="):
			cur.EndMs, _ = strconv.ParseInt(strings.TrimPrefix(line, "END="), 10, 64)
		case strings.HasPrefix(line, "title="):
			cur.Title = strings.TrimPrefix(line, "title=")
		}
	}
	if inChapter {
		out = append(out, cur)
	}
	return out
}

// The shape the live probe actually returned: Tree requested, FLAT delivered,
// 37 chapters, non-zero brand offsets. This is the common case, not the edge.
func TestBuildFFMetadataFlatTree(t *testing.T) {
	ci := ChapterInfo{
		BrandIntroDurationMs: 3924,
		BrandOutroDurationMs: 4945,
		RuntimeLengthMs:      30000,
		IsAccurate:           true,
		Chapters: []Chapter{
			{Title: "Opening Credits", StartOffsetMs: 0, LengthMs: 3924},
			{Title: "Chapter 1", StartOffsetMs: 3924, LengthMs: 10000},
			{Title: "Chapter 2", StartOffsetMs: 13924, LengthMs: 10000},
		},
	}

	doc := BuildFFMetadata(ci)
	if !strings.HasPrefix(doc, ";FFMETADATA1\n") {
		t.Fatalf("missing ffmetadata header: %q", doc[:min(40, len(doc))])
	}
	if !strings.Contains(doc, "TIMEBASE=1/1000") {
		t.Error("missing TIMEBASE — ffmpeg would misread every offset")
	}
	marks := parseChapterMarks(t, doc)
	if len(marks) != 3 {
		t.Fatalf("got %d chapters, want 3", len(marks))
	}
	// Offsets are emitted UNMODIFIED: they already include the Audible intro,
	// so shifting them by BrandIntroDurationMs would desync every mark.
	if marks[1].StartMs != 3924 {
		t.Errorf("chapter 1 start = %d, want the raw 3924 (no brand-offset correction)", marks[1].StartMs)
	}
	// The last chapter runs to the declared runtime.
	if marks[2].EndMs != 30000 {
		t.Errorf("last chapter end = %d, want RuntimeLengthMs 30000", marks[2].EndMs)
	}
}

// Nesting must still work when Audible does send it — leaves only, with the
// part title prefixed. Emitting the parent too would overlap its children.
func TestBuildFFMetadataNestedEmitsLeavesWithPartPrefix(t *testing.T) {
	ci := ChapterInfo{
		RuntimeLengthMs: 40000,
		Chapters: []Chapter{
			{Title: "Part One", StartOffsetMs: 0, LengthMs: 20000, Chapters: []Chapter{
				{Title: "Chapter 1", StartOffsetMs: 0, LengthMs: 10000},
				{Title: "Chapter 2", StartOffsetMs: 10000, LengthMs: 10000},
			}},
			{Title: "Part Two", StartOffsetMs: 20000, LengthMs: 20000, Chapters: []Chapter{
				{Title: "Chapter 3", StartOffsetMs: 20000, LengthMs: 20000},
			}},
		},
	}

	marks := parseChapterMarks(t, BuildFFMetadata(ci))
	if len(marks) != 3 {
		t.Fatalf("got %d chapters, want 3 leaves (parents must not be emitted)", len(marks))
	}
	if marks[0].Title != "Part One · Chapter 1" {
		t.Errorf("title = %q, want the part prefixed onto the leaf", marks[0].Title)
	}
	if marks[2].Title != "Part Two · Chapter 3" {
		t.Errorf("title = %q", marks[2].Title)
	}
	// No overlaps: each chapter must end exactly where the next begins.
	for i := 0; i+1 < len(marks); i++ {
		if marks[i].EndMs != marks[i+1].StartMs {
			t.Errorf("chapter %d ends at %d but %d starts at %d — overlapping marks",
				i, marks[i].EndMs, i+1, marks[i+1].StartMs)
		}
	}
}

// Audible's length_ms is not always trustworthy. Boundaries get repaired from
// the next chapter's start, because ffmpeg rejects END <= START and renders
// overlaps unpredictably.
func TestBuildFFMetadataRepairsBadBoundaries(t *testing.T) {
	ci := ChapterInfo{
		RuntimeLengthMs: 30000,
		Chapters: []Chapter{
			{Title: "A", StartOffsetMs: 0, LengthMs: 999999}, // absurdly long
			{Title: "B", StartOffsetMs: 10000, LengthMs: 0},  // no length at all
			{Title: "C", StartOffsetMs: 20000, LengthMs: 0},
		},
	}

	marks := parseChapterMarks(t, BuildFFMetadata(ci))
	if len(marks) != 3 {
		t.Fatalf("got %d chapters, want 3", len(marks))
	}
	for i, m := range marks {
		if m.EndMs <= m.StartMs {
			t.Errorf("chapter %d has END %d <= START %d — ffmpeg would reject it", i, m.EndMs, m.StartMs)
		}
	}
	if marks[0].EndMs != 10000 {
		t.Errorf("overlong chapter A was not clamped to the next start: end=%d", marks[0].EndMs)
	}
}

// Out-of-order input must be sorted; ffmpeg expects ascending chapters.
func TestBuildFFMetadataSortsByStart(t *testing.T) {
	ci := ChapterInfo{
		RuntimeLengthMs: 30000,
		Chapters: []Chapter{
			{Title: "Third", StartOffsetMs: 20000, LengthMs: 10000},
			{Title: "First", StartOffsetMs: 0, LengthMs: 10000},
			{Title: "Second", StartOffsetMs: 10000, LengthMs: 10000},
		},
	}

	marks := parseChapterMarks(t, BuildFFMetadata(ci))
	got := []string{marks[0].Title, marks[1].Title, marks[2].Title}
	want := []string{"First", "Second", "Third"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Chapter titles are Amazon-supplied text. An unescaped "=" or newline would
// corrupt the document and take the whole chapter list with it.
func TestBuildFFMetadataEscapesHostileTitles(t *testing.T) {
	ci := ChapterInfo{
		RuntimeLengthMs: 20000,
		Chapters: []Chapter{
			{Title: "E=mc2; #1 \\ hit\nsecond line", StartOffsetMs: 0, LengthMs: 10000},
			{Title: "Normal", StartOffsetMs: 10000, LengthMs: 10000},
		},
	}

	doc := BuildFFMetadata(ci)
	marks := parseChapterMarks(t, doc)
	if len(marks) != 2 {
		t.Fatalf("got %d chapters, want 2 — an unescaped newline split the document", len(marks))
	}
	title := marks[0].Title
	for _, raw := range []string{`\=`, `\;`, `\#`, `\\`} {
		if !strings.Contains(title, raw) {
			t.Errorf("title %q missing escape %q", title, raw)
		}
	}
	if strings.Contains(title, "\n") {
		t.Error("newline survived into the title")
	}
}

// No chapters → empty document, so the caller can omit the input entirely.
// Handing ffmpeg an empty metadata file is an error, not a no-op.
func TestBuildFFMetadataEmpty(t *testing.T) {
	if got := BuildFFMetadata(ChapterInfo{}); got != "" {
		t.Errorf("BuildFFMetadata(empty) = %q, want an empty string", got)
	}
	if got := BuildFFMetadata(ChapterInfo{RuntimeLengthMs: 1000}); got != "" {
		t.Errorf("runtime with no chapters = %q, want empty", got)
	}
}

// An untitled chapter still gets a mark rather than being dropped — a book with
// unnamed chapters must remain navigable.
func TestBuildFFMetadataNamesUntitledChapters(t *testing.T) {
	ci := ChapterInfo{
		RuntimeLengthMs: 20000,
		Chapters: []Chapter{
			{StartOffsetMs: 0, LengthMs: 10000},
			{StartOffsetMs: 10000, LengthMs: 10000},
		},
	}
	marks := parseChapterMarks(t, BuildFFMetadata(ci))
	if len(marks) != 2 {
		t.Fatalf("got %d marks, want 2", len(marks))
	}
	for _, m := range marks {
		if m.Title == "" {
			t.Error("an untitled chapter produced an empty title")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
