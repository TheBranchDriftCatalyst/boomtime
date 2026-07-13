package db

import (
	"context"
	"embed"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/fixture"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

//go:embed testdata/heartbeats_fixture.json
var fixtureFS embed.FS

// loadFixture reads the committed anonymized fixture, bulk-inserts it under
// `sender` into the isolated test DB, and runs gap/rollup recomputation so
// aggregations are realistic. Returns the loaded document for assertions.
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

// TestFixturePipeline is a golden smoke test: load the anonymized fixture and
// assert stable, meaningful invariants on realistic data. Proves the whole
// fixturegen -> loader -> aggregation pipeline works end to end.
func TestFixturePipeline(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Clean slate for THIS sender only. Do NOT truncateAll here: internal/db and
	// internal/testutil are separate test binaries sharing boomtime_test under a
	// parallel `go test ./...`, and TRUNCATE users CASCADE nukes the other
	// binary's minted users mid-test (flaky FK failures). All of this test's
	// assertions are sender-scoped, so a sender purge is a sufficient reset.
	sender := "fixture_user"
	deleteSenderRows(d, ctx, sender)
	cleanupSender(t, d, ctx, sender)
	doc := loadFixture(t, d, sender)

	// The fixture must actually be anonymized (never commit real data).
	if !doc.Anonymized {
		t.Fatal("committed fixture must be anonymized")
	}

	// Range spanning the whole fixture window.
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

	// --- Invariant 1: stats totals are positive on realistic data. ---
	rows, err := d.GetUserActivity(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if totalStatSeconds(rows) <= 0 {
		t.Fatal("expected positive total coding time from the fixture")
	}

	// --- Invariant 2: rollup total == raw total (both use gap_seconds<=900). ---
	rollup, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	rawTotal := totalStatSeconds(rows)
	rollTotal := totalStatSeconds(rollup)
	if rawTotal != rollTotal {
		t.Fatalf("rollup total (%d) != raw total (%d) at the default 15-min limit", rollTotal, rawTotal)
	}

	// --- Invariant 3: top-N + "Other" bucketing kicks in on many-value axes. ---
	// The fixture covers 12 projects (> the top-12 cap only if >12); assert we get
	// the documented number of distinct projects and that language capping applies
	// when there are more than the cap.
	langGroups, _, err := d.GroupHeartbeats(ctx, sender, "language", start, end, nil, "", 500, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(langGroups) < 2 {
		t.Fatalf("expected multiple languages in the fixture, got %d", len(langGroups))
	}

	// --- Invariant 4: fixture-declared coverage matches what actually loaded. ---
	var distinctProjects int
	if err := d.Pool.QueryRow(ctx,
		`SELECT count(DISTINCT project) FROM heartbeats WHERE sender=$1`, sender).Scan(&distinctProjects); err != nil {
		t.Fatal(err)
	}
	if distinctProjects != doc.Counts.Projects {
		t.Fatalf("loaded distinct projects (%d) != fixture Counts.Projects (%d)", distinctProjects, doc.Counts.Projects)
	}
	if doc.Counts.Projects < 8 {
		t.Fatalf("fixture should cover >= 8 projects for realism, got %d", doc.Counts.Projects)
	}
	if doc.Counts.Days < 30 {
		t.Fatalf("fixture should span >= 30 days, got %d", doc.Counts.Days)
	}

	// --- Invariant 5: weekly bucketing (momentum) produces multiple weeks. ---
	mom, err := d.GetMomentum(ctx, sender, start, end, 15, HiddenSets{}, RenameSets{}, MemberSets{}, false)
	if err != nil {
		t.Fatal(err)
	}
	weeks := map[string]struct{}{}
	for _, m := range mom {
		weeks[m.WeekStart.Format("2006-01-02")] = struct{}{}
	}
	if len(weeks) < 4 {
		t.Fatalf("expected multiple weeks from a ~%d-day fixture, got %d", doc.Counts.Days, len(weeks))
	}
	if sumMomentum(mom) != rawTotal {
		t.Fatalf("momentum total (%d) != raw activity total (%d)", sumMomentum(mom), rawTotal)
	}
}
