// Package labelcatalog is the Go-side mirror of the TypeScript label catalog
// (web/src/features/publicprofile/labels/catalog.ts). It carries JUST what the
// label-image worker needs — the pair {id, imagePrompt} for every label the
// backend should render an image for.
//
// Why a mirror instead of loading the TS catalog at runtime:
//
//   - The FE catalog is TypeScript; parsing it from Go would need a JS runtime
//     or a codegen step in the Go build pipeline. Both add ceremony without
//     paying off for a 20-40 entry list that rarely changes.
//   - Runtime coordination between FE and BE (FE POSTs its catalog to a
//     /registry endpoint on startup) adds a chatty side channel and a startup
//     ordering dependency (backend can't generate anything until at least one
//     FE has booted). Not worth the complexity for a static manifest.
//
// The drift guard lives in catalog_drift_test.go: it reads catalog.ts at
// build time (via a relative path from this package), extracts every `id:`
// literal, and asserts every entry that has an `imagePrompt` field also
// appears in this Go catalog with the SAME id. When the TS side adds a new
// prompt (e.g. the memecore/kawaii expansion), CI fails until the operator
// adds the corresponding Go entry. Same shape as the aggregation-invariants
// audit tests.
package labelcatalog

// Entry is the minimum shape the worker needs: an id (row primary key) and
// the prompt to send to the shim. Anything else — glyph, description, rank
// — is purely FE display concern and stays in the TS catalog.
type Entry struct {
	ID     string
	Prompt string
}

// Entries is the shipped list. Ordered archetype-then-tribe (matches the TS
// catalog's authoring order) so `regenerate --all` produces a predictable
// generation sequence in logs.
//
// Style guide for prompts (kept short so the shim's default 1024x1024
// generation stays sharp): "a distinctive emblem/badge representing X,
// cyberpunk hacker aesthetic, dark red on black, no text". Varied imagery
// per label, no baked-in text (the FE already shows the label text next to
// the image).
var Entries = []Entry{
	// ---------------- ARCHETYPES ----------------
	{
		ID:     "late-night-coder",
		Prompt: "hooded figure at a glowing terminal, moonlight through venetian blinds, cyberpunk noir emblem, deep red and black, no text",
	},
	{
		ID:     "early-bird",
		Prompt: "silhouette coding as sun rises over a neon skyline, warm ambient red glow, cyberpunk emblem, no text",
	},
	{
		ID:     "weekend-warrior",
		Prompt: "battle-scarred cyber-samurai coding on a tatami mat, weekend zen, red katana across knees, emblem style, no text",
	},
	{
		ID:     "monogamist",
		Prompt: "singular focus, monastic coder before one glowing screen, red halo, cyberpunk emblem, deep red and black, no text",
	},
	{
		ID:     "polyglot",
		Prompt: "a many-tongued cyber-oracle with multiple screens showing different scripts, chrome accents, deep red glow, emblem, no text",
	},
	{
		ID:     "consistent",
		Prompt: "brutalist clock tower emitting red pulses, unbroken chain of light, discipline motif, cyberpunk emblem, no text",
	},
	{
		ID:     "sprinter",
		Prompt: "figure sprinting through neon rain, motion blur, red trailing light streaks, cyberpunk emblem, no text",
	},
	{
		ID:     "machine",
		Prompt: "half-android coder plugged directly into a terminal, tireless, deep red glow, cyberpunk emblem, no text",
	},
	{
		ID:     "deep-focus",
		Prompt: "figure in meditative trance surrounded by three floating holographic screens, red aura, cyberpunk emblem, no text",
	},
	{
		ID:     "multi-tasker",
		Prompt: "cyber-Kali with many arms typing on many keyboards, chrome accents, deep red, cyberpunk emblem, no text",
	},
	{
		ID:     "meeting-warrior",
		Prompt: "stern corporate samurai in a boardroom, katana laid across knees, red arasaka-style emblem, cyberpunk, no text",
	},
	{
		ID:     "ai-native",
		Prompt: "cyber-monk conversing with a shimmering AI hologram, neon red glyphs, symbiosis motif, cyberpunk emblem, no text",
	},
	{
		ID:     "test-obsessive",
		Prompt: "figure inspecting glowing code under a microscope, forensic precision, cyberpunk emblem, deep red and black, no text",
	},
	{
		ID:     "documenter",
		Prompt: "hooded scribe illuminating cyberpunk scrolls with red ink, quill of light, emblem style, no text",
	},

	// ---------------- TRIBES ----------------
	{
		ID:     "vim-enjoyer",
		Prompt: "cyber-monk with a modal keyboard tattooed on hands, hjkl glowing red, deep red and black emblem, no text",
	},
	{
		ID:     "emacs-elder",
		Prompt: "ancient wizard in flowing robes at a parenthesis altar, red bracket sigils floating around, cyberpunk emblem, no text",
	},
	{
		ID:     "terminal-purist",
		Prompt: "ascetic figure at a monochrome terminal, no color except a deep red prompt cursor, cyberpunk emblem, no text",
	},
	{
		ID:     "mac-native",
		Prompt: "sleek chrome apple-shaped emblem, arasaka red, corporate cyberpunk minimalism, no text",
	},
	{
		ID:     "linux-warlord",
		Prompt: "war-painted penguin warrior in cyber-armor, chrome tusks, deep red war paint, cyberpunk emblem, no text",
	},
	{
		ID:     "windows-survivor",
		Prompt: "battle-scarred figure walking away from shattered windows, red debris raining, cyberpunk emblem, no text",
	},
	{
		ID:     "cross-platform",
		Prompt: "three-headed cyber-hydra, one head per operating system, chrome scales, glowing red eyes, cyberpunk emblem, no text",
	},
}

// ByID returns the entry for `id`, or (Entry{}, false) if absent.
func ByID(id string) (Entry, bool) {
	for _, e := range Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// IDs returns just the ordered list of label ids (used by tests + logging).
func IDs() []string {
	out := make([]string, len(Entries))
	for i, e := range Entries {
		out[i] = e.ID
	}
	return out
}
