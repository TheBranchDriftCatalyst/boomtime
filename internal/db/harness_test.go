package db

import (
	"context"
	"testing"
	"time"
)

// harness_test.go is the SINGLE source of truth for the internal/db test seed
// builders and assertion helpers ("AIO" harness). All package-db tests reuse
// these instead of re-writing per-test insert/seed boilerplate. The isolated-DB
// access (openTestDB / truncateAll / TestMain) lives in main_test.go and is used
// as-is. External/handler tests use internal/dbtest (which imports db) — this
// file cannot live outside package db without an import cycle.

// ---- identifiers & lifecycle ----

// mkSender returns a unique sender name so parallel tests never collide.
func mkSender(prefix string) string {
	return prefix + "_" + time.Now().Format("150405.000000000")
}

// cleanupSender registers a t.Cleanup that deletes every row a sender owns across
// the mutable tables (children before parents).
func cleanupSender(t *testing.T, d *DB, ctx context.Context, sender string) {
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM spaces WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM badges WHERE username=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner=$1`, sender)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
	})
}

// ensureUser inserts the users row a heartbeat's sender FK requires.
func ensureUser(t *testing.T, d *DB, ctx context.Context, sender string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		sender); err != nil {
		t.Fatal(err)
	}
}

// ensureProjects inserts the projects rows a heartbeat's (sender,project) FK needs.
func ensureProjects(t *testing.T, d *DB, ctx context.Context, sender string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, sender, n); err != nil {
			t.Fatal(err)
		}
	}
}

// newSender is the fluent entry point: it makes a unique sender, inserts its user
// row, and registers cleanup, returning a SenderFixture builder.
func newSender(t *testing.T, d *DB, prefix string) *SenderFixture {
	t.Helper()
	ctx := context.Background()
	name := mkSender(prefix)
	cleanupSender(t, d, ctx, name)
	ensureUser(t, d, ctx, name)
	return &SenderFixture{t: t, db: d, ctx: ctx, name: name}
}

// ---- fluent seed builder ----

// hbSeed is a fully-specified heartbeat row. Empty string fields become SQL NULL
// (via nz); gap_seconds is seeded directly so expected attributed totals are exact.
type hbSeed struct {
	project, language, editor, plugin, machine, platform, branch, category string
	ty                                                                     string
	entity                                                                 string
	isWrite                                                                *bool
	ts                                                                     time.Time
	gap                                                                    int64 // gap_seconds (<= limit*60 counts as attributed)
}

// nz maps "" -> nil so NULL columns stay NULL (not 'Other').
func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// insertSeed inserts one heartbeat. ty defaults to 'file', entity to 'a.go'.
func insertSeed(t *testing.T, d *DB, ctx context.Context, sender string, h hbSeed) {
	t.Helper()
	ty := h.ty
	if ty == "" {
		ty = "file"
	}
	entity := h.entity
	if entity == "" {
		entity = "a.go"
	}
	var isWrite any
	if h.isWrite != nil {
		isWrite = *h.isWrite
	}
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, project, language, editor, plugin, machine, platform, branch, category,
		   entity, ty, is_write, time_sent, user_agent, gap_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'ua',$14)`,
		sender, nz(h.project), nz(h.language), nz(h.editor), nz(h.plugin), nz(h.machine),
		nz(h.platform), nz(h.branch), nz(h.category), entity, ty, isWrite, h.ts, h.gap)
	if err != nil {
		t.Fatal(err)
	}
}

// SenderFixture builds heartbeats and derived data for one owner-scoped sender.
type SenderFixture struct {
	t    *testing.T
	db   *DB
	ctx  context.Context
	name string
}

func (f *SenderFixture) Sender() string       { return f.name }
func (f *SenderFixture) DB() *DB              { return f.db }
func (f *SenderFixture) Ctx() context.Context { return f.ctx }
func (f *SenderFixture) Projects(names ...string) *SenderFixture {
	ensureProjects(f.t, f.db, f.ctx, f.name, names...)
	return f
}

// Seed inserts one heartbeat (sender auto-filled) and creates its project row.
func (f *SenderFixture) Seed(h hbSeed) *SenderFixture {
	f.t.Helper()
	if h.project != "" {
		ensureProjects(f.t, f.db, f.ctx, f.name, h.project)
	}
	insertSeed(f.t, f.db, f.ctx, f.name, h)
	return f
}

