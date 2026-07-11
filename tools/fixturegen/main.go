// Command fixturegen reads a representative subset of a source user's real
// heartbeats (SELECT-only) from a Postgres DB and emits a heartbeat fixture.
//
// By default it DETERMINISTICALLY pseudonymizes identifying fields (project,
// entity paths, branch, machine, user_agent, dependencies) so the committed
// fixture leaks no real data while preserving referential integrity (the same
// real value always maps to the same fake value). Non-identifying fields
// (language, editor, platform, category, is_write, type, timing) are kept.
//
// Usage:
//
//	fixturegen --source postgres://... --sender panda --out testdata/heartbeats_fixture.json
//	fixturegen --raw --out testdata/heartbeats_fixture.local.json   # REAL values, gitignored
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/fixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		source    = flag.String("source", "postgres://test:test@localhost:5432/test?sslmode=disable", "source DB DSN (SELECT-only)")
		sender    = flag.String("sender", "panda", "source user to sample")
		out       = flag.String("out", "internal/db/testdata/heartbeats_fixture.json", "output path")
		anonymize = flag.Bool("anonymize", true, "deterministically pseudonymize identifying fields")
		raw       = flag.Bool("raw", false, "emit REAL values (implies --anonymize=false; use a gitignored .local.json path)")
		maxProj   = flag.Int("projects", 10, "number of top projects to include")
		maxRows   = flag.Int("max", 3000, "max heartbeats in the fixture")
		days      = flag.Int("days", 75, "trailing window (days) of the source user's most recent data")
		salt      = flag.String("salt", "boomtime-fixture-v1", "hash salt for deterministic anonymization")
	)
	flag.Parse()

	if *raw {
		*anonymize = false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, *source)
	if err != nil {
		die("connect: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		die("ping: %v", err)
	}

	rows, err := selectSubset(ctx, pool, *sender, *maxProj, *maxRows, *days)
	if err != nil {
		die("select: %v", err)
	}
	if len(rows) == 0 {
		die("no heartbeats found for sender %q", *sender)
	}

	a := &anonymizer{salt: *salt, enabled: *anonymize}
	for i := range rows {
		a.apply(&rows[i])
	}

	// Deterministic order: by time, then a stable tiebreak, so re-runs are stable.
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].TimeSent.Equal(rows[j].TimeSent) {
			return rows[i].TimeSent.Before(rows[j].TimeSent)
		}
		if rows[i].Entity != rows[j].Entity {
			return rows[i].Entity < rows[j].Entity
		}
		return derefStr(rows[i].Project) < derefStr(rows[j].Project)
	})

	// GeneratedAt is DATA-DERIVED (the latest heartbeat time), not wall-clock, so
	// re-running on the same source produces a byte-identical file (stable diffs).
	var generatedAt time.Time
	for _, h := range rows {
		if h.TimeSent.After(generatedAt) {
			generatedAt = h.TimeSent
		}
	}
	doc := fixture.File{
		Anonymized:  *anonymize,
		GeneratedAt: generatedAt.UTC(),
		Counts:      summarize(rows),
		Heartbeats:  rows,
	}

	if err := writeJSON(*out, doc); err != nil {
		die("write: %v", err)
	}

	fmt.Printf("wrote %d heartbeats to %s (anonymized=%v)\n", len(rows), *out, *anonymize)
	c := doc.Counts
	fmt.Printf("coverage: projects=%d languages=%d editors=%d machines=%d branches=%d categories=%d days=%d\n",
		c.Projects, c.Languages, c.Editors, c.Machines, c.Branches, c.Categories, c.Days)
}

