package db

import (
	"context"
	"testing"
	"time"
)

// afByEntity indexes ActiveFile rows by entity for assertions.
func afByEntity(files []ActiveFile) map[string]ActiveFile {
	m := make(map[string]ActiveFile, len(files))
	for _, f := range files {
		m[f.Entity] = f
	}
	return m
}

// TestActiveFilesCrossProject: a file touched by two projects reports projects=2
// with the summed attributed time; a single-project file reports projects=1;
// lynchpins (projects desc) sort first; a non-file (ty!='file') row is excluded.
func TestActiveFilesCrossProject(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "actf")
	sender := f.Sender()
	f.Projects("alpha", "beta")

	base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
	// router.py appears under BOTH alpha and beta: 120s in alpha + 60s in beta.
	f.Seed(hbSeed{project: "alpha", entity: "router.py", ty: "file", ts: base, gap: 120})
	f.Seed(hbSeed{project: "beta", entity: "router.py", ty: "file", ts: base.Add(time.Minute), gap: 60})
	// only_a.go: single project alpha, 200s (more seconds but only 1 project).
	f.Seed(hbSeed{project: "alpha", entity: "only_a.go", ty: "file", ts: base.Add(2 * time.Minute), gap: 200})
	// A non-file heartbeat on the same entity name must be excluded entirely.
	f.Seed(hbSeed{project: "beta", entity: "router.py", ty: "domain", ts: base.Add(3 * time.Minute), gap: 300})

	t0 := base.AddDate(0, 0, -1)
	t1 := base.AddDate(0, 0, 1)

	files, trunc, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if trunc {
		t.Fatal("did not expect truncation")
	}
	by := afByEntity(files)

	r, ok := by["router.py"]
	if !ok {
		t.Fatal("router.py missing")
	}
	if r.Projects != 2 {
		t.Errorf("router.py projects = %d, want 2", r.Projects)
	}
	if r.Seconds != 180 { // 120 + 60; the ty='domain' 300s row excluded
		t.Errorf("router.py seconds = %d, want 180 (non-file excluded)", r.Seconds)
	}

	a, ok := by["only_a.go"]
	if !ok {
		t.Fatal("only_a.go missing")
	}
	if a.Projects != 1 {
		t.Errorf("only_a.go projects = %d, want 1", a.Projects)
	}
	if a.Seconds != 200 {
		t.Errorf("only_a.go seconds = %d, want 200", a.Seconds)
	}

	// Lynchpins first: router.py (projects=2) must precede only_a.go (projects=1)
	// even though only_a.go has more seconds.
	if len(files) < 2 || files[0].Entity != "router.py" {
		t.Fatalf("expected router.py first (lynchpin order), got %+v", files)
	}
}

// TestActiveFilesRespectsGapCutoff: only gap_seconds <= timeLimit*60 is attributed.
func TestActiveFilesRespectsGapCutoff(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "actfgap")
	sender := f.Sender()
	f.Projects("alpha")

	base := time.Date(2025, 5, 4, 10, 0, 0, 0, time.UTC)
	f.Seed(hbSeed{project: "alpha", entity: "x.go", ts: base, gap: 120})                     // counted
	f.Seed(hbSeed{project: "alpha", entity: "x.go", ts: base.Add(time.Minute), gap: 999999}) // over cutoff, dropped

	files, _, err := d.GetActiveFiles(ctx, sender, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1), 15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	by := afByEntity(files)
	if by["x.go"].Seconds != 120 {
		t.Errorf("x.go seconds = %d, want 120 (over-cutoff gap dropped)", by["x.go"].Seconds)
	}
}

// TestActiveFilesHiddenProjectExcluded: hiding a project removes its contribution
// from a shared file's project count and time.
func TestActiveFilesHiddenProjectExcluded(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "actfhide")
	sender := f.Sender()
	f.Projects("keep", "secret")

	base := time.Date(2025, 5, 5, 10, 0, 0, 0, time.UTC)
	// shared.go touched by keep (120s) and secret (60s).
	f.Seed(hbSeed{project: "keep", entity: "shared.go", ts: base, gap: 120})
	f.Seed(hbSeed{project: "secret", entity: "shared.go", ts: base.Add(time.Minute), gap: 60})

	t0 := base.AddDate(0, 0, -1)
	t1 := base.AddDate(0, 0, 1)

	// Without hiding: projects=2, seconds=180.
	all, _, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if all[0].Projects != 2 || all[0].Seconds != 180 {
		t.Fatalf("baseline shared.go = %+v, want projects=2 seconds=180", all[0])
	}

	// Hide 'secret': shared.go drops to projects=1, seconds=120.
	hs := mkHiddenSets(map[string][]string{"project": {"secret"}})
	hidden, _, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, hs, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	by := afByEntity(hidden)
	s := by["shared.go"]
	if s.Projects != 1 {
		t.Errorf("shared.go projects after hiding secret = %d, want 1", s.Projects)
	}
	if s.Seconds != 120 {
		t.Errorf("shared.go seconds after hiding secret = %d, want 120", s.Seconds)
	}
}

// TestActiveFilesRenameMergesProjectCount: a rename that merges two raw projects
// into one display name collapses a shared file's project count accordingly (the
// remap is applied before COUNT(DISTINCT project)).
func TestActiveFilesRenameMergesProjectCount(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "actfren")
	sender := f.Sender()
	f.Projects("web-old", "web-new")

	base := time.Date(2025, 5, 6, 10, 0, 0, 0, time.UTC)
	// api.go touched by web-old (120s) and web-new (60s) -> baseline projects=2.
	f.Seed(hbSeed{project: "web-old", entity: "api.go", ts: base, gap: 120})
	f.Seed(hbSeed{project: "web-new", entity: "api.go", ts: base.Add(time.Minute), gap: 60})

	t0 := base.AddDate(0, 0, -1)
	t1 := base.AddDate(0, 0, 1)

	// Rename both raw names to the same display "web": distinct projects -> 1,
	// seconds conserved (180).
	createRename(t, d, ctx, sender, "project", "web-old", "web")
	createRename(t, d, ctx, sender, "project", "web-new", "web")
	rs := loadRenames(t, d, ctx, sender)

	files, _, err := d.GetActiveFiles(ctx, sender, t0, t1, 15, 20, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	by := afByEntity(files)
	api := by["api.go"]
	if api.Projects != 1 {
		t.Errorf("api.go projects after merge = %d, want 1 (merged display name)", api.Projects)
	}
	if api.Seconds != 180 {
		t.Errorf("api.go seconds after merge = %d, want 180 (conserved)", api.Seconds)
	}
}

// TestActiveFilesTruncation: the limit caps the result and sets truncated.
func TestActiveFilesTruncation(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()
	f := newSender(t, d, "actftrunc")
	sender := f.Sender()
	f.Projects("alpha")

	base := time.Date(2025, 5, 7, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		f.Seed(hbSeed{project: "alpha", entity: "f" + string(rune('a'+i)) + ".go", ts: base.Add(time.Duration(i) * time.Minute), gap: 60})
	}

	files, trunc, err := d.GetActiveFiles(ctx, sender, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1), 15, 3, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Errorf("len = %d, want 3 (capped)", len(files))
	}
	if !trunc {
		t.Error("expected truncated=true when more files than limit")
	}
}
