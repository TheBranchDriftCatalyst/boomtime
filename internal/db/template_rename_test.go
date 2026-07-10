package db

import (
	"context"
	"testing"
	"time"
)

// TestNormalizeTemplate: `$N` -> `\N`, `$$` -> `$`, and existing `\N` untouched.
func TestNormalizeTemplate(t *testing.T) {
	cases := []struct{ in, want string }{
		{`\1`, `\1`},               // already Postgres form
		{`$1`, `\1`},               // shell form -> Postgres
		{`$1-$2`, `\1-\2`},         // multiple groups
		{`pre-$1`, `pre-\1`},       // prefix preserved
		{`$$1`, `$1`},              // literal `$` then digit stays `$1` (not a backref)
		{`a$b`, `a$b`},             // `$` not followed by digit is literal
		{`$0`, `\0`},               // whole match
		{`x`, `x`},                 // no refs
		{``, ``},                   // empty
		{`\1 and $2`, `\1 and \2`}, // mixed forms
	}
	for _, c := range cases {
		if got := NormalizeTemplate(c.in); got != c.want {
			t.Errorf("NormalizeTemplate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTemplateRenameStripsPrefix drives the core feature end-to-end at the DB
// layer: a template rule `^@(.*)$ -> \1` strips a leading `@` from several `@x`
// projects. It merges into the un-prefixed names across GetUserActivity (raw
// scan) AND GetUserActivityRollup (pre-aggregated), leaves non-matching projects
// untouched, preserves raw heartbeats, and reverts cleanly on delete.
func TestTemplateRenameStripsPrefix(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "tmpl")
	sender := f.Sender()

	base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
	// @swarm-graph and @drogon should lose the '@'; plain-project is untouched.
	swarm, _ := f.Block(hbSeed{project: "@swarm-graph", language: "Go", editor: "vim"}, base, 4, 120)
	drogon, _ := f.Block(hbSeed{project: "@drogon", language: "Go", editor: "vim"}, base.Add(time.Hour), 3, 120)
	plain, _ := f.Block(hbSeed{project: "plain-project", language: "Go", editor: "vim"}, base.Add(2*time.Hour), 2, 120)
	f.RefreshRollup(base.AddDate(0, 0, -1))

	t0 := base.AddDate(0, 0, -1)
	t1 := base.AddDate(0, 0, 1)

	// --- Baseline (no rule): raw prefixed names, no merge. ---
	baseRows, err := d.GetUserActivity(ctx, sender, t0, t1, 15, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	baseProj := axisTotals(baseRows, "project")
	if baseProj["@swarm-graph"] != swarm || baseProj["@drogon"] != drogon {
		t.Fatalf("baseline projects = %+v, want @swarm-graph=%d @drogon=%d", baseProj, swarm, drogon)
	}

	// --- Create the template rename and load it. ---
	id := createTemplateRename(t, d, ctx, sender, "project", "^@(.*)$", `\1`)
	rs := loadRenames(t, d, ctx, sender)
	if !rs.HasAxis("project") {
		t.Fatal("expected a project rename after createTemplateRename")
	}

	// assertMerged verifies the '@' is stripped and totals are conserved/relabeled.
	assertMerged := func(label string, rows []StatRow) {
		t.Helper()
		p := axisTotals(rows, "project")
		if _, ok := p["@swarm-graph"]; ok {
			t.Errorf("%s: '@swarm-graph' should be relabeled away; got %+v", label, p)
		}
		if _, ok := p["@drogon"]; ok {
			t.Errorf("%s: '@drogon' should be relabeled away", label)
		}
		if p["swarm-graph"] != swarm {
			t.Errorf("%s: 'swarm-graph' = %d, want %d", label, p["swarm-graph"], swarm)
		}
		if p["drogon"] != drogon {
			t.Errorf("%s: 'drogon' = %d, want %d", label, p["drogon"], drogon)
		}
		if p["plain-project"] != plain {
			t.Errorf("%s: 'plain-project' = %d, want %d (unaffected)", label, p["plain-project"], plain)
		}
		if got, want := grandTotal(rows), swarm+drogon+plain; got != want {
			t.Errorf("%s: grand total = %d, want %d (conserved)", label, got, want)
		}
	}

	// --- Raw-scan path (limit != 15 forces GetUserActivity). ---
	rawRows, err := d.GetUserActivity(ctx, sender, t0, t1, 30, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMerged("GetUserActivity", rawRows)

	// --- Rollup path (default 15-min, no hides). ---
	rollRows, err := d.GetUserActivityRollup(ctx, sender, t0, t1, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMerged("GetUserActivityRollup", rollRows)

	// --- Projects list path. ---
	projs, err := d.GetAllProjects(ctx, sender, t0, t1, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, p := range projs {
		set[p] = true
	}
	if !set["swarm-graph"] || !set["drogon"] || !set["plain-project"] {
		t.Errorf("projects list missing merged names: %+v", projs)
	}
	if set["@swarm-graph"] || set["@drogon"] {
		t.Errorf("projects list should not contain raw '@' names: %+v", projs)
	}

	// --- Raw heartbeats untouched (non-destructive). ---
	if n := rawCount(t, d, ctx, sender, "project", "@swarm-graph"); n != 5 { // 1 break + 4 attributed
		t.Errorf("raw '@swarm-graph' heartbeats = %d, want 5 (unchanged)", n)
	}
	if n := rawCount(t, d, ctx, sender, "project", "swarm-graph"); n != 0 {
		t.Errorf("no raw 'swarm-graph' rows should exist; got %d", n)
	}

	// --- Reversible: delete the rule -> baseline returns. ---
	if _, err := d.DeleteCurationRule(ctx, sender, id); err != nil {
		t.Fatal(err)
	}
	rs2 := loadRenames(t, d, ctx, sender)
	revRows, err := d.GetUserActivity(ctx, sender, t0, t1, 15, HiddenSets{}, rs2, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	rev := axisTotals(revRows, "project")
	if rev["@swarm-graph"] != swarm || rev["@drogon"] != drogon {
		t.Errorf("after delete, raw names should return: %+v", rev)
	}
	if _, ok := rev["swarm-graph"]; ok {
		t.Error("after delete, merged 'swarm-graph' should be gone")
	}
}

// TestTemplateAffectedMappedTo: /affected on a template rule returns each raw
// value paired with its regexp_replace preview (value -> mappedTo).
func TestTemplateAffectedMappedTo(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "tmplaff")
	sender := f.Sender()

	day := time.Date(2025, 6, 13, 9, 0, 0, 0, time.UTC)
	f.Seed(hbSeed{project: "@swarm-graph", language: "Go", entity: "a.go", ts: day, gap: 60})
	f.Seed(hbSeed{project: "@swarm-graph", language: "Go", entity: "a.go", ts: day.Add(time.Minute), gap: 60})
	f.Seed(hbSeed{project: "@drogon", language: "Go", entity: "b.go", ts: day.Add(2 * time.Minute), gap: 60})
	f.Seed(hbSeed{project: "plain", language: "Go", entity: "c.go", ts: day.Add(3 * time.Minute), gap: 60})

	id := createTemplateRename(t, d, ctx, sender, "project", "^@(.*)$", `\1`)
	rule, _, err := d.GetCurationRule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	vals, _, err := d.CurationAffectedValues(ctx, sender, rule, 200)
	if err != nil {
		t.Fatal(err)
	}
	mapped := map[string]string{}
	counts := map[string]int64{}
	for _, v := range vals {
		mapped[v.Value] = v.MappedTo
		counts[v.Value] = v.Count
	}
	if mapped["@swarm-graph"] != "swarm-graph" {
		t.Errorf("mappedTo['@swarm-graph'] = %q, want 'swarm-graph'", mapped["@swarm-graph"])
	}
	if mapped["@drogon"] != "drogon" {
		t.Errorf("mappedTo['@drogon'] = %q, want 'drogon'", mapped["@drogon"])
	}
	if counts["@swarm-graph"] != 2 || counts["@drogon"] != 1 {
		t.Errorf("counts = %+v, want swarm=2 drogon=1", counts)
	}
	if _, ok := mapped["plain"]; ok {
		t.Error("'plain' does not match ^@ and must not appear in affected values")
	}
}

// TestExactAndRegexAffectedMappedToFixed: for exact/regex renames, mappedTo is
// the fixed new_value (not a per-value template result).
func TestExactAndRegexAffectedMappedToFixed(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "fixmap")
	sender := f.Sender()

	day := time.Date(2025, 6, 14, 9, 0, 0, 0, time.UTC)
	f.Seed(hbSeed{project: "Meet - Standup", language: "Go", entity: "a.go", ts: day, gap: 60})
	f.Seed(hbSeed{project: "Meet - Planning", language: "Go", entity: "b.go", ts: day.Add(time.Minute), gap: 60})
	f.Seed(hbSeed{project: "solo", language: "Go", entity: "c.go", ts: day.Add(2 * time.Minute), gap: 60})

	rxID := createRegexRename(t, d, ctx, sender, "project", "^Meet", "Meeting")
	rxRule, _, _ := d.GetCurationRule(ctx, rxID)
	rxVals, _, err := d.CurationAffectedValues(ctx, sender, rxRule, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rxVals {
		if v.MappedTo != "Meeting" {
			t.Errorf("regex mappedTo[%q] = %q, want fixed 'Meeting'", v.Value, v.MappedTo)
		}
	}

	exID := createRename(t, d, ctx, sender, "project", "solo", "Misc")
	exRule, _, _ := d.GetCurationRule(ctx, exID)
	exVals, _, err := d.CurationAffectedValues(ctx, sender, exRule, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(exVals) != 1 || exVals[0].MappedTo != "Misc" {
		t.Errorf("exact affected = %+v, want mappedTo 'Misc'", exVals)
	}
}

// TestTemplateValidation: a good template validates; a bad backref (\9 for a
// single-group pattern) is rejected by ValidateTemplate.
func TestTemplateValidation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	if err := d.ValidateTemplate(ctx, "^@(.*)$", `\1`); err != nil {
		t.Errorf("valid template rejected: %v", err)
	}
	if err := d.ValidateTemplate(ctx, "^@(.*)$", `\9`); err == nil {
		t.Error("bad backref \\9 (only 1 group) should be rejected")
	}
	if err := d.ValidateTemplate(ctx, "(unterminated", `\1`); err == nil {
		t.Error("uncompilable pattern should be rejected")
	}
}