// selectSubset picks a compact, representative slice: the top-N projects (by
// heartbeat count) within the sender's most-recent `days`-day window, capped at
// `maxRows` rows, ordered deterministically.
func selectSubset(ctx context.Context, pool *pgxpool.Pool, sender string, topProjects, maxRows, days int) ([]fixture.Heartbeat, error) {
	// Anchor the window on the sender's latest heartbeat so we get real, dense data.
	var maxTime time.Time
	if err := pool.QueryRow(ctx, `SELECT max(time_sent) FROM heartbeats WHERE sender=$1`, sender).Scan(&maxTime); err != nil {
		return nil, fmt.Errorf("max time: %w", err)
	}
	if maxTime.IsZero() {
		return nil, nil
	}
	windowStart := maxTime.AddDate(0, 0, -days)

	// Top-N projects by activity in the window.
	projRows, err := pool.Query(ctx, `
		SELECT project FROM heartbeats
		WHERE sender=$1 AND time_sent >= $2 AND project IS NOT NULL
		GROUP BY project ORDER BY count(*) DESC LIMIT $3`, sender, windowStart, topProjects)
	if err != nil {
		return nil, err
	}
	var projects []string
	for projRows.Next() {
		var p string
		if err := projRows.Scan(&p); err != nil {
			projRows.Close()
			return nil, err
		}
		projects = append(projects, p)
	}
	projRows.Close()
	if len(projects) == 0 {
		return nil, nil
	}

	// Pull ALL candidate heartbeats for those projects in the window (ascending,
	// deterministic), then evenly downsample across time so the fixture SPANS the
	// full window and covers many branches/categories — not just the densest days.
	rows, err := pool.Query(ctx, `
		SELECT project, language, editor, plugin, platform, machine, branch, category,
		       entity, ty, is_write, lineno, cursorpos, file_lines, dependencies, user_agent, time_sent
		FROM heartbeats
		WHERE sender=$1 AND time_sent >= $2 AND project = ANY($3)
		ORDER BY time_sent ASC, id ASC`, sender, windowStart, projects)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []fixture.Heartbeat
	for rows.Next() {
		var h fixture.Heartbeat
		var ts time.Time
		if err := rows.Scan(
			&h.Project, &h.Language, &h.Editor, &h.Plugin, &h.Platform, &h.Machine, &h.Branch, &h.Category,
			&h.Entity, &h.Type, &h.IsWrite, &h.Lineno, &h.Cursorpos, &h.FileLines, &h.Dependencies, &h.UserAgent, &ts,
		); err != nil {
			return nil, err
		}
		h.TimeSent = ts.UTC()
		all = append(all, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return downsample(all, maxRows), nil
}

// downsample keeps every row when len(rows) <= max, else takes an evenly-strided
// subset across the (time-ordered) input so the result spans the full window.
// Deterministic: same input -> same output.
func downsample(rows []fixture.Heartbeat, max int) []fixture.Heartbeat {
	if max <= 0 || len(rows) <= max {
		return rows
	}
	out := make([]fixture.Heartbeat, 0, max)
	// Stride keeps consecutive runs partly intact (so gap_seconds stays realistic
	// within a session) while still covering the whole window: take a contiguous
	// block, skip a gap, repeat. Block size ~ max/blocks.
	const blocks = 200
	blockSize := max / blocks
	if blockSize < 3 {
		blockSize = 3
	}
	stride := len(rows) / (max / blockSize)
	if stride < blockSize {
		stride = blockSize
	}
	for i := 0; i < len(rows) && len(out) < max; i += stride {
		for j := i; j < i+blockSize && j < len(rows) && len(out) < max; j++ {
			out = append(out, rows[j])
		}
	}
	return out
}

// anonymizer deterministically pseudonymizes identifying fields. The same real
// value maps to the same fake value (referential integrity), and a hash salt
// makes the mapping stable across runs but opaque.
type anonymizer struct {
	salt    string
	enabled bool
	// stable N-numbering per axis (e.g. branch-0, machine-1).
	seq map[string]map[string]int
}

func (a *anonymizer) apply(h *fixture.Heartbeat) {
	if !a.enabled {
		return
	}
	if h.Project != nil {
		v := "project-" + a.hash(*h.Project, 6)
		h.Project = &v
	}
	h.Entity = a.anonEntity(h.Entity)
	if h.Branch != nil {
		v := "branch-" + fmt.Sprint(a.number("branch", *h.Branch))
		h.Branch = &v
	}
	if h.Machine != nil {
		v := "machine-" + fmt.Sprint(a.number("machine", *h.Machine))
		h.Machine = &v
	}
	if h.Plugin != nil {
		v := "plugin-" + a.hash(*h.Plugin, 4)
		h.Plugin = &v
	}
	h.UserAgent = a.anonUserAgent(h.UserAgent)
	if len(h.Dependencies) > 0 {
		deps := make([]string, len(h.Dependencies))
		for i, d := range h.Dependencies {
			deps[i] = "dep-" + a.hash(d, 6)
		}
		h.Dependencies = deps
	}
	// Timing/line/cursor kept; they're not identifying.
}

// anonEntity keeps directory DEPTH and file EXTENSION (so language detection
// still works) while hashing each path segment and the filename stem:
//
//	src/api/claims.ts -> d/f12/a9c4.ts
func (a *anonymizer) anonEntity(entity string) string {
	if entity == "" {
		return entity
	}
	ext := path.Ext(entity)               // ".ts" (or "")
	dir, file := path.Split(entity)       // "src/api/", "claims.ts"
	stem := strings.TrimSuffix(file, ext) // "claims"

	var segs []string
	for _, s := range strings.Split(strings.Trim(dir, "/"), "/") {
		if s == "" {
			continue
		}
		segs = append(segs, a.hash(s, 3))
	}
	name := a.hash(stem, 4) + ext
	if len(segs) == 0 {
		return name
	}
	return strings.Join(segs, "/") + "/" + name
}

// anonUserAgent strips real machine/plugin specifics but keeps the editor + OS
// SHAPE so user-agent parsing (platform=[1], editor=[3], plugin=[4]) still
// produces a realistic, stable-but-fake agent. hakatime UA form:
//
//	wakatime/1.0 (Linux-5.4) go1.20 vscode/1.70 vscode-wakatime/4.0
func (a *anonymizer) anonUserAgent(ua string) string {
	tokens := strings.Split(ua, " ")
	get := func(i int) string {
		if i < len(tokens) {
			return tokens[i]
		}
		return ""
	}
	// platform=[1]: keep the OS family word but drop precise version.
	platform := get(1)
	if platform != "" {
		p := strings.Trim(platform, "()")
		if dash := strings.IndexByte(p, '-'); dash > 0 {
			p = p[:dash]
		}
		platform = "(" + p + ")"
	}
	// editor=[3]: keep the editor NAME, hash the version.
	editor := get(3)
	if editor != "" {
		name := editor
		if slash := strings.IndexByte(editor, '/'); slash > 0 {
			name = editor[:slash]
		}
		editor = name + "/x"
	}
	// plugin=[4]: keep the plugin NAME, drop version.
	plugin := get(4)
	if plugin != "" {
		name := plugin
		if slash := strings.IndexByte(plugin, '/'); slash > 0 {
			name = plugin[:slash]
		}
		plugin = name + "/x"
	}
	return strings.TrimSpace(fmt.Sprintf("wakatime/1.0 %s x %s %s", platform, editor, plugin))
}

// hash returns the first n hex chars of a salted SHA-256 of s.
func (a *anonymizer) hash(s string, n int) string {
	sum := sha256.Sum256([]byte(a.salt + "|" + s))
	h := hex.EncodeToString(sum[:])
	if n > len(h) {
		n = len(h)
	}
	return h[:n]
}

// number returns a stable per-axis integer for a value (branch-0, branch-1, ...).
// Assignment order is deterministic: sorted by the value's salted hash.
func (a *anonymizer) number(axis, value string) int {
	if a.seq == nil {
		a.seq = map[string]map[string]int{}
	}
	if a.seq[axis] == nil {
		a.seq[axis] = map[string]int{}
	}
	if n, ok := a.seq[axis][value]; ok {
		return n
	}
	// Derive a stable ordinal from the hash so numbering doesn't depend on row
	// arrival order (keeps the file stable across re-runs).
	sum := sha256.Sum256([]byte(a.salt + "|" + axis + "|" + value))
	n := int(binary.BigEndian.Uint32(sum[:4]) % 100000)
	a.seq[axis][value] = n
	return n
}

func summarize(rows []fixture.Heartbeat) fixture.Counts {
	set := func() map[string]struct{} { return map[string]struct{}{} }
	proj, lang, ed, mach, br, cat, day := set(), set(), set(), set(), set(), set(), set()
	add := func(m map[string]struct{}, p *string) {
		if p != nil {
			m[*p] = struct{}{}
		}
	}
	for _, h := range rows {
		add(proj, h.Project)
		add(lang, h.Language)
		add(ed, h.Editor)
		add(mach, h.Machine)
		add(br, h.Branch)
		add(cat, h.Category)
		day[h.TimeSent.Format("2006-01-02")] = struct{}{}
	}
	return fixture.Counts{
		Heartbeats: len(rows),
		Projects:   len(proj), Languages: len(lang), Editors: len(ed),
		Machines: len(mach), Branches: len(br), Categories: len(cat), Days: len(day),
	}
}

func writeJSON(out string, doc fixture.File) error {
	if dir := path.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(out, b, 0o644)
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fixturegen: "+format+"\n", args...)
	os.Exit(1)
}