// Block seeds a leading break beat (gap 999999, unattributed) then n attributed
// beats of `each` seconds, 1 minute apart, all sharing the template's fields
// (project/branch/language/... ) starting at startTS. Returns attributed total
// (n*each) and rows inserted (n+1). This is the workhorse for exact-number tests.
func (f *SenderFixture) Block(tmpl hbSeed, startTS time.Time, n int, each int64) (attributed int64, rows int) {
	f.t.Helper()
	if tmpl.project != "" {
		ensureProjects(f.t, f.db, f.ctx, f.name, tmpl.project)
	}
	brk := tmpl
	brk.ts = startTS
	brk.gap = 999999
	insertSeed(f.t, f.db, f.ctx, f.name, brk)
	for i := 0; i < n; i++ {
		h := tmpl
		h.ts = startTS.Add(time.Duration(i+1) * time.Minute)
		h.gap = each
		insertSeed(f.t, f.db, f.ctx, f.name, h)
	}
	return int64(n) * each, n + 1
}

// RefreshRollup rebuilds the rollup for this sender from the given time.
func (f *SenderFixture) RefreshRollup(since time.Time) *SenderFixture {
	f.t.Helper()
	if err := f.db.RefreshRollup(f.ctx, f.name, since); err != nil {
		f.t.Fatal(err)
	}
	return f
}

// RecomputeGaps recomputes gap_seconds for this sender from the given time.
func (f *SenderFixture) RecomputeGaps(since time.Time) *SenderFixture {
	f.t.Helper()
	if err := f.db.RecomputeGaps(f.ctx, f.name, since); err != nil {
		f.t.Fatal(err)
	}
	return f
}

// ---- legacy per-axis block seeder (kept for existing tests) ----

// seedAxisBlock seeds a break beat + n attributed beats of `each` seconds, varying
// only the given axis to `val` (project/language/editor). Returns attributed
// total and rows.
func seedAxisBlock(t *testing.T, d *DB, ctx context.Context, sender, axis, val string, startTS time.Time, n int, each int64) (attributed int64, rowCount int) {
	t.Helper()
	tmpl := hbSeed{
		project: "P", language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding",
	}
	switch axis {
	case "project":
		tmpl.project = val
	case "language":
		tmpl.language = val
	case "editor":
		tmpl.editor = val
	}
	ensureProjects(t, d, ctx, sender, tmpl.project)
	f := &SenderFixture{t: t, db: d, ctx: ctx, name: sender}
	return f.Block(tmpl, startTS, n, each)
}

// seedHB inserts a single file heartbeat with a chosen project+language (no gap).
func seedHB(t *testing.T, d *DB, ctx context.Context, sender, project, lang string, ts time.Time) {
	t.Helper()
	insertSeed(t, d, ctx, sender, hbSeed{project: project, language: lang, entity: "a.go", ts: ts})
}

// ---- rule helpers ----

// createRename stores an EXACT rename rule and returns its id.
func createRename(t *testing.T, d *DB, ctx context.Context, sender, axis, match, newVal string) int {
	t.Helper()
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", "exact", match, &newVal)
	if err != nil {
		t.Fatalf("createRename %s %s->%s: %v", axis, match, newVal, err)
	}
	return rule.ID
}

// createRegexRename stores a REGEX rename rule and returns its id.
func createRegexRename(t *testing.T, d *DB, ctx context.Context, sender, axis, pattern, newVal string) int {
	t.Helper()
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", "regex", pattern, &newVal)
	if err != nil {
		t.Fatalf("createRegexRename %s /%s/->%s: %v", axis, pattern, newVal, err)
	}
	return rule.ID
}

// createTemplateRename stores a TEMPLATE rename rule (regex pattern + a
// regexp_replace replacement template referencing capture groups) and returns
// its id. `tmpl` is normalized (`$N`->`\N`) exactly as the handler would.
func createTemplateRename(t *testing.T, d *DB, ctx context.Context, sender, axis, pattern, tmpl string) int {
	t.Helper()
	norm := NormalizeTemplate(tmpl)
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", "template", pattern, &norm)
	if err != nil {
		t.Fatalf("createTemplateRename %s /%s/->%q: %v", axis, pattern, tmpl, err)
	}
	return rule.ID
}

