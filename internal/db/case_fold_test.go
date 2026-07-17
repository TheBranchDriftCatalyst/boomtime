package db

import (
	"strings"
	"testing"
	"time"
)

// TestCaseFoldAggregationAcrossAxes is the regression for the case-sensitive
// aggregation bug: the Category dashboard showed "writing docs" and
// "Writing Docs" as two separate rows even though the user meant them as one.
// The fix is that every axis (project, language, editor, plugin, machine,
// platform, branch, category, entity) is case-folded at the aggregation
// GROUP BY, and a canonical display casing is picked via MODE() WITHIN GROUP.
//
// This test seeds three heartbeats per axis whose only difference is casing,
// then asserts the aggregation collapses them to ONE row (three of the same
// case-folded key) with the summed total, and returns a display label that is
// one of the input casings.
//
// Each sub-test uses its OWN sender so a per-axis seed's gap calculations
// aren't contaminated by earlier sub-tests' beats (recomputeGaps runs over
// the whole sender's timeline).
func TestCaseFoldAggregationAcrossAxes(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	// Seed helper: three heartbeats in a case-variant block. The first is a
	// "break" (huge gap → 0 attributed); the next two carry the value in
	// different casings and share the timeLimit-eligible gap of 100s each.
	seedCaseBlock := func(sender, axis, low, mid, up string, start time.Time) int64 {
		t.Helper()
		ctx := t.Context()
		tmpl := hbSeed{
			project: "P", language: "Go", editor: "vim", plugin: "pl",
			machine: "m", platform: "linux", branch: "main", category: "Coding",
			entity: "a.go",
		}
		set := func(tmpl *hbSeed, v string) {
			switch axis {
			case "project":
				tmpl.project = v
			case "language":
				tmpl.language = v
			case "editor":
				tmpl.editor = v
			case "plugin":
				tmpl.plugin = v
			case "machine":
				tmpl.machine = v
			case "platform":
				tmpl.platform = v
			case "branch":
				tmpl.branch = v
			case "category":
				tmpl.category = v
			case "entity":
				tmpl.entity = v
			}
		}
		// Break beat (unattributed) at the block's start.
		brk := tmpl
		set(&brk, low)
		brk.ts = start
		brk.gap = 999999
		if axis == "project" {
			ensureProjects(t, d, ctx, sender, low)
		}
		insertSeed(t, d, ctx, sender, brk)
		// Two attributed beats: same value, two different casings (mid and up).
		for i, v := range []string{mid, up} {
			h := tmpl
			set(&h, v)
			h.ts = start.Add(time.Duration(i+1) * time.Minute)
			h.gap = 100
			if axis == "project" {
				ensureProjects(t, d, ctx, sender, v)
			}
			insertSeed(t, d, ctx, sender, h)
		}
		return 200 // 2 attributed beats * 100s each
	}

	cases := []struct {
		axis, low, mid, up string
	}{
		{"category", "writing docs", "Writing Docs", "WRITING DOCS"},
		{"project", "myproject", "MyProject", "MYPROJECT"},
		{"language", "go", "Go", "GO"},
		{"editor", "vscode", "VSCode", "VSCODE"},
		{"platform", "linux", "Linux", "LINUX"},
		{"machine", "laptop", "Laptop", "LAPTOP"},
		{"branch", "main", "Main", "MAIN"},
	}

	day := time.Date(2025, 6, 1, 8, 0, 0, 0, time.UTC)
	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	for _, tc := range cases {
		t.Run(tc.axis, func(t *testing.T) {
			// Isolated sender per sub-test — no gap cross-contamination.
			f := newSender(t, d, "cfax_"+tc.axis)
			ctx := f.Ctx()
			sender := f.Sender()
			ensureProjects(t, d, ctx, sender, "P")

			expected := seedCaseBlock(sender, tc.axis, tc.low, tc.mid, tc.up, day)
			// Intentionally do NOT RecomputeGaps: the harness seeds gap_seconds
			// directly (100/100 attributed + 999999 break) so the expected total
			// stays exact and independent of timestamp spacing.
			// Category is not a column on StatRow, so it's checked via
			// GetCategoryDaily; every other axis has a StatRow column.
			if tc.axis == "category" {
				cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15,
					HiddenSets{}, RenameSets{}, MemberSets{}, false)
				if err != nil {
					t.Fatal(err)
				}
				var seen []string
				var total int64
				for _, c := range cats {
					if strings.EqualFold(c.Category, tc.low) {
						seen = append(seen, c.Category)
						total += c.TotalSeconds
					}
				}
				if len(seen) != 1 {
					t.Fatalf("[category] expected one folded row, got %d: %v", len(seen), seen)
				}
				if total != expected {
					t.Fatalf("[category] folded total = %d, want %d", total, expected)
				}
				return
			}
			rows, err := d.GetUserActivity(ctx, sender, start, end, 15,
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			if err != nil {
				t.Fatal(err)
			}
			totals := axisTotals(rows, tc.axis)
			// EXACTLY one canonical entry — the three case variants must have
			// collapsed into ONE row per axis in this block.
			var seen []string
			for k := range totals {
				if strings.EqualFold(k, tc.low) {
					seen = append(seen, k)
				}
			}
			if len(seen) != 1 {
				t.Fatalf("[%s] expected one folded row for %q variants, got %d: %v",
					tc.axis, tc.low, len(seen), seen)
			}
			if got := totals[seen[0]]; got != expected {
				t.Fatalf("[%s] folded total = %d, want %d", tc.axis, got, expected)
			}
			// Display must be one of the three inputs (MODE picks one).
			label := seen[0]
			ok := false
			for _, v := range []string{tc.low, tc.mid, tc.up} {
				if label == v {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("[%s] display label %q is not one of the raw inputs (%q/%q/%q)",
					tc.axis, label, tc.low, tc.mid, tc.up)
			}
		})
	}
}

// TestCaseFoldCategoryDaily specifically exercises the Category axis (which
// lives on its own row type, not StatRow). Three case variants of "coding"
// must collapse to ONE row totalling the summed attributed seconds.
func TestCaseFoldCategoryDaily(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	f := newSender(t, d, "casefoldcat")
	ctx := f.Ctx()
	sender := f.Sender()
	ensureProjects(t, d, ctx, sender, "P")

	day := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
	// Three beats: break, then two attributed at different casings of "coding".
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "P", language: "Go", editor: "vim", entity: "a.go",
		category: "coding", ts: day, gap: 999999,
	})
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "P", language: "Go", editor: "vim", entity: "a.go",
		category: "Coding", ts: day.Add(time.Minute), gap: 100,
	})
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "P", language: "Go", editor: "vim", entity: "a.go",
		category: "CODING", ts: day.Add(2 * time.Minute), gap: 100,
	})

	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// EXACTLY one row for the case-folded "coding" key.
	var seen []string
	var total int64
	for _, c := range cats {
		if strings.EqualFold(c.Category, "coding") {
			seen = append(seen, c.Category)
			total += c.TotalSeconds
		}
	}
	if len(seen) != 1 {
		t.Fatalf("expected one folded category row, got %d: %v", len(seen), seen)
	}
	if total != 200 {
		t.Fatalf("folded category total = %d, want 200", total)
	}
	// Display label must be one of the raw inputs.
	ok := false
	for _, v := range []string{"coding", "Coding", "CODING"} {
		if seen[0] == v {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("display label %q not one of the raw casings", seen[0])
	}
	// And it must match "coding" ignoring case (redundant with EqualFold above,
	// kept explicit as the acceptance check the brief calls out).
	if !strings.EqualFold(seen[0], "coding") {
		t.Fatalf("display label %q is not case-insensitively 'coding'", seen[0])
	}
}

