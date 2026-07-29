// harness_ginkgo_test.go — ginkgo-flavored wrappers around the harness_test.go
// helpers (which take *testing.T and can therefore only be called from stdlib
// tests). This file mirrors the shape of the stdlib helpers under names that
// end in `G`, so a ginkgo It can call `openTestDBG()` / `newSenderG(d, "pfx")`
// / `cleanupSenderG(d, ctx, sender)` and get the same behavior. All failures
// route through gomega's `Fail`, and cleanup is registered with
// `ginkgo.DeferCleanup` (so it runs at the enclosing It/Context boundary).
package db

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// openTestDB is the stdlib-flavored counterpart to openTestDBG. Stdlib
// tests (still allowed alongside ginkgo — see internal/db/auth_test.go
// and internal/db/award_ledger_test.go) call this seam so they can share
// the isolated DB harness without going through ginkgo. Skips (t.Skip)
// rather than fails when the DB is unreachable — matches openTestDBG.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	if !dbReady {
		t.Skip("skipping: isolated test database unavailable: " + dbSkipMsg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := New(ctx, testDatabaseURL())
	if err != nil {
		t.Skipf("skipping: could not open %s: %v", testDBName, err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// openTestDBG mirrors openTestDB but for ginkgo Its. Skips the current spec
// when the isolated test DB is unavailable, and registers a DeferCleanup to
// close the pool at the end of the spec.
func openTestDBG() *DB {
	if !dbReady {
		ginkgo.Skip("skipping: isolated test database unavailable: " + dbSkipMsg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := New(ctx, testDatabaseURL())
	if err != nil {
		ginkgo.Skip("skipping: could not open " + testDBName + ": " + err.Error())
	}
	ginkgo.DeferCleanup(func() { database.Close() })
	return database
}

// ensureUserG inserts the users row a heartbeat's sender FK requires.
func ensureUserG(d *DB, ctx context.Context, sender string) {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		sender)
	Expect(err).NotTo(HaveOccurred())
}

// ensureProjectsG inserts the projects rows a heartbeat's (sender,project) FK needs.
func ensureProjectsG(d *DB, ctx context.Context, sender string, names ...string) {
	for _, n := range names {
		_, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, sender, n)
		Expect(err).NotTo(HaveOccurred())
	}
}

// cleanupSenderG registers a DeferCleanup that deletes every row a sender owns.
// Call this INSIDE an It / BeforeEach body — DeferCleanup outside a spec node panics.
func cleanupSenderG(d *DB, ctx context.Context, sender string) {
	ginkgo.DeferCleanup(func() { deleteSenderRows(d, ctx, sender) })
}

// SenderFixtureG wraps SenderFixture with ginkgo-safe error routing (Fail via
// gomega Expect rather than t.Fatal). It reuses SenderFixture's insert/rollup
// primitives by pointing back to a fresh fixture with a background Ctx.
type SenderFixtureG struct {
	db   *DB
	ctx  context.Context
	name string
}

func (f *SenderFixtureG) Sender() string       { return f.name }
func (f *SenderFixtureG) DB() *DB              { return f.db }
func (f *SenderFixtureG) Ctx() context.Context { return f.ctx }

// Projects ensures project rows for this sender exist.
func (f *SenderFixtureG) Projects(names ...string) *SenderFixtureG {
	ensureProjectsG(f.db, f.ctx, f.name, names...)
	return f
}

// Seed inserts one heartbeat, ensuring its project row is present.
func (f *SenderFixtureG) Seed(h hbSeed) *SenderFixtureG {
	if h.project != "" {
		ensureProjectsG(f.db, f.ctx, f.name, h.project)
	}
	insertSeedG(f.db, f.ctx, f.name, h)
	return f
}

// Block mirrors SenderFixture.Block but routes errors through gomega.
func (f *SenderFixtureG) Block(tmpl hbSeed, startTS time.Time, n int, each int64) (attributed int64, rows int) {
	if tmpl.project != "" {
		ensureProjectsG(f.db, f.ctx, f.name, tmpl.project)
	}
	brk := tmpl
	brk.ts = startTS
	brk.gap = 999999
	insertSeedG(f.db, f.ctx, f.name, brk)
	for i := 0; i < n; i++ {
		h := tmpl
		h.ts = startTS.Add(time.Duration(i+1) * time.Minute)
		h.gap = each
		insertSeedG(f.db, f.ctx, f.name, h)
	}
	return int64(n) * each, n + 1
}

// RefreshRollup rebuilds the rollup for this sender from the given time.
func (f *SenderFixtureG) RefreshRollup(since time.Time) *SenderFixtureG {
	err := f.db.RefreshRollup(f.ctx, f.name, since)
	Expect(err).NotTo(HaveOccurred())
	return f
}

// RecomputeGaps recomputes gap_seconds for this sender from the given time.
func (f *SenderFixtureG) RecomputeGaps(since time.Time) *SenderFixtureG {
	err := f.db.RecomputeGaps(f.ctx, f.name, since)
	Expect(err).NotTo(HaveOccurred())
	return f
}

// newSenderG makes a unique sender, inserts its user row, registers cleanup,
// and returns a fixture builder. Ginkgo analog of newSender().
func newSenderG(d *DB, prefix string) *SenderFixtureG {
	ctx := context.Background()
	name := mkSender(prefix)
	cleanupSenderG(d, ctx, name)
	ensureUserG(d, ctx, name)
	return &SenderFixtureG{db: d, ctx: ctx, name: name}
}

// insertSeedG inserts one heartbeat with gomega error routing.
func insertSeedG(d *DB, ctx context.Context, sender string, h hbSeed) {
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
	Expect(err).NotTo(HaveOccurred())
}

// seedAxisBlockG mirrors seedAxisBlock but routes errors through gomega.
func seedAxisBlockG(d *DB, ctx context.Context, sender, axis, val string, startTS time.Time, n int, each int64) (attributed int64, rowCount int) {
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
	ensureProjectsG(d, ctx, sender, tmpl.project)
	f := &SenderFixtureG{db: d, ctx: ctx, name: sender}
	return f.Block(tmpl, startTS, n, each)
}

// seedHBG mirrors seedHB (ginkgo variant).
func seedHBG(d *DB, ctx context.Context, sender, project, lang string, ts time.Time) {
	insertSeedG(d, ctx, sender, hbSeed{project: project, language: lang, entity: "a.go", ts: ts})
}

// createRenameG stores an EXACT rename rule and returns its id (ginkgo variant).
func createRenameG(d *DB, ctx context.Context, sender, axis, match, newVal string) int {
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", "exact", match, &newVal)
	Expect(err).NotTo(HaveOccurred())
	return rule.ID
}

// createRegexRenameG stores a REGEX rename rule (ginkgo variant).
func createRegexRenameG(d *DB, ctx context.Context, sender, axis, pattern, newVal string) int {
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", "regex", pattern, &newVal)
	Expect(err).NotTo(HaveOccurred())
	return rule.ID
}

// createTemplateRenameG stores a TEMPLATE rename rule (ginkgo variant).
func createTemplateRenameG(d *DB, ctx context.Context, sender, axis, pattern, tmpl string) int {
	norm := NormalizeTemplate(tmpl)
	rule, err := d.CreateCurationRule(ctx, sender, axis, "rename", "template", pattern, &norm)
	Expect(err).NotTo(HaveOccurred())
	return rule.ID
}

// loadRenamesG loads rename sets (ginkgo variant).
func loadRenamesG(d *DB, ctx context.Context, sender string) RenameSets {
	rs, err := d.LoadRenameSets(ctx, sender)
	Expect(err).NotTo(HaveOccurred())
	return rs
}

// rawCountG returns the number of raw heartbeats for sender where col=val.
func rawCountG(d *DB, ctx context.Context, sender, col, val string) int {
	var n int
	q := "SELECT count(*) FROM heartbeats WHERE sender=$1 AND " + col + "=$2"
	err := d.Pool.QueryRow(ctx, q, sender, val).Scan(&n)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// scalarCountG runs a `SELECT count(*) ... WHERE ...=$1` with sender bound.
func scalarCountG(d *DB, ctx context.Context, q, sender string) int {
	var n int
	err := d.Pool.QueryRow(ctx, q, sender).Scan(&n)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func mkSender(prefix string) string {
	return prefix + "_" + time.Now().Format("150405.000000000")
}

func deleteSenderRows(d *DB, ctx context.Context, sender string) {
	_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM spaces WHERE owner=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM badges WHERE username=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM widget_links WHERE username=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE owner=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner=$1`, sender)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
}

func cleanupSender(t *testing.T, d *DB, ctx context.Context, sender string) {
	t.Cleanup(func() { deleteSenderRows(d, ctx, sender) })
}

func ensureUser(t *testing.T, d *DB, ctx context.Context, sender string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		sender); err != nil {
		t.Fatal(err)
	}
}

func ensureProjects(t *testing.T, d *DB, ctx context.Context, sender string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, sender, n); err != nil {
			t.Fatal(err)
		}
	}
}

func newSender(t *testing.T, d *DB, prefix string) *SenderFixture {
	t.Helper()
	ctx := context.Background()
	name := mkSender(prefix)
	cleanupSender(t, d, ctx, name)
	ensureUser(t, d, ctx, name)
	return &SenderFixture{t: t, db: d, ctx: ctx, name: name}
}

type hbSeed struct {
	project, language, editor, plugin, machine, platform, branch, category string
	ty                                                                     string
	entity                                                                 string
	isWrite                                                                *bool
	ts                                                                     time.Time
	gap                                                                    int64 // gap_seconds (<= limit*60 counts as attributed)
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

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

type SenderFixture struct {
	t    *testing.T
	db   *DB
	ctx  context.Context
	name string
}

func (f *SenderFixture) Sender() string { return f.name }

func (f *SenderFixture) DB() *DB { return f.db }

func (f *SenderFixture) Ctx() context.Context { return f.ctx }

func (f *SenderFixture) Projects(names ...string) *SenderFixture {
	ensureProjects(f.t, f.db, f.ctx, f.name, names...)
	return f
}

func (f *SenderFixture) Seed(h hbSeed) *SenderFixture {
	f.t.Helper()
	if h.project != "" {
		ensureProjects(f.t, f.db, f.ctx, f.name, h.project)
	}
	insertSeed(f.t, f.db, f.ctx, f.name, h)
	return f
}

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

func (f *SenderFixture) RefreshRollup(since time.Time) *SenderFixture {
	f.t.Helper()
	if err := f.db.RefreshRollup(f.ctx, f.name, since); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func (f *SenderFixture) RecomputeGaps(since time.Time) *SenderFixture {
	f.t.Helper()
	if err := f.db.RecomputeGaps(f.ctx, f.name, since); err != nil {
		f.t.Fatal(err)
	}
	return f
}

func seedHB(t *testing.T, d *DB, ctx context.Context, sender, project, lang string, ts time.Time) {
	t.Helper()
	insertSeed(t, d, ctx, sender, hbSeed{project: project, language: lang, entity: "a.go", ts: ts})
}

func mkHiddenSets(byAxis map[string][]string) HiddenSets {
	m := make(map[string][]string, len(byAxis))
	for k, v := range byAxis {
		if len(v) > 0 {
			m[k] = v
		}
	}
	return HiddenSets{byAxis: m}
}

func totalStatSeconds(rows []StatRow) int64 {
	var s int64
	for _, r := range rows {
		s += r.TotalSeconds
	}
	return s
}

func grandTotal(rows []StatRow) int64 { return totalStatSeconds(rows) }

func axisTotals(rows []StatRow, axis string) map[string]int64 {
	secs := map[string]int64{}
	for _, r := range rows {
		secs[statRowAxis(r, axis)] += r.TotalSeconds
	}
	return secs
}

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