func loadRenames(t *testing.T, d *DB, ctx context.Context, sender string) RenameSets {
	t.Helper()
	rs, err := d.LoadRenameSets(ctx, sender)
	if err != nil {
		t.Fatalf("LoadRenameSets: %v", err)
	}
	return rs
}

// mkHiddenSets builds a HiddenSets from an axis->values map (test helper).
func mkHiddenSets(byAxis map[string][]string) HiddenSets {
	m := make(map[string][]string, len(byAxis))
	for k, v := range byAxis {
		if len(v) > 0 {
			m[k] = v
		}
	}
	return HiddenSets{byAxis: m}
}

// ---- raw-count / scalar helpers ----

// rawCount returns the number of raw heartbeats for sender where col=val.
func rawCount(t *testing.T, d *DB, ctx context.Context, sender, col, val string) int {
	t.Helper()
	var n int
	q := "SELECT count(*) FROM heartbeats WHERE sender=$1 AND " + col + "=$2"
	if err := d.Pool.QueryRow(ctx, q, sender, val).Scan(&n); err != nil {
		t.Fatalf("rawCount %s=%s: %v", col, val, err)
	}
	return n
}

// scalarCount runs a `SELECT count(*) ... WHERE ...=$1` with the sender bound to $1.
func scalarCount(t *testing.T, d *DB, ctx context.Context, q, sender string) int {
	t.Helper()
	var n int
	if err := d.Pool.QueryRow(ctx, q, sender).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---- StatRow assertion helpers ----

func totalStatSeconds(rows []StatRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.TotalSeconds
	}
	return s
}

// grandTotal is an alias for totalStatSeconds used by the merge tests.
func grandTotal(rows []StatRow) int64 { return totalStatSeconds(rows) }

// axisTotals maps axis-value -> summed attributed seconds.
func axisTotals(rows []StatRow, axis string) map[string]int64 {
	secs := map[string]int64{}
	for _, r := range rows {
		secs[statRowAxis(r, axis)] += r.TotalSeconds
	}
	return secs
}

// statRowHasAxis reports whether get_user_activity's StatRow carries a column for
// this axis (plugin/category are not selected there).
func statRowHasAxis(axis string) bool {
	switch axis {
	case "project", "language", "editor", "machine", "platform", "branch":
		return true
	}
	return false
}

func statRowAxis(r StatRow, axis string) string {
	switch axis {
	case "project":
		return r.Project
	case "language":
		return r.Language
	case "editor":
		return r.Editor
	case "machine":
		return r.Machine
	case "platform":
		return r.Platform
	case "branch":
		return r.Branch
	}
	return ""
}

func statRowsContain(rows []StatRow, axis, val string) bool {
	for _, r := range rows {
		if statRowAxis(r, axis) == val {
			return true
		}
	}
	return false
}

func hasProject(rows []StatRow, p string) bool { return statRowsContain(rows, "project", p) }

// ---- other result-set sum/lookup helpers ----

func sumPunch(cells []PunchcardCell) int64 {
	var s int64
	for _, c := range cells {
		s += c.Seconds
	}
	return s
}

func sumSessions(rows []SessionRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.Seconds
	}
	return s
}

func sumMomentum(rows []MomentumRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.Seconds
	}
	return s
}

func lbRowAxis(r LeaderboardRow, axis string) string {
	switch axis {
	case "project":
		return r.Project
	case "language":
		return r.Language
	}
	return ""
}

func groupsContain(groups []ExploreGroup, val string) bool {
	for _, g := range groups {
		if g.Value != nil && *g.Value == val {
			return true
		}
	}
	return false
}

func listContains(items []ExploreRow, axis, val string) bool {
	for _, r := range items {
		if exploreRowAxis(r, axis) == val {
			return true
		}
	}
	return false
}

func exploreRowAxis(r ExploreRow, axis string) string {
	get := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	switch axis {
	case "project":
		return get(r.Project)
	case "language":
		return get(r.Language)
	case "editor":
		return get(r.Editor)
	case "plugin":
		return get(r.Plugin)
	case "machine":
		return get(r.Machine)
	case "platform":
		return get(r.Platform)
	case "branch":
		return get(r.Branch)
	case "category":
		return get(r.Category)
	}
	return ""
}
