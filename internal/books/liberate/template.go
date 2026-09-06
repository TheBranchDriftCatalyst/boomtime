// template.go — the naming-template half of Libation's FileManager: turn a
// book's metadata into the relative path its M4B lives at inside the library
// root. See docs/design/catalyst-books-liberation-architecture.md §6.
//
// THREAT MODEL. Every value substituted into a path here (title, author, series)
// is Amazon-supplied text that arrived over the network and was stored verbatim
// in reading_items.raw_meta. It is untrusted. A title containing "../" or a NUL
// or a 4 KB run of combining characters must not be able to write outside the
// library root, break the FS, or produce a name a downstream scanner chokes on.
// Sanitisation is therefore per-SEGMENT (so a separator injected mid-value can
// never create a new directory level) and the assembled path is prefix-checked
// against the root before it is returned.
package liberate

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultTemplate is the out-of-the-box layout. It follows the structure
// Audiobookshelf and Plex both scan cleanly: author, optional series, one folder
// per book, file named for the book.
//
//	Neal Stephenson/Snow Crash/Snow Crash.m4b
//	James S. A. Corey/The Expanse/Leviathan Wakes/Leviathan Wakes.m4b
//
// Bracketed groups are OPTIONAL: a group is dropped whole when any placeholder
// inside it renders empty (see renderOptionalGroups). That is what lets ONE
// template serve both the standalone and the in-series case, rather than the two
// separate templates the design doc originally proposed.
const DefaultTemplate = "{author}/[{series}/]{title}/{title}.m4b"

// maxSegmentBytes bounds each path component. 255 is the usual per-component FS
// limit; 120 leaves room for the ".partial" suffix, for a long library root, and
// for eCryptfs/encrypted homes which roughly halve the usable length.
const maxSegmentBytes = 120

// maxExtBytes bounds what splitExt is willing to treat as a file extension,
// dot included. Audio extensions are 4-5 bytes (".m4b", ".flac"); anything
// longer is a dot inside a TITLE ("Star Wars: Episode IV. A New Hope"), and
// mistaking one for an extension would let a title's own tail dodge the
// filename byte cap.
const maxExtBytes = 8

// ErrEscapesRoot is returned when a rendered path would land outside the library
// root. It is a hard failure, never a sanitise-and-continue: if we got here the
// input was actively hostile and the right move is to refuse and record it.
var ErrEscapesRoot = errors.New("liberate: rendered path escapes the library root")

// BookMeta is the substitution source for a template. Every field is untrusted
// text except ASIN, which the Audible API constrains — it is still sanitised.
type BookMeta struct {
	Title       string
	Subtitle    string
	Author      string
	Narrator    string
	Series      string
	SeriesIndex string
	Year        string
	ASIN        string
}

// placeholderRE matches {name} tokens.
var placeholderRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// optionalGroupRE matches [ ... ] optional groups. Non-greedy so adjacent groups
// stay separate; groups do not nest.
var optionalGroupRE = regexp.MustCompile(`\[([^\[\]]*)\]`)

// wsRE collapses whitespace runs.
var wsRE = regexp.MustCompile(`\s+`)

// windowsReserved are device names that are unusable as filenames on Windows and
// on SMB shares. The library may well be shared over SMB from truenas00, so a
// book by an author named "Aux" should not silently fail to write.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// values maps a BookMeta to its placeholder table.
func (b BookMeta) values() map[string]string {
	return map[string]string{
		"author":       b.Author,
		"title":        b.Title,
		"subtitle":     b.Subtitle,
		"narrator":     b.Narrator,
		"series":       b.Series,
		"series_index": b.SeriesIndex,
		"year":         b.Year,
		"asin":         b.ASIN,
	}
}

