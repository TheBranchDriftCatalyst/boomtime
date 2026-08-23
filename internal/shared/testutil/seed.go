package testutil

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// HB is a fully-specified heartbeat for the external seed builder. Empty string
// fields become SQL NULL; Gap is gap_seconds (<= limit*60 counts as attributed).
type HB struct {
	Project, Language, Editor, Plugin, Machine, Platform, Branch, Category string
	Ty, Entity                                                             string
	IsWrite                                                                *bool
	TS                                                                     time.Time
	Gap                                                                    int64
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Seeder seeds heartbeats + derived data for one owner via the isolated DB.
// t is a HarnessT (not *testing.T) so ginkgo callers can construct one via
// Harness.Seeder without having a real *testing.T.
type Seeder struct {
	t      HarnessT
	db     *db.DB
	ctx    context.Context
	sender string
}

// Seeder returns a heartbeat seeder scoped to the given owner (its user row must
// already exist, e.g. from MintUser).
func (hz *Harness) Seeder(sender string) *Seeder {
	return &Seeder{t: hz.T, db: hz.DB, ctx: context.Background(), sender: sender}
}

// SeedRollup inserts one rollup row for (owner, day, language, seconds).
// Returns any Pool.Exec error verbatim — callers pick the assertion style:
//
//	stdlib:  if err := hz.SeedRollup(...); err != nil { t.Fatalf(...) }
//	ginkgo:  Expect(hz.SeedRollup(...)).To(Succeed())
//
// Was: seedRollupForOwner (goals_test.go) + seedRollupForOwnerG
// (goals_ginkgo_test.go). Both were removed as part of boom-0vp.18.
func (hz *Harness) SeedRollup(owner string, day time.Time, language string, seconds int64) error {
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO hb_rollup_daily (sender, day, project, language, editor,
			platform, machine, category, plugin, branch, total_seconds)
		VALUES ($1, $2::date, 'P', $3, 'vim', 'linux', 'm', 'Coding', 'pl', 'main', $4)
		ON CONFLICT DO NOTHING`,
		owner, day, language, seconds)
	return err
}

// Projects inserts the projects rows the (sender,project) FK requires.
func (s *Seeder) Projects(names ...string) *Seeder {
	s.t.Helper()
	for _, n := range names {
		if _, err := s.db.Pool.Exec(s.ctx, `INSERT INTO projects (owner,name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, s.sender, n); err != nil {
			s.t.Fatalf("ensure project %s: %v", n, err)
		}
	}
	return s
}

// Seed inserts one heartbeat (creating its project row) with sender auto-filled.
func (s *Seeder) Seed(h HB) *Seeder {
	s.t.Helper()
	if h.Project != "" {
		s.Projects(h.Project)
	}
	ty := h.Ty
	if ty == "" {
		ty = "file"
	}
	entity := h.Entity
	if entity == "" {
		entity = "a.go"
	}
	var isWrite any
	if h.IsWrite != nil {
		isWrite = *h.IsWrite
	}
	_, err := s.db.Pool.Exec(s.ctx, `
		INSERT INTO heartbeats
		  (sender, project, language, editor, plugin, machine, platform, branch, category,
		   entity, ty, is_write, time_sent, user_agent, gap_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'ua',$14)`,
		s.sender, nz(h.Project), nz(h.Language), nz(h.Editor), nz(h.Plugin), nz(h.Machine),
		nz(h.Platform), nz(h.Branch), nz(h.Category), entity, ty, isWrite, h.TS, h.Gap)
	if err != nil {
		s.t.Fatalf("insert heartbeat: %v", err)
	}
	return s
}

// Block seeds a leading break beat (gap 999999, unattributed) then n attributed
// beats of `each` seconds 1 minute apart, sharing tmpl's fields. Returns
// attributed total (n*each).
func (s *Seeder) Block(tmpl HB, startTS time.Time, n int, each int64) int64 {
	s.t.Helper()
	brk := tmpl
	brk.TS = startTS
	brk.Gap = 999999
	s.Seed(brk)
	for i := 0; i < n; i++ {
		h := tmpl
		h.TS = startTS.Add(time.Duration(i+1) * time.Minute)
		h.Gap = each
		s.Seed(h)
	}
	return int64(n) * each
}

// RefreshRollup rebuilds the rollup for this owner since the given time.
func (s *Seeder) RefreshRollup(since time.Time) *Seeder {
	s.t.Helper()
	if err := s.db.RefreshRollup(s.ctx, s.sender, since); err != nil {
		s.t.Fatalf("RefreshRollup: %v", err)
	}
	return s
}

// RecomputeGaps recomputes gap_seconds for this owner since the given time.
func (s *Seeder) RecomputeGaps(since time.Time) *Seeder {
	s.t.Helper()
	if err := s.db.RecomputeGaps(s.ctx, s.sender, since); err != nil {
		s.t.Fatalf("RecomputeGaps: %v", err)
	}
	return s
}
