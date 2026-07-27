// Package git implements the laptop-side git-history backfill (gaka-vh8).
//
// The package is intentionally pure Go: it uses go-git for repo access and
// never shells out to `git`. Callers are the boomtime `backfill git` CLI
// subcommand (cmd/boomtime/backfill.go) and, indirectly, the admin UI via
// the /admin/backfill/preview server-side dry run.
//
// Flow overview:
//
//   WalkRepos(root) → list of repo paths under root that look like git repos
//     (skips vendored / cache dirs; a repo path is one whose .git/ resolves).
//   NewScanner(path, cfg) → an iterator over that repo's log filtered by
//     author email allowlist and optional [since, until] window.
//   Cluster(commits, cfg) → sessions: consecutive commits ≤ ClusterGap
//     apart become one session, spanned by [first − PreCommitLead,
//     last + PostCommitTail].
//   Materialize(session, sourceTag, rate) → synthetic Heartbeat rows
//     spaced HeartbeatRate apart across [session.Start, session.End],
//     each pointing at the "hottest" file in the session (most lines
//     touched) with the derived language.
//
// The server never invokes any of this — the CLI is the executor. The
// server's job is to accept materialized heartbeats, run the overlap
// check (skip any session that has real Wakatime data inside its window
// so we never double-count), insert with the existing unique_heartbeats
// constraint absorbing idempotency, and stream job state over WS.
package git

import (
	"path/filepath"
	"strings"
	"time"
)

// Commit is one filtered git commit, materialized into the shape the
// clusterer needs. Order-of-magnitude size: a 1000-commit repo produces a
// few hundred KiB of []Commit in memory, which is trivial.
//
// FilesChanged is the git-diff filename list against the parent (first
// parent for merges). Used both for the entity (we pick the "hottest"
// file for the session's heartbeats) and for language attribution (the
// language derived from the most-touched file's extension).
type Commit struct {
	RepoName     string    `json:"repo"`
	Hash         string    `json:"hash"`
	AuthorEmail  string    `json:"authorEmail"`
	Time         time.Time `json:"time"`
	FilesChanged []string  `json:"files,omitempty"`
	LinesAdded   int       `json:"linesAdded"`
	LinesDeleted int       `json:"linesDeleted"`
}