// RenderPath renders tmpl against meta and returns a CLEAN, RELATIVE path safe to
// join onto the library root. An empty tmpl uses DefaultTemplate.
//
// ORDER OF OPERATIONS. This is the security-critical part of the function and the
// sequence is not interchangeable:
//
//  1. sanitise every VALUE first. This is what makes separator injection
//     impossible: after this step no substituted value can contain "/" or "\\",
//     so it cannot forge a path component. Sanitising only the assembled
//     segments (the obvious-looking alternative) is WRONG, because the split on
//     "/" happens after substitution and would already have turned an injected
//     separator into a real directory level.
//  2. drop optional groups whose (sanitised) values are empty — after step 1, so
//     a value that sanitises away to nothing correctly collapses its group
//     instead of leaving a dangling separator or literal.
//  3. substitute the sanitised values.
//  4. reject a template that produced a file with no name (an empty {title}
//     against the default template would otherwise yield a file literally called
//     "m4b").
//  5. split on "/" — by construction these are template-authored separators only.
//  6. sanitise each segment again, for junk contributed by template LITERALS, and
//     drop segments that came out empty. The LAST segment goes through
//     sanitizeFilename instead, which protects the extension from the byte cap.
//  7. verify no segment is "." or ".." and the result is relative.
func RenderPath(tmpl string, meta BookMeta) (string, error) {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultTemplate
	}

	// (1) Values are untrusted; sanitise before they can influence structure.
	vals := make(map[string]string, 8)
	for k, v := range meta.values() {
		vals[k] = SanitizeSegment(v)
	}

	// (2) + (3)
	rendered := renderOptionalGroups(tmpl, vals)
	rendered = placeholderRE.ReplaceAllStringFunc(rendered, func(tok string) string {
		return vals[strings.Trim(tok, "{}")]
	})

	rawSegments := strings.Split(rendered, "/")

	// (4) The last segment is the filename. If everything before its extension
	// sanitises away, the template had nothing to name the file with.
	last := rawSegments[len(rawSegments)-1]
	if stem := SanitizeSegment(strings.TrimSuffix(last, filepath.Ext(last))); stem == "" {
		return "", fmt.Errorf("liberate: template %q produced a file with no name (empty title?)", tmpl)
	}

	// (5) + (6)
	segments := make([]string, 0, len(rawSegments))
	for i, seg := range rawSegments {
		// The filename is the ONE segment assembled by concatenation (a capped
		// value plus the template's extension), so it is the one that can lose
		// its extension to the cap. See sanitizeFilename.
		s := SanitizeSegment(seg)
		if i == len(rawSegments)-1 {
			s = sanitizeFilename(seg)
		}
		if s == "" {
			continue
		}
		// (7) SanitizeSegment strips leading/trailing dots, so a bare "." or ".."
		// cannot survive it — this asserts that invariant rather than trusting it.
		if s == "." || s == ".." {
			return "", fmt.Errorf("%w: segment %q", ErrEscapesRoot, s)
		}
		segments = append(segments, s)
	}
	if len(segments) == 0 {
		return "", errors.New("liberate: template rendered to an empty path")
	}

	out := filepath.Clean(filepath.Join(segments...))
	if filepath.IsAbs(out) || out == "." || out == ".." || strings.HasPrefix(out, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrEscapesRoot, out)
	}
	return out, nil
}

// renderOptionalGroups drops each [ ... ] group whose placeholders are not ALL
// non-empty, and unwraps the brackets from the ones that survive. A group with no
// placeholders at all is treated as literal text and kept.
func renderOptionalGroups(tmpl string, vals map[string]string) string {
	return optionalGroupRE.ReplaceAllStringFunc(tmpl, func(group string) string {
		inner := group[1 : len(group)-1]
		toks := placeholderRE.FindAllStringSubmatch(inner, -1)
		for _, t := range toks {
			if strings.TrimSpace(vals[t[1]]) == "" {
				return ""
			}
		}
		return inner
	})
}

// SanitizeSegment makes one path component safe. Exported because the sink needs
// the identical rule when it derives sidecar names, and because the unit test
// drives it directly with hostile input.
//
// Returns "" when nothing usable survives; callers drop empty segments.
func SanitizeSegment(seg string) string {
	if seg == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(seg))
	for _, r := range seg {
		switch {
		case r == 0:
			// NUL truncates the path at the syscall boundary — drop, never map.
			continue
		case r < 0x20 || r == 0x7f:
			continue // C0 controls + DEL
		case unicode.Is(unicode.Cf, r):
			// Format chars: RTL/LTR overrides and friends. These make a filename
			// display as something other than what it is — a spoofing vector in
			// any file browser. Drop them.
			continue
		case r == '/' || r == '\\':
			// A separator inside a VALUE would forge a new path level. This is
			// the single most important line in the function.
			b.WriteRune('-')
		case strings.ContainsRune(`<>:"|?*`, r):
			b.WriteRune('-') // Windows/SMB-hostile
		default:
			b.WriteRune(r)
		}
	}
	out := wsRE.ReplaceAllString(b.String(), " ")
	// Trailing dots and spaces are silently stripped by Windows/SMB, which turns
	// "Vol. 2." and "Vol. 2" into colliding names. Strip them ourselves so the
	// collision is visible here rather than surprising us on the share.
	out = strings.Trim(out, " .")
	if out == "" {
		return ""
	}
	if windowsReserved[strings.ToLower(out)] {
		out += "_"
	}
	return truncateUTF8(out, maxSegmentBytes)
}

