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
//     drop segments that came out empty.
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
	for _, seg := range rawSegments {
		s := SanitizeSegment(seg)
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

// truncateUTF8 caps s at n BYTES without splitting a rune. Byte-based because
// filesystem limits are byte limits, not rune limits — a 100-rune CJK title is
// 300 bytes and would blow a 255-byte component limit.
func truncateUTF8(s string, n int) string {
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