// TestCaseFoldRollupPath: the Overview default (limit=15) hits the rollup fast
// path. Case variants stored on distinct rollup rows (rollup preserves raw
// casing) must merge in the read-side wrap.
func TestCaseFoldRollupPath(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	f := newSender(t, d, "casefoldroll")
	ctx := f.Ctx()
	sender := f.Sender()

	day := time.Date(2025, 8, 1, 10, 0, 0, 0, time.UTC)
	// Two projects that differ only in casing.
	ensureProjects(t, d, ctx, sender, "MyProject", "myproject")
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "MyProject", language: "Go", editor: "vim", entity: "a.go",
		ts: day, gap: 999999,
	})
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "MyProject", language: "Go", editor: "vim", entity: "a.go",
		ts: day.Add(time.Minute), gap: 100,
	})
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "myproject", language: "Go", editor: "vim", entity: "a.go",
		ts: day.Add(2 * time.Minute), gap: 100,
	})
	f.RefreshRollup(day.AddDate(0, 0, -1))

	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	roll, err := d.GetUserActivityRollup(ctx, sender, start, end,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	totals := axisTotals(roll, "project")
	// Only ONE case-folded row for MyProject/myproject.
	var seen []string
	for k := range totals {
		if strings.EqualFold(k, "myproject") {
			seen = append(seen, k)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("rollup: expected one folded row, got %d: %v", len(seen), seen)
	}
	if got := totals[seen[0]]; got != 200 {
		t.Fatalf("rollup folded total = %d, want 200", got)
	}
}

// TestCaseFoldEntity: the entity list and per-entity aggregation both fold
// case variants of file paths into one row (per the coordinator's scope
// widening — file paths are treated case-insensitively too).
func TestCaseFoldEntity(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	f := newSender(t, d, "casefoldent")
	ctx := f.Ctx()
	sender := f.Sender()

	day := time.Date(2025, 9, 1, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "P")

	// Three heartbeats at the same file, three casings. First is the break;
	// the next two contribute 100s each.
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "P", language: "Go", entity: "src/main.go",
		ts: day, gap: 999999,
	})
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "P", language: "Go", entity: "src/Main.go",
		ts: day.Add(time.Minute), gap: 100,
	})
	insertSeed(t, d, ctx, sender, hbSeed{
		project: "P", language: "Go", entity: "SRC/MAIN.GO",
		ts: day.Add(2 * time.Minute), gap: 100,
	})

	// Entity list — must collapse the three casings to ONE row.
	list, _, err := d.ListEntitiesByType(ctx, sender, "file", 100)
	if err != nil {
		t.Fatal(err)
	}
	var seen []EntitySummary
	for _, e := range list {
		if strings.EqualFold(e.Entity, "src/main.go") {
			seen = append(seen, e)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("entity list: expected one folded row, got %d: %v", len(seen), seen)
	}
	// Two case variants of the entity have been ingested (the break has the
	// same entity as one of them), so the count is at least 2.
	if seen[0].Count < 2 {
		t.Fatalf("folded entity count = %d, want >=2", seen[0].Count)
	}

	// Active files — same case-folding on the entity column, projects distinct
	// count uses lower(project) so a case-variant merge won't double-count.
	files, _, err := d.GetActiveFiles(ctx, sender, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), 15, 100,
		HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var mainMatches int
	for _, af := range files {
		if strings.EqualFold(af.Entity, "src/main.go") {
			mainMatches++
			// 200s total attributed on the two non-break beats.
			if af.Seconds != 200 {
				t.Fatalf("active files folded seconds = %d, want 200", af.Seconds)
			}
		}
	}
	if mainMatches != 1 {
		t.Fatalf("active files: expected one folded row, got %d", mainMatches)
	}
}

