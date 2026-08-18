// fixture_ginkgo_test.go — ginkgo mirror of fixture_test.go (gaka-0vp.13).
// 1:1 case map (1 stdlib TestXxx → 1 It):
//
//	TestFixturePipeline → It "fixture pipeline: golden smoke test on realistic anonymized data"
//
// Note: this file does NOT re-embed the fixture — that's in fixture_test.go via
// the stdlib compile. We re-use the loadFixtureG helper defined here, which
// bypasses the stdlib fixtureFS + loadFixture(t) signature.
package db

import (
	"context"
	"embed"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/fixture"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// loadFixtureG mirrors loadFixture(t) but returns the fixture doc for ginkgo.
// Reads the embedded fixture bytes via the stdlib-owned fixtureFS.
func loadFixtureG(d *DB, sender string) fixture.File {
	ctx := context.Background()

	raw, err := fixtureFS.ReadFile("testdata/heartbeats_fixture.json")
	Expect(err).NotTo(HaveOccurred())
	var doc fixture.File
	Expect(json.Unmarshal(raw, &doc)).To(Succeed())
	Expect(len(doc.Heartbeats)).NotTo(BeZero(), "fixture has no heartbeats")

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		sender)
	Expect(err).NotTo(HaveOccurred())

	beats := make([]model.HeartbeatPayload, 0, len(doc.Heartbeats))
	for _, h := range doc.Heartbeats {
		s := sender
		hb := model.HeartbeatPayload{
			Sender:       &s,
			Editor:       h.Editor,
			Plugin:       h.Plugin,
			Platform:     h.Platform,
			Machine:      h.Machine,
			Branch:       h.Branch,
			Category:     h.Category,
			Dependencies: h.Dependencies,
			Entity:       h.Entity,
			IsWrite:      h.IsWrite,
			Language:     h.Language,
			Lineno:       h.Lineno,
			FileLines:    h.FileLines,
			Project:      h.Project,
			Type:         model.EntityType(h.Type),
			UserAgent:    h.UserAgent,
			TimeSent:     float64(h.TimeSent.Unix()),
		}
		if h.Cursorpos != nil {
			if n, err := strconv.ParseInt(*h.Cursorpos, 10, 64); err == nil {
				hb.Cursorpos = &n
			}
		}
		beats = append(beats, hb)
	}
	_, err = d.SaveHeartbeats(ctx, beats)
	Expect(err).NotTo(HaveOccurred())
	return doc
}

var _ = ginkgo.Describe("fixture pipeline", func() {
	ginkgo.It("loads the anonymized fixture and asserts stable invariants on realistic data", func() {
		d := openTestDBG()
		ctx := context.Background()

		// The stdlib version keeps the constant sender "fixture_user" (see
		// comment in fixture_test.go about NOT truncateAll'ing).
		sender := "fixture_user_ginkgo"
		deleteSenderRows(d, ctx, sender)
		cleanupSenderG(d, ctx, sender)
		doc := loadFixtureG(d, sender)

		Expect(doc.Anonymized).To(BeTrue(), "committed fixture must be anonymized")

		var minT, maxT time.Time
		for i, h := range doc.Heartbeats {
			if i == 0 || h.TimeSent.Before(minT) {
				minT = h.TimeSent
			}
			if i == 0 || h.TimeSent.After(maxT) {
				maxT = h.TimeSent
			}
		}
		start := minT.AddDate(0, 0, -1)
		end := maxT.AddDate(0, 0, 1)

		// Invariant 1: positive total.
		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rows)).To(BeNumerically(">", 0), "expected positive total")

		// Invariant 2: rollup total == raw total.
		rollup, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rollup)).To(Equal(totalStatSeconds(rows)), "rollup total must match raw at default 15-min limit")

		// Invariant 3: >1 language on top-N/Other.
		langGroups, _, err := d.GroupHeartbeats(ctx, sender, "language", start, end, nil, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(langGroups)).To(BeNumerically(">=", 2))

		// Invariant 4: fixture-declared coverage matches loaded.
		var distinctProjects int
		err = d.Pool.QueryRow(ctx, `SELECT count(DISTINCT project) FROM heartbeats WHERE sender=$1`, sender).Scan(&distinctProjects)
		Expect(err).NotTo(HaveOccurred())
		Expect(distinctProjects).To(Equal(doc.Counts.Projects))
		Expect(doc.Counts.Projects).To(BeNumerically(">=", 8))
		Expect(doc.Counts.Days).To(BeNumerically(">=", 30))

		// Invariant 5: multiple weeks + momentum total == raw total.
		mom, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		weeks := map[string]struct{}{}
		for _, m := range mom {
			weeks[m.WeekStart.Format("2006-01-02")] = struct{}{}
		}
		Expect(len(weeks)).To(BeNumerically(">=", 4))
		Expect(sumMomentum(mom)).To(Equal(totalStatSeconds(rows)))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
//
//go:embed testdata/heartbeats_fixture.json
var fixtureFS embed.FS

func loadFixture(t *testing.T, d *DB, sender string) fixture.File {
	t.Helper()
	ctx := context.Background()

	raw, err := fixtureFS.ReadFile("testdata/heartbeats_fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc fixture.File
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(doc.Heartbeats) == 0 {
		t.Fatal("fixture has no heartbeats")
	}

	// The heartbeats.sender FK references users(username).
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		sender); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Convert fixture rows -> model.HeartbeatPayload. SaveHeartbeats inserts the
	// project rows (FK), upserts heartbeats, and recomputes gap_seconds + rollup.
	beats := make([]model.HeartbeatPayload, 0, len(doc.Heartbeats))
	for _, h := range doc.Heartbeats {
		s := sender
		hb := model.HeartbeatPayload{
			Sender:       &s,
			Editor:       h.Editor,
			Plugin:       h.Plugin,
			Platform:     h.Platform,
			Machine:      h.Machine,
			Branch:       h.Branch,
			Category:     h.Category,
			Dependencies: h.Dependencies,
			Entity:       h.Entity,
			IsWrite:      h.IsWrite,
			Language:     h.Language,
			Lineno:       h.Lineno,
			FileLines:    h.FileLines,
			Project:      h.Project,
			Type:         model.EntityType(h.Type),
			UserAgent:    h.UserAgent,
			TimeSent:     float64(h.TimeSent.Unix()),
		}
		if h.Cursorpos != nil {
			if n, err := strconv.ParseInt(*h.Cursorpos, 10, 64); err == nil {
				hb.Cursorpos = &n
			}
		}
		beats = append(beats, hb)
	}

	if _, err := d.SaveHeartbeats(ctx, beats); err != nil {
		t.Fatalf("SaveHeartbeats: %v", err)
	}
	return doc
}