// Session is a cluster of commits that all sit within ClusterGap of the
// next. Start and End include the caller-configured lead/tail padding so
// the materialized heartbeats extend a bit before the first commit and
// after the last (mirrors editor plugin behavior — you type for a while
// before committing).
//
// Language is picked at cluster time (Cluster picks the extension that
// touched the most lines across every commit in the cluster and maps it
// via extToLang / cfg.LangMap). Empty when no files have a recognizable
// extension.
type Session struct {
	RepoName string    `json:"repo"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Commits  []Commit  `json:"commits"`
	Language string    `json:"language,omitempty"`
	// TopFile is the single file (across all commits in the session) with
	// the most lines added+deleted. Used as the `entity` for every
	// synthetic heartbeat in this session so time-attribution rolls up
	// under that path/name. Empty when no commit touched any file.
	TopFile string `json:"topFile,omitempty"`
}

// Heartbeat is the wire-shape sent to the boomtime API. Field names match
// model.HeartbeatPayload so a caller can round-trip it directly:
//
//	json.Marshal(hb) → the same JSON the /heartbeats endpoint expects.
//
// Every field is a pointer/string when the DB column is nullable so an
// unset editor/plugin doesn't accidentally show up as the string "".
type Heartbeat struct {
	Entity    string  `json:"entity"`
	Type      string  `json:"type"`     // always "file" for backfill
	Category  string  `json:"category"` // always "coding" for backfill
	Time      float64 `json:"time"`     // unix seconds (float, hakatime shape)
	Project   *string `json:"project,omitempty"`
	Language  *string `json:"language,omitempty"`
	Sender    *string `json:"sender,omitempty"` // e.g. "backfill:git"
	UserAgent string  `json:"user_agent"`
	// The remaining hakatime fields are all null for synthetic backfill —
	// omitted for compactness. Editor/plugin/machine deliberately not
	// filled: we don't want the FE's "Editors" or "Machines" tabs to grow
	// a phantom "backfill" row for something that never actually ran an
	// editor. The `sender` column is the tag we filter on.
}

// EstimatorConfig is the caller-supplied tuning for both Scanner and
// Cluster/Materialize. Values below the sensible floor are silently
// clamped; empty AuthorEmails means "accept every author" (the CLI
// enforces a non-empty allowlist at flag-parse time).
type EstimatorConfig struct {
	ClusterGap     time.Duration // default 30m
	PreCommitLead  time.Duration // default 15m
	PostCommitTail time.Duration // default 5m
	HeartbeatRate  time.Duration // default 2m (wakatime cadence)

	// AuthorEmails filters commits by exact-match author email. Empty
	// means "accept every author" — the CLI intentionally rejects an
	// empty flag+config combination so a caller can't accidentally
	// pull in every author on a shared machine.
	AuthorEmails []string

	// Since / Until optionally clamp the commit iteration window.
	// Zero-value means "no bound on that side".
	Since time.Time
	Until time.Time

	// LangMap overrides the default file-extension → language lookup.
	// Keys are extensions WITHOUT the leading dot ("ts", "py"), values
	// are the target language string used verbatim in the heartbeat.
	// Populated from backfill_config.lang_map by the server config and
	// merged with the compiled defaults.
	LangMap map[string]string
}

// defaults returns a copy of cfg with any zero-value tuning replaced by
// sensible defaults. Kept as a helper so Cluster and Materialize can be
// called with a bare EstimatorConfig{} and still behave.
func (cfg EstimatorConfig) defaults() EstimatorConfig {
	if cfg.ClusterGap <= 0 {
		cfg.ClusterGap = 30 * time.Minute
	}
	if cfg.PreCommitLead < 0 {
		cfg.PreCommitLead = 0
	}
	if cfg.PreCommitLead == 0 && cfg.ClusterGap > 0 {
		cfg.PreCommitLead = 15 * time.Minute
	}
	if cfg.PostCommitTail < 0 {
		cfg.PostCommitTail = 0
	}
	if cfg.PostCommitTail == 0 {
		cfg.PostCommitTail = 5 * time.Minute
	}
	if cfg.HeartbeatRate <= 0 {
		cfg.HeartbeatRate = 2 * time.Minute
	}
	return cfg
}

// EmailAllowed reports whether `email` matches the configured allowlist.
// Empty AuthorEmails means "allow everything". Comparison is
// case-insensitive because git commits commonly have mixed-case addresses
// even for the same author (Someone@Example.COM vs someone@example.com).
func (cfg EstimatorConfig) EmailAllowed(email string) bool {
	if len(cfg.AuthorEmails) == 0 {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(email))
	for _, e := range cfg.AuthorEmails {
		if strings.ToLower(strings.TrimSpace(e)) == lower {
			return true
		}
	}
	return false
}

// languageFor picks a language for a given file path via LangMap
// overrides first, then the compiled default extToLang table. Returns
// empty string when there is no recognizable extension — Cluster then
// falls back to "" and Materialize emits no `language` field.
func (cfg EstimatorConfig) languageFor(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "" {
		return ""
	}
	if cfg.LangMap != nil {
		if v, ok := cfg.LangMap[ext]; ok {
			return v
		}
	}
	if v, ok := extToLang[ext]; ok {
		return v
	}
	return ""
}

// extToLang is a small, hand-curated compile-time table of the languages
// that show up in this codebase and typical config-heavy repos. Anything
// unknown falls through to "" (no language). Users can extend via the
// per-user backfill_config.lang_map override.
//
// Chosen values match what wakatime.com reports for the same file
// extension so downstream aggregations (stats/languages, projects
// language breakdown) blend cleanly with real Wakatime data.
var extToLang = map[string]string{
	"go":    "Go",
	"ts":    "TypeScript",
	"tsx":   "TSX",
	"js":    "JavaScript",
	"jsx":   "JSX",
	"py":    "Python",
	"rb":    "Ruby",
	"rs":    "Rust",
	"java":  "Java",
	"kt":    "Kotlin",
	"swift": "Swift",
	"c":     "C",
	"h":     "C",
	"cpp":   "C++",
	"cc":    "C++",
	"hpp":   "C++",
	"cs":    "C#",
	"php":   "PHP",
	"sh":    "Bash",
	"bash":  "Bash",
	"zsh":   "Bash",
	"fish":  "Fish",
	"sql":   "SQL",
	"html":  "HTML",
	"css":   "CSS",
	"scss":  "SCSS",
	"sass":  "Sass",
	"less":  "Less",
	"json":  "JSON",
	"yaml":  "YAML",
	"yml":   "YAML",
	"toml":  "TOML",
	"md":    "Markdown",
	"tex":   "TeX",
	"lua":   "Lua",
	"vim":   "VimL",
	"el":    "Emacs Lisp",
	"hs":    "Haskell",
	"ex":    "Elixir",
	"exs":   "Elixir",
	"erl":   "Erlang",
	"clj":   "Clojure",
	"dart":  "Dart",
	"scala": "Scala",
	"nix":   "Nix",
	"proto": "Protocol Buffer",
	"tf":    "Terraform",
	"hcl":   "HCL",
	"dockerfile": "Docker",
}
