// cluster_ginkgo_test.go — ginkgo mirror of cluster_test.go.
// 1:1 case map (9 stdlib TestXxx → 9 Its across two Describe blocks):
//
//	TestCluster_EmptyReturnsNil                              → Cluster > empty input returns nil
//	TestCluster_SingleCommit_ProducesOneSession_WithLeadAndTail
//	                                                         → Cluster > single commit → one session with lead/tail padding
//	TestCluster_CommitsWithinGap_MergeIntoOneSession         → Cluster > commits within gap merge into one session
//	TestCluster_CommitsBeyondGap_SplitIntoTwoSessions        → Cluster > commits beyond gap split into two sessions
//	TestCluster_UnsortedInput_ClustersByTime                 → Cluster > unsorted input clusters by time
//	TestCluster_TopFileWinsByLinesTouched                    → Cluster > top file wins by lines touched
//	TestMaterialize_ExactHBCount                             → Materialize > exact heartbeat count and cadence
//	TestMaterialize_EmptyTopFile_UsesPlaceholderEntity       → Materialize > empty TopFile uses placeholder entity
//	TestMaterialize_EndBeforeStart_ReturnsNil                → Materialize > End before Start returns nil
//	TestMaterialize_DistributesAcrossFilesByWeight           → Materialize > distributes heartbeats across files by weight
//
// Reuses at() and mkCommit() helpers from cluster_test.go (same package).
package git

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster", func() {
	// The default estimator config used by every non-empty test.
	cfg := EstimatorConfig{
		ClusterGap:     30 * time.Minute,
		PreCommitLead:  15 * time.Minute,
		PostCommitTail: 5 * time.Minute,
		HeartbeatRate:  2 * time.Minute,
	}

	It("returns nil for empty input", func() {
		out := Cluster(nil, EstimatorConfig{})
		Expect(out).To(BeNil())
	})

	It("produces one session with lead+tail padding from a single commit", func() {
		c := mkCommit(at(60), []string{"main.go"}, 10)
		sessions := Cluster([]Commit{c}, cfg)
		Expect(sessions).To(HaveLen(1))
		s := sessions[0]
		Expect(s.Start.Equal(at(45))).To(BeTrue(), "Start = %v, want %v", s.Start, at(45))
		Expect(s.End.Equal(at(65))).To(BeTrue(), "End = %v, want %v", s.End, at(65))
		Expect(s.TopFile).To(Equal("main.go"))
		Expect(s.Language).To(Equal("Go"))
	})

	It("merges commits within ClusterGap into one session", func() {
		commits := []Commit{
			mkCommit(at(60), []string{"a.py"}, 5),
			mkCommit(at(80), []string{"a.py"}, 15),  // 20min later
			mkCommit(at(100), []string{"a.py"}, 30), // 20min later
		}
		sessions := Cluster(commits, cfg)
		Expect(sessions).To(HaveLen(1))
		s := sessions[0]
		Expect(s.Start.Equal(at(45))).To(BeTrue(), "Start = %v, want at(45)", s.Start)
		Expect(s.End.Equal(at(105))).To(BeTrue(), "End = %v, want at(105)", s.End)
		Expect(s.Language).To(Equal("Python"))
	})

	It("splits commits beyond ClusterGap into two sessions", func() {
		commits := []Commit{
			mkCommit(at(60), []string{"a.go"}, 10),
			mkCommit(at(100), []string{"b.ts"}, 20), // 40min later -> split
		}
		sessions := Cluster(commits, cfg)
		Expect(sessions).To(HaveLen(2))
		Expect(sessions[0].Language).To(Equal("Go"))
		Expect(sessions[1].Language).To(Equal("TypeScript"))
	})

	It("clusters unsorted input by time (sorts internally)", func() {
		// Feed in reverse order — Cluster must sort internally.
		commits := []Commit{
			mkCommit(at(100), []string{"a.go"}, 10),
			mkCommit(at(60), []string{"a.go"}, 5),
			mkCommit(at(80), []string{"a.go"}, 15),
		}
		sessions := Cluster(commits, cfg)
		Expect(sessions).To(HaveLen(1))
		Expect(sessions[0].Start.Equal(at(45))).To(BeTrue(), "Start = %v, want at(45)", sessions[0].Start)
		Expect(sessions[0].End.Equal(at(105))).To(BeTrue(), "End = %v, want at(105)", sessions[0].End)
	})

	It("picks TopFile by lines touched across all commits", func() {
		// a.go touched 5+5=10; b.go touched 100
		commits := []Commit{
			mkCommit(at(60), []string{"a.go"}, 5),
			mkCommit(at(70), []string{"b.go"}, 100),
			mkCommit(at(80), []string{"a.go"}, 5),
		}
		sessions := Cluster(commits, cfg)
		Expect(sessions).To(HaveLen(1))
		Expect(sessions[0].TopFile).To(Equal("b.go"))
	})
})

