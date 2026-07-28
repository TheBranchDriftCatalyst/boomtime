package git

import (
	"testing"
	"time"
)

// t helpers -----------------------------------------------------------------

func at(min int) time.Time {
	// Anchor at 2026-01-01 00:00 UTC; minute offsets keep the numbers
	// easy to reason about in failure messages.
	return time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC)
}

func mkCommit(t time.Time, files []string, added int) Commit {
	return Commit{
		RepoName:     "r",
		Hash:         t.Format("150405"),
		AuthorEmail:  "me@example.com",
		Time:         t,
		FilesChanged: files,
		LinesAdded:   added,
		LinesDeleted: 0,
	}
}

// tests --------------------------------------------------------------------

func TestCluster_EmptyReturnsNil(t *testing.T) {
	out := Cluster(nil, EstimatorConfig{})
	if out != nil {
		t.Fatalf("Cluster(nil) = %v, want nil", out)
	}
}

func TestCluster_SingleCommit_ProducesOneSession_WithLeadAndTail(t *testing.T) {
	cfg := EstimatorConfig{
		ClusterGap:     30 * time.Minute,
		PreCommitLead:  15 * time.Minute,
		PostCommitTail: 5 * time.Minute,
		HeartbeatRate:  2 * time.Minute,
	}
	c := mkCommit(at(60), []string{"main.go"}, 10)
	sessions := Cluster([]Commit{c}, cfg)
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if !s.Start.Equal(at(45)) {
		t.Errorf("Start = %v, want %v", s.Start, at(45))
	}
	if !s.End.Equal(at(65)) {
		t.Errorf("End = %v, want %v", s.End, at(65))
	}
	if s.TopFile != "main.go" {
		t.Errorf("TopFile = %q, want main.go", s.TopFile)
	}
	if s.Language != "Go" {
		t.Errorf("Language = %q, want Go", s.Language)
	}
}

func TestCluster_CommitsWithinGap_MergeIntoOneSession(t *testing.T) {
	cfg := EstimatorConfig{
		ClusterGap:     30 * time.Minute,
		PreCommitLead:  15 * time.Minute,
		PostCommitTail: 5 * time.Minute,
		HeartbeatRate:  2 * time.Minute,
	}
	commits := []Commit{
		mkCommit(at(60), []string{"a.py"}, 5),
		mkCommit(at(80), []string{"a.py"}, 15), // 20min later
		mkCommit(at(100), []string{"a.py"}, 30), // 20min later
	}
	sessions := Cluster(commits, cfg)
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if !s.Start.Equal(at(45)) {
		t.Errorf("Start = %v, want at(45)", s.Start)
	}
	if !s.End.Equal(at(105)) {
		t.Errorf("End = %v, want at(105)", s.End)
	}
	if s.Language != "Python" {
		t.Errorf("Language = %q, want Python", s.Language)
	}
}

func TestCluster_CommitsBeyondGap_SplitIntoTwoSessions(t *testing.T) {
	cfg := EstimatorConfig{
		ClusterGap:     30 * time.Minute,
		PreCommitLead:  15 * time.Minute,
		PostCommitTail: 5 * time.Minute,
		HeartbeatRate:  2 * time.Minute,
	}
	commits := []Commit{
		mkCommit(at(60), []string{"a.go"}, 10),
		mkCommit(at(100), []string{"b.ts"}, 20), // 40min later -> split
	}
	sessions := Cluster(commits, cfg)
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	if sessions[0].Language != "Go" || sessions[1].Language != "TypeScript" {
		t.Errorf("languages = %q, %q; want Go, TypeScript",
			sessions[0].Language, sessions[1].Language)
	}
}

func TestCluster_UnsortedInput_ClustersByTime(t *testing.T) {
	cfg := EstimatorConfig{
		ClusterGap:     30 * time.Minute,
		PreCommitLead:  15 * time.Minute,
		PostCommitTail: 5 * time.Minute,
		HeartbeatRate:  2 * time.Minute,
	}
	// Feed in reverse order — Cluster must sort internally.
	commits := []Commit{
		mkCommit(at(100), []string{"a.go"}, 10),
		mkCommit(at(60), []string{"a.go"}, 5),
		mkCommit(at(80), []string{"a.go"}, 15),
	}
	sessions := Cluster(commits, cfg)
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if !sessions[0].Start.Equal(at(45)) {
		t.Errorf("Start = %v, want at(45)", sessions[0].Start)
	}
	if !sessions[0].End.Equal(at(105)) {
		t.Errorf("End = %v, want at(105)", sessions[0].End)
	}
}

func TestCluster_TopFileWinsByLinesTouched(t *testing.T) {
	cfg := EstimatorConfig{
		ClusterGap:     30 * time.Minute,
		PreCommitLead:  15 * time.Minute,
		PostCommitTail: 5 * time.Minute,
		HeartbeatRate:  2 * time.Minute,
	}
	// a.go touched 5+5=10; b.go touched 100
	commits := []Commit{
		mkCommit(at(60), []string{"a.go"}, 5),
		mkCommit(at(70), []string{"b.go"}, 100),
		mkCommit(at(80), []string{"a.go"}, 5),
	}
	sessions := Cluster(commits, cfg)
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].TopFile != "b.go" {
		t.Errorf("TopFile = %q, want b.go", sessions[0].TopFile)
	}
}

