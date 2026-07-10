package db

import (
	"context"
	"testing"
	"time"
)

// TestRegexRemapAcrossAllAggregations is the belt-and-suspenders integration test
// proving a SINGLE regex rename rule (`project ^Meet - → Meeting`) remaps
// correctly, with concrete numbers, through EVERY server-side aggregation path —
// while leaving raw records untouched and being fully reversible.
//
// Seeding (all gaps chosen ≤ 900s so they attribute at both the 15-min rollup and
// a 30-min raw read; the leading break beat per run has gap 999999 and is NOT
// attributed):
//
//	Meet - A : 4 attributed beats × 300s = 1200s (branch feature-a, Go,  Coding,   file writes on a.go)
//	Meet - B : 3 attributed beats × 300s =  900s (branch feature-b, Rust,Debugging,file reads  on b.go)
//	Meet - C : 2 attributed beats × 300s =  600s (branch feature-c, Go,  Coding,   file reads  on c.go)
//	real-proj: 5 attributed beats × 300s = 1500s (branch main,     Go,  Coding,   file writes on r.go)  ← NOT matched
//
// Merged "Meeting" == 1200+900+600 == 2700s. real-proj == 1500s. Grand total 4200s
// is conserved (a rename is a pure relabel).
func TestRegexRemapAcrossAllAggregations(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	sender := mkSender("rxall")
	_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
	cleanupSender(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, "Meet - A", "Meet - B", "Meet - C", "real-proj")

	// insertRun seeds a break beat (gap 999999, unattributed) then n attributed
	// beats of `each` seconds. All rows share (project, branch, language, category,
	// entity, isWrite). Returns attributed seconds and TOTAL rows inserted (n+1).
	const each int64 = 300
	insertRun := func(project, branch, lang, cat, entity string, isWrite bool, startTS time.Time, n int) (int64, int) {
		t.Helper()
		ins := func(ts time.Time, gap int64) {
			if _, err := d.Pool.Exec(ctx, `
				INSERT INTO heartbeats
				  (sender, project, branch, language, category, entity, ty, is_write, time_sent, user_agent, gap_seconds)
				VALUES ($1,$2,$3,$4,$5,$6,'file',$7,$8,'ua',$9)`,
				sender, project, branch, lang, cat, entity, isWrite, ts, gap); err != nil {
				t.Fatal(err)
			}
		}
		ins(startTS, 999999) // break
		for i := 0; i < n; i++ {
			ins(startTS.Add(time.Duration(i+1)*time.Minute), each)
		}
		return int64(n) * each, n + 1
	}

	// Spread across 3 ISO weeks (Mondays) so momentum has multiple week buckets.
	w1 := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC)  // Mon
	w2 := time.Date(2025, 6, 9, 9, 0, 0, 0, time.UTC)  // Mon
	w3 := time.Date(2025, 6, 16, 9, 0, 0, 0, time.UTC) // Mon

	// Meet - A: split across w1 (2 beats) and w2 (2 beats) => tA=1200, 6 raw rows.
	aW1, aR1 := insertRun("Meet - A", "feature-a", "Go", "Coding", "a.go", true, w1, 2)
	aW2, aR2 := insertRun("Meet - A", "feature-a", "Go", "Coding", "a.go", true, w2, 2)
	tA, rawA := aW1+aW2, aR1+aR2
	// Meet - B: w2 => tB=900, 4 raw rows.
	tB, rawB := insertRun("Meet - B", "feature-b", "Rust", "Debugging", "b.go", false, w2.Add(time.Hour), 3)
	// Meet - C: w3 => tC=600, 3 raw rows.
	tC, rawC := insertRun("Meet - C", "feature-c", "Go", "Coding", "c.go", false, w3, 2)
	// real-proj: w1+w3 => tR=1500, 7 raw rows. NOT matched by ^Meet -.
	rW1, rR1 := insertRun("real-proj", "main", "Go", "Coding", "r.go", true, w1.Add(2*time.Hour), 2)
	rW3, rR3 := insertRun("real-proj", "main", "Go", "Coding", "r.go", true, w3.Add(2*time.Hour), 3)
	tR, rawR := rW1+rW3, rR1+rR3

	merged := tA + tB + tC // 2700
	grand := merged + tR   // 4200

	if err := d.RefreshRollup(ctx, sender, w1.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}

	start := w1.AddDate(0, 0, -1)
	end := w3.AddDate(0, 0, 7)

	// ---- Baseline (no rule): the three Meet projects are distinct. ----
	rawBefore, err := d.GetUserActivity(ctx, sender, start, end, 30, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	pre := axisTotals(rawBefore, "project")
	if pre["Meet - A"] != tA || pre["Meet - B"] != tB || pre["Meet - C"] != tC || pre["real-proj"] != tR {
		t.Fatalf("baseline project totals wrong: %+v (want A=%d B=%d C=%d R=%d)", pre, tA, tB, tC, tR)
	}
	if grandTotal(rawBefore) != grand {
		t.Fatalf("baseline grand total = %d, want %d", grandTotal(rawBefore), grand)
	}

	// ---- Create the single REGEX rule (via the same CreateCurationRule path). ----
	newVal := "Meeting"
	rule, err := d.CreateCurationRule(ctx, sender, "project", "rename", "regex", "^Meet - ", &newVal)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := d.LoadRenameSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.HasAxis("project") {
		t.Fatal("regex rule should register on the project axis")
	}

	// Reusable assertion: "Meeting" == merged, "real-proj" == tR, no raw Meet-*,
	// grand total conserved — for a StatRow set.
	assertMerged := func(label string, rows []StatRow) {
		t.Helper()
		got := axisTotals(rows, "project")
		for _, raw := range []string{"Meet - A", "Meet - B", "Meet - C"} {
			if _, ok := got[raw]; ok {
				t.Fatalf("[%s] raw %q still shown after merge", label, raw)
			}
		}
		if got["Meeting"] != merged {
			t.Fatalf("[%s] Meeting = %d, want %d", label, got["Meeting"], merged)
		}
		if got["real-proj"] != tR {
			t.Fatalf("[%s] real-proj = %d, want %d (untouched)", label, got["real-proj"], tR)
		}
		if grandTotal(rows) != grand {
			t.Fatalf("[%s] grand total changed by merge: %d, want %d", label, grandTotal(rows), grand)
		}
	}

	// ---- PATH 1: GetUserActivity (raw path, limit=30 ≠ 15). ----
	rawRows, err := d.GetUserActivity(ctx, sender, start, end, 30, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMerged("raw activity", rawRows)

	// ---- PATH 2: GetUserActivityRollup (fast path, limit=15). ----
	rollRows, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMerged("rollup", rollRows)

	// ---- PATH 3: GetProjectStats by DISPLAY name "Meeting" aggregates A+B+C. ----
	sumProj := func(rows []ProjectStatRow) int64 {
		var s int64
		for _, r := range rows {
			s += r.TotalSeconds
		}
		return s
	}
	meetingDetail, err := d.GetProjectStats(ctx, sender, "Meeting", start, end, 30, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumProj(meetingDetail); got != merged {
		t.Fatalf("[project detail Meeting] total = %d, want %d", got, merged)
	}
	// identity: real-proj detail unchanged.
	realDetail, err := d.GetProjectStats(ctx, sender, "real-proj", start, end, 30, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumProj(realDetail); got != tR {
		t.Fatalf("[project detail real-proj] total = %d, want %d", got, tR)
	}
	// A raw source name is no longer addressable (keyed by display name).
	if rows, err := d.GetProjectStats(ctx, sender, "Meet - A", start, end, 30, HiddenSets{}, rs, MemberSets{}, false); err != nil {
		t.Fatal(err)
	} else if len(rows) != 0 {
		t.Fatalf("[project detail Meet - A] should be empty under merge, got %d rows", len(rows))
	}

	// ---- PATH 4: GetAllProjects — one "Meeting" entry, no raw Meet-*. ----
	projects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var meetingSeen, rawSeen int
	for _, p := range projects {
		switch {
		case p == "Meeting":
			meetingSeen++
		case p == "Meet - A" || p == "Meet - B" || p == "Meet - C":
			rawSeen++
		}
	}
	if meetingSeen != 1 || rawSeen != 0 {
		t.Fatalf("[project list] Meeting=%d rawMeet=%d, want 1/0 (list=%v)", meetingSeen, rawSeen, projects)
	}

	// ---- PATH 5: GetLeaderboards — requester-scoped merge. ----
	lb, err := d.GetLeaderboards(ctx, start, end, sender, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	lbByProj := map[string]int64{}
	for _, r := range lb {
		if r.Sender == sender {
			lbByProj[r.Project] += r.TotalSeconds
		}
	}
	for _, raw := range []string{"Meet - A", "Meet - B", "Meet - C"} {
		if _, ok := lbByProj[raw]; ok {
			t.Fatalf("[leaderboards] raw %q still present", raw)
		}
	}
	if lbByProj["Meeting"] != merged {
		t.Fatalf("[leaderboards] Meeting = %d, want %d", lbByProj["Meeting"], merged)
	}
	if lbByProj["real-proj"] != tR {
		t.Fatalf("[leaderboards] real-proj = %d, want %d", lbByProj["real-proj"], tR)
	}

	// ---- PATH 6: GetMomentum — one "Meeting" series, weekly sums combined. ----
	mom, err := d.GetMomentum(ctx, sender, start, end, 15, HiddenSets{}, rs, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	momByProj := map[string]int64{}
	momWeeks := map[string]map[string]int64{}
	for _, m := range mom {
		if m.Project == "Meet - A" || m.Project == "Meet - B" || m.Project == "Meet - C" {
			t.Fatalf("[momentum] raw project %q still present", m.Project)
		}
		momByProj[m.Project] += m.Seconds
		if momWeeks[m.Project] == nil {
			momWeeks[m.Project] = map[string]int64{}
		}
		momWeeks[m.Project][m.WeekStart.Format("2006-01-02")] += m.Seconds
	}
	if momByProj["Meeting"] != merged {
		t.Fatalf("[momentum] Meeting total = %d, want %d", momByProj["Meeting"], merged)
	}
	if momByProj["real-proj"] != tR {
		t.Fatalf("[momentum] real-proj total = %d, want %d", momByProj["real-proj"], tR)
	}
	// Meeting spans multiple weeks (A in w1+w2, B in w2, C in w3): the w2 bucket
	// combines the remainder of A (600) plus all of B (900) == 1500.
	mw := momWeeks["Meeting"]
	if len(mw) < 3 {
		t.Fatalf("[momentum] Meeting should span >=3 weeks, got %v", mw)
	}
	if mw[w2.Format("2006-01-02")] != 600+tB {
		t.Fatalf("[momentum] Meeting week w2 = %d, want %d (A's w2 600 + all of B %d)", mw[w2.Format("2006-01-02")], 600+tB, tB)
	}

	// ---- PATH 7: GetCategoryDaily — project remap doesn't change category totals. ----
	catTotal := func(rs RenameSets) int64 {
		cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, HiddenSets{}, rs, MemberSets{}, false)
		if err != nil {
			t.Fatal(err)
		}
		var s int64
		for _, c := range cats {
			s += c.TotalSeconds
		}
		return s
	}
	catBefore := catTotal(RenameSets{})
	catAfter := catTotal(rs)
	if catBefore != catAfter {
		t.Fatalf("[category] total changed by a PROJECT remap: %d -> %d (should be invariant)", catBefore, catAfter)
	}
	if catAfter != grand {
		t.Fatalf("[category] total = %d, want %d (all attributed time)", catAfter, grand)
	}

	// ---- PATH 8: GetProjectExtras("Meeting") aggregates across A+B+C. ----
	ex, err := d.GetProjectExtras(ctx, sender, "Meeting", start, end, 15, rs)
	if err != nil {
		t.Fatal(err)
	}
	var exWrite, exRead int64
	for _, e := range ex.Daily {
		exWrite += e.WriteSeconds
		exRead += e.ReadSeconds
	}
	// A is writes (1200), B+C are reads (900+600=1500); all under Meeting.
	if exWrite != tA {
		t.Fatalf("[extras] writeSeconds = %d, want %d (Meet - A writes)", exWrite, tA)
	}
	if exRead != tB+tC {
		t.Fatalf("[extras] readSeconds = %d, want %d (Meet - B+C reads)", exRead, tB+tC)
	}
	// branches[] carries all three source branches (none from real-proj).
	branchTotals := map[string]int64{}
	for _, b := range ex.Branches {
		branchTotals[b.Branch] += b.TotalSeconds
	}
	if branchTotals["feature-a"] != tA || branchTotals["feature-b"] != tB || branchTotals["feature-c"] != tC {
		t.Fatalf("[extras] branch totals = %+v, want feature-a=%d b=%d c=%d", branchTotals, tA, tB, tC)
	}
	if _, ok := branchTotals["main"]; ok {
		t.Fatal("[extras] real-proj's 'main' branch must not appear under Meeting")
	}
	// breadth: 3 distinct file entities across the merged sources (a.go/b.go/c.go).
	entTotal := int64(0)
	for _, e := range ex.Daily {
		entTotal += e.DistinctEntities
	}
	if entTotal < 3 {
		t.Fatalf("[extras] distinct entities across days = %d, want >=3 (a.go,b.go,c.go)", entTotal)
	}

	// ---- PATH 9: affected-values for the rule = all Meet-* raw values + counts. ----
	affected, truncated, err := d.CurationAffectedValues(ctx, sender, rule, 200)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("[affected] did not expect truncation")
	}
	affByVal := map[string]int64{}
	for _, a := range affected {
		affByVal[a.Value] = a.Count
	}
	if affByVal["Meet - A"] != int64(rawA) || affByVal["Meet - B"] != int64(rawB) || affByVal["Meet - C"] != int64(rawC) {
		t.Fatalf("[affected] = %+v, want raw counts A=%d B=%d C=%d", affected, rawA, rawB, rawC)
	}
	if _, ok := affByVal["real-proj"]; ok {
		t.Fatal("[affected] real-proj must not match ^Meet -")
	}
	if len(affected) != 3 {
		t.Fatalf("[affected] len = %d, want 3", len(affected))
	}

	// ---- RAW PRESERVED: audit surfaces show raw Meet-*, never "Meeting". ----
	col, _ := ExploreColumn("project")
	groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, 500, 15)
	if err != nil {
		t.Fatal(err)
	}
	groupVals := map[string]bool{}
	for _, g := range groups {
		if g.Value != nil {
			groupVals[*g.Value] = true
		}
	}
	for _, raw := range []string{"Meet - A", "Meet - B", "Meet - C", "real-proj"} {
		if !groupVals[raw] {
			t.Fatalf("[audit group] raw %q must still be shown", raw)
		}
	}
	if groupVals["Meeting"] {
		t.Fatal("[audit group] must NOT show the remapped 'Meeting'")
	}
	// ListHeartbeats (audit) still returns raw project rows.
	items, _, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	listVals := map[string]int{}
	for _, r := range items {
		if r.Project != nil {
			listVals[*r.Project]++
		}
	}
	if listVals["Meeting"] != 0 {
		t.Fatal("[audit list] must not show remapped 'Meeting'")
	}
	// Raw heartbeat row counts per source project are unchanged by the rule.
	if rawCount(t, d, ctx, sender, "project", "Meet - A") != rawA ||
		rawCount(t, d, ctx, sender, "project", "Meet - B") != rawB ||
		rawCount(t, d, ctx, sender, "project", "Meet - C") != rawC ||
		rawCount(t, d, ctx, sender, "project", "real-proj") != rawR {
		t.Fatal("[raw preserved] a raw project row count changed by the rename")
	}
	if rawCount(t, d, ctx, sender, "project", "Meeting") != 0 {
		t.Fatal("[raw preserved] rename created raw 'Meeting' rows (should be 0)")
	}

	// ---- REVERSIBILITY: delete the rule → every path reverts to the raw split. ----
	if _, err := d.DeleteCurationRule(ctx, sender, rule.ID); err != nil {
		t.Fatal(err)
	}
	rs2, err := d.LoadRenameSets(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if rs2.Any() {
		t.Fatal("rename set should be empty after delete")
	}

	revert, err := d.GetUserActivity(ctx, sender, start, end, 30, HiddenSets{}, rs2, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	rv := axisTotals(revert, "project")
	if rv["Meet - A"] != tA || rv["Meet - B"] != tB || rv["Meet - C"] != tC || rv["real-proj"] != tR {
		t.Fatalf("[revert raw] project totals = %+v, want A=%d B=%d C=%d R=%d", rv, tA, tB, tC, tR)
	}
	if _, ok := rv["Meeting"]; ok {
		t.Fatal("[revert raw] 'Meeting' should be gone after deleting the rule")
	}
	// Rollup + projects list + momentum revert too.
	revRoll, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, rs2, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rr := axisTotals(revRoll, "project"); rr["Meet - A"] != tA || rr["Meeting"] != 0 {
		t.Fatalf("[revert rollup] = %+v, want raw Meet-* restored, no Meeting", rr)
	}
	revProjects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{}, rs2, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var revMeet, revMeeting int
	for _, p := range revProjects {
		if p == "Meeting" {
			revMeeting++
		}
		if p == "Meet - A" || p == "Meet - B" || p == "Meet - C" {
			revMeet++
		}
	}
	if revMeet != 3 || revMeeting != 0 {
		t.Fatalf("[revert project list] Meet=%d Meeting=%d, want 3/0 (%v)", revMeet, revMeeting, revProjects)
	}

	// ---- CONSERVATION: grand total identical before vs after (pure relabel). ----
	if grandTotal(rawBefore) != grandTotal(rawRows) || grandTotal(rawRows) != grandTotal(revert) {
		t.Fatalf("grand total not conserved across merge/revert: before=%d merged=%d revert=%d",
			grandTotal(rawBefore), grandTotal(rawRows), grandTotal(revert))
	}
}