// sanitizeFilename is SanitizeSegment for the LAST path component — the one that
// carries the file EXTENSION.
//
// WHY IT EXISTS. SanitizeSegment caps a component at maxSegmentBytes, and the
// filename is the only component built by CONCATENATING an already-capped value
// with template text ("{title}" + ".m4b"). A title that sanitises to the cap
// therefore yields a 124-byte segment whose LAST four bytes — the extension — are
// exactly what the cap then amputates, and a title 2 bytes under the cap yields
// the even sneakier ".m4". The committed file ends up with no audio extension (or
// a nonsense one), every library scanner skips it as a non-audio file, and the
// database happily reports the book liberated forever.
//
// So the stem is capped with the extension's cost already deducted, and the
// extension is re-appended AFTER sanitisation rather than riding through it.
// For any name comfortably under the cap the result is byte-identical to
// SanitizeSegment.
func sanitizeFilename(seg string) string {
	stem, ext := splitExt(seg)
	if ext == "" {
		return SanitizeSegment(seg)
	}
	stem = truncateUTF8(SanitizeSegment(stem), maxSegmentBytes-len(ext))
	if stem == "" {
		// Nothing usable in front of the extension. Fall back to the plain rule
		// rather than committing a hidden dotfile named ".m4b".
		return SanitizeSegment(seg)
	}
	return stem + ext
}

// splitExt splits a filename into its stem and its extension (dot included),
// returning an empty extension when the segment does not end in one.
//
// The test is deliberately STRICTER than filepath.Ext, which just finds the last
// dot: filepath.Ext("Vol. 2") is ". 2" and filepath.Ext("St. James") is
// ". James". Treating either as an extension would let the stem/extension split
// rewrite ordinary titles ("St.James"), so an extension here must be a short run
// of ASCII letters and digits — which is what every audio container uses and what
// a library scanner actually matches on.
func splitExt(seg string) (string, string) {
	dot := strings.LastIndexByte(seg, '.')
	if dot < 0 {
		return seg, ""
	}
	ext := seg[dot:]
	if len(ext) < 2 || len(ext) > maxExtBytes {
		return seg, ""
	}
	for i := 1; i < len(ext); i++ {
		c := ext[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		return seg, ""
	}
	return seg[:dot], ext
}

// DisambiguatePath returns rel with the ASIN tagged onto the filename, and onto
// the book folder when that folder is named for the same stem (the shape the
// default template produces).
//
// This is the collision escape hatch. The default template carries NO unique
// discriminator — two library items with the same author and title (a re-recorded
// edition, abridged vs unabridged, a plus-catalog duplicate) render the SAME path,
// and FSSink.Commit is documented to overwrite, so the second liberation silently
// destroys the first book's audio while both rows still claim to be liberated.
// The ASIN is the one value Amazon guarantees unique per title.
//
//	Matt Dinniman/Carl/Carl.m4b  ->  Matt Dinniman/Carl [B09X]/Carl [B09X].m4b
//
// Idempotent: tagging an already-tagged path returns it unchanged, which is what
// lets the caller detect "there is no distinct path to move to" and refuse rather
// than overwrite.
func DisambiguatePath(rel, asin string) string {
	tag := SanitizeSegment(asin)
	if rel == "" || tag == "" {
		return rel
	}
	dir, file := filepath.Split(rel)
	stem, _ := splitExt(file)
	if strings.HasSuffix(stem, tagSuffix(tag)) {
		return rel // already disambiguated
	}
	tagged := appendTag(file, tag)
	if tagged == file {
		return rel
	}
	segments := strings.Split(strings.TrimSuffix(dir, string(filepath.Separator)), string(filepath.Separator))
	if last := len(segments) - 1; dir != "" && segments[last] == stem {
		// The default template names the book FOLDER for the title too. Leaving
		// two different books in one folder would make Audiobookshelf treat them
		// as one multi-file book, so the folder gets the same tag.
		segments[last] = appendTag(segments[last], tag)
		return filepath.Join(append(segments, tagged)...)
	}
	return filepath.Join(dir, tagged)
}

// appendTag inserts " [tag]" before name's extension, making room inside the byte
// cap by shortening the STEM — never the tag, which is the whole point of the
// exercise.
func appendTag(name, tag string) string {
	stem, ext := splitExt(name)
	suffix := tagSuffix(tag)
	stem = truncateUTF8(SanitizeSegment(stem), maxSegmentBytes-len(suffix)-len(ext))
	if stem == "" {
		return name
	}
	return stem + suffix + ext
}

func tagSuffix(tag string) string { return " [" + tag + "]" }

// truncateUTF8 caps s at n BYTES without splitting a rune. Byte-based because
// filesystem limits are byte limits, not rune limits — a 100-rune CJK title is
// 300 bytes and would blow a 255-byte component limit.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		// A caller whose fixed suffix already exceeds the cap has no budget left.
		// Returning "" makes that a visible empty-stem fallback rather than a
		// negative slice bound.
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	// Re-trim: the cut may have exposed a trailing space or dot.
	return strings.Trim(s[:cut], " .")
}
