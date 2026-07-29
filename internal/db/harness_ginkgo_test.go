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
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