var _ = Describe("Materialize", func() {
	// TestMaterialize_ExactHBCount verifies the heartbeat count and cadence
	// for a session with a known duration and rate — critical because the
	// rate is what determines how much "time" the backfill row set will
	// contribute to daily rollups.
	It("produces the exact heartbeat count and cadence for a known duration", func() {
		sess := Session{
			RepoName: "r",
			Start:    at(0),
			End:      at(10), // 10 min duration
			TopFile:  "main.go",
			Language: "Go",
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		// 10min / 2min = 5 intervals -> 6 heartbeats (0,2,4,6,8,10).
		Expect(hbs).To(HaveLen(6))
		for i, hb := range hbs {
			expect := at(0).Add(time.Duration(i) * 2 * time.Minute)
			got := time.Unix(int64(hb.Time), 0).UTC()
			Expect(got.Equal(expect)).To(BeTrue(), "hb[%d].Time = %v, want %v", i, got, expect)
			Expect(hb.Entity).To(Equal("main.go"), "hb[%d].Entity mismatch", i)
			Expect(hb.Language).NotTo(BeNil(), "hb[%d].Language nil", i)
			Expect(*hb.Language).To(Equal("Go"), "hb[%d].Language mismatch", i)
			Expect(hb.Sender).NotTo(BeNil(), "hb[%d].Sender nil", i)
			Expect(*hb.Sender).To(Equal("backfill:git"), "hb[%d].Sender mismatch", i)
		}
	})

	It("uses backfill:<repo> placeholder entity when TopFile is empty", func() {
		sess := Session{
			RepoName: "myproj",
			Start:    at(0),
			End:      at(4),
		}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).NotTo(BeEmpty())
		// 0,2,4 -> 3 heartbeats
		Expect(hbs).To(HaveLen(3))
		for _, hb := range hbs {
			Expect(hb.Entity).To(Equal("backfill:myproj"))
		}
	})

	It("returns nil when End is before Start", func() {
		sess := Session{Start: at(10), End: at(0)}
		hbs := Materialize(sess, "backfill:git", 2*time.Minute)
		Expect(hbs).To(BeNil())
	})

	// TestMaterialize_DistributesAcrossFilesByWeight verifies the post-fix
	// behavior: when Session has FileWeights, heartbeats are spread across
	// every file (not just TopFile) proportional to weight, and per-file
	// language attribution kicks in. Fixes the "all N heartbeats have
	// entity=.flake8" observation on the first live backfill run.
	It("distributes heartbeats across files by weight (largest-remainder)", func() {
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
		Expect(hbs).To(HaveLen(10))
		counts := map[string]int{}
		for _, hb := range hbs {
			counts[hb.Entity]++
		}
		// Largest-remainder allocation of 10 slots with weights 6/3/1 → 6/3/1.
		Expect(counts["main.go"]).To(Equal(6))
		Expect(counts["readme.md"]).To(Equal(3))
		Expect(counts["util.go"]).To(Equal(1))
		// Per-heartbeat language derived from the file's extension.
		for _, hb := range hbs {
			wantLang := sess.FileLanguages[hb.Entity]
			Expect(hb.Language).NotTo(BeNil(), "Entity=%q Language is nil, want %q", hb.Entity, wantLang)
			Expect(*hb.Language).To(Equal(wantLang), "Entity=%q language mismatch", hb.Entity)
		}
	})
})

// -- helpers restored from internal/backfill/git/cluster_test.go (gaka-0vp.17) --
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