// TestCaseFoldHideCatchesVariants: a hide rule authored as "MyProject" must
// also catch rows where the raw project is "myproject" or "MYPROJECT".
func TestCaseFoldHideCatchesVariants(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	f := newSender(t, d, "casefoldhide")
	ctx := f.Ctx()
	sender := f.Sender()

	day := time.Date(2025, 10, 1, 10, 0, 0, 0, time.UTC)
	ensureProjects(t, d, ctx, sender, "MyProject", "myproject", "Keep")

	// Two beats for MyProject (100s attributed), two for myproject (100s),
	// two for Keep (100s). The hide rule "MyProject" must drop BOTH casings.
	insertSeed(t, d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day, gap: 999999})
	insertSeed(t, d, ctx, sender, hbSeed{project: "MyProject", entity: "a.go", ts: day.Add(time.Minute), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "myproject", entity: "a.go", ts: day.Add(2 * time.Minute), gap: 100})
	insertSeed(t, d, ctx, sender, hbSeed{project: "Keep", entity: "a.go", ts: day.Add(3 * time.Minute), gap: 100})

	// Hide rule authored in a THIRD casing to prove no-case-tie behavior.
	if _, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "MYPROJECT", nil); err != nil {
		t.Fatal(err)
	}
	hs, err := d.LoadHiddenSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !hs.AnyHidden() {
		t.Fatal("hide rule should register")
	}

	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)
	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, hs, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	totals := axisTotals(rows, "project")
	for k := range totals {
		if strings.EqualFold(k, "MyProject") {
			t.Fatalf("case-variant %q survived the case-insensitive hide", k)
		}
	}
	// "Keep" must still appear untouched.
	found := false
	for k := range totals {
		if strings.EqualFold(k, "Keep") {
			found = true
		}
	}
	if !found {
		t.Fatal("non-matching project 'Keep' should still be visible")
	}
}