// TestMaterialize_ExactHBCount verifies the heartbeat count and cadence
// for a session with a known duration and rate — critical because the
// rate is what determines how much "time" the backfill row set will
// contribute to daily rollups.
func TestMaterialize_ExactHBCount(t *testing.T) {
	sess := Session{
		RepoName: "r",
		Start:    at(0),
		End:      at(10), // 10 min duration
		TopFile:  "main.go",
		Language: "Go",
	}
	hbs := Materialize(sess, "backfill:git", 2*time.Minute)
	// 10min / 2min = 5 intervals -> 6 heartbeats (0,2,4,6,8,10).
	if len(hbs) != 6 {
		t.Fatalf("len(hbs) = %d, want 6", len(hbs))
	}
	for i, hb := range hbs {
		expect := at(0).Add(time.Duration(i) * 2 * time.Minute)
		got := time.Unix(int64(hb.Time), 0).UTC()
		if !got.Equal(expect) {
			t.Errorf("hb[%d].Time = %v, want %v", i, got, expect)
		}
		if hb.Entity != "main.go" {
			t.Errorf("hb[%d].Entity = %q, want main.go", i, hb.Entity)
		}
		if hb.Language == nil || *hb.Language != "Go" {
			t.Errorf("hb[%d].Language = %v, want Go", i, hb.Language)
		}
		if hb.Sender == nil || *hb.Sender != "backfill:git" {
			t.Errorf("hb[%d].Sender = %v, want backfill:git", i, hb.Sender)
		}
	}
}

func TestMaterialize_EmptyTopFile_UsesPlaceholderEntity(t *testing.T) {
	sess := Session{
		RepoName: "myproj",
		Start:    at(0),
		End:      at(4),
	}
	hbs := Materialize(sess, "backfill:git", 2*time.Minute)
	if len(hbs) == 0 {
		t.Fatalf("empty output")
	}
	// 0,2,4 -> 3 heartbeats
	if len(hbs) != 3 {
		t.Errorf("len(hbs) = %d, want 3", len(hbs))
	}
	for _, hb := range hbs {
		if hb.Entity != "backfill:myproj" {
			t.Errorf("Entity = %q, want backfill:myproj", hb.Entity)
		}
	}
}

func TestMaterialize_EndBeforeStart_ReturnsNil(t *testing.T) {
	sess := Session{Start: at(10), End: at(0)}
	hbs := Materialize(sess, "backfill:git", 2*time.Minute)
	if hbs != nil {
		t.Errorf("hbs = %v, want nil", hbs)
	}
}

// TestMaterialize_DistributesAcrossFilesByWeight verifies the post-fix
// behavior: when Session has FileWeights, heartbeats are spread across
// every file (not just TopFile) proportional to weight, and per-file
// language attribution kicks in. Fixes the "all N heartbeats have
// entity=.flake8" observation on the first live backfill run.
func TestMaterialize_DistributesAcrossFilesByWeight(t *testing.T) {
	sess := Session{
		RepoName: "r",
		Start:    at(0),
		End:      at(18), // 18min / 2min = 10 heartbeats
		TopFile:  "main.go",
		Language: "Go",
		FileWeights: map[string]int{
			"main.go":   6, // 60% of weight → ~6 slots
			"readme.md": 3, // 30% → ~3 slots
			"util.go":   1, // 10% → ~1 slot
		},
		FileLanguages: map[string]string{
			"main.go":   "Go",
			"readme.md": "Markdown",
			"util.go":   "Go",
		},
	}
	hbs := Materialize(sess, "backfill:git", 2*time.Minute)
	if len(hbs) != 10 {
		t.Fatalf("len(hbs) = %d, want 10", len(hbs))
	}
	counts := map[string]int{}
	for _, hb := range hbs {
		counts[hb.Entity]++
	}
	// Largest-remainder allocation of 10 slots with weights 6/3/1 → 6/3/1.
	if counts["main.go"] != 6 {
		t.Errorf("main.go got %d slots, want 6", counts["main.go"])
	}
	if counts["readme.md"] != 3 {
		t.Errorf("readme.md got %d slots, want 3", counts["readme.md"])
	}
	if counts["util.go"] != 1 {
		t.Errorf("util.go got %d slots, want 1", counts["util.go"])
	}
	// Per-heartbeat language derived from the file's extension.
	for _, hb := range hbs {
		wantLang := sess.FileLanguages[hb.Entity]
		if hb.Language == nil || *hb.Language != wantLang {
			got := "<nil>"
			if hb.Language != nil {
				got = *hb.Language
			}
			t.Errorf("Entity=%q got Language=%q, want %q", hb.Entity, got, wantLang)
		}
	}
}
