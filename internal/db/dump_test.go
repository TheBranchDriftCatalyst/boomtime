package db

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// openIsolatedDumpDB provisions/migrates a DEDICATED "<testdb>_dump" database.
// The restore tests TRUNCATE every table; `go test ./...` runs packages in
// parallel against the shared test DB, so they must never run there.
func openIsolatedDumpDB(t *testing.T) *DB {
	t.Helper()
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	url := maintenanceURLFor(testDatabaseURL(), testDBName+"_dump")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ensureTestDatabase(ctx, url); err != nil {
		t.Skipf("skipping: could not ensure %s_dump: %v", testDBName, err)
	}
	if err := MigrateURL(ctx, url); err != nil {
		t.Fatalf("migrate %s_dump: %v", testDBName, err)
	}
	d, err := New(ctx, url)
	if err != nil {
		t.Skipf("skipping: connect %s_dump: %v", testDBName, err)
	}
	t.Cleanup(d.Close)
	return d
}

// tableCount returns SELECT count(*) for a dump table.
func tableCount(t *testing.T, d *DB, table string) int64 {
	t.Helper()
	var n int64
	if err := d.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// seedFullState populates at least one row in EVERY dump table for a sender and
// returns the id of a 'running' import job (used to assert normalization).
func seedFullState(t *testing.T, d *DB, f *SenderFixture) (runningJobID int) {
	t.Helper()
	ctx := f.Ctx()
	sender := f.Sender()

	// Heartbeats (+projects via the fixture) with exact gaps, then the rollup.
	tmpl := hbSeed{
		project: "P", language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding",
	}
	f.Block(tmpl, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC), 5, 60)
	f.RefreshRollup(time.Unix(0, 0).UTC())

	// Tokens.
	mustExec(t, d, ctx, `INSERT INTO auth_tokens (token, owner, token_name) VALUES ($1,$2,'backup-test')`,
		"tok_"+sender, sender)
	mustExec(t, d, ctx, `INSERT INTO refresh_tokens (refresh_token, owner, token_expiry) VALUES ($1,$2,now())`,
		"refresh_"+sender, sender)

	// Badge for the seeded project.
	mustExec(t, d, ctx, `INSERT INTO badges (username, project) VALUES ($1,'P')`, sender)

	// Curation rule.
	newVal := "Renamed"
	if _, err := d.CreateCurationRule(ctx, sender, "project", "rename", "exact", "P", &newVal); err != nil {
		t.Fatalf("create curation rule: %v", err)
	}

	// Space + rule.
	var spaceID int
	if err := d.Pool.QueryRow(ctx, `INSERT INTO spaces (owner, name) VALUES ($1,'sp') RETURNING id`, sender).Scan(&spaceID); err != nil {
		t.Fatalf("insert space: %v", err)
	}
	mustExec(t, d, ctx, `INSERT INTO space_rules (space_id, axis, match_value) VALUES ($1,'project','P')`, spaceID)

	// A RUNNING import job (+ a log line): restore must normalize it to failed.
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO import_jobs (value, state, owner) VALUES ('{}'::jsonb,'running',$1) RETURNING id`,
		sender).Scan(&runningJobID); err != nil {
		t.Fatalf("insert import job: %v", err)
	}
	mustExec(t, d, ctx, `INSERT INTO import_job_logs (job_id, level, message) VALUES ($1,'info','hi')`, runningJobID)
	return runningJobID
}

func mustExec(t *testing.T, d *DB, ctx context.Context, q string, args ...any) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx, q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestDumpRestoreRoundTrip: seed every table → dump → mutate → restore → the
// exact pre-dump state is back, sequences continue past restored ids, and
// interrupted jobs are normalized.
func TestDumpRestoreRoundTrip(t *testing.T) {
	d := openIsolatedDumpDB(t)
	ctx := context.Background()

	// Clean slate on the dedicated dump-test db.
	truncateAll(t, d)

	f := newSender(t, d, "dump")
	sender := f.Sender()
	jobID := seedFullState(t, d, f)

	// Snapshot per-table counts (every dump table must be non-empty, otherwise
	// this test silently stops covering a table).
	want := map[string]int64{}
	var wantTotal int64
	for _, dt := range dumpTables {
		n := tableCount(t, d, dt.Name)
		if n == 0 {
			t.Fatalf("seed left dump table %s empty — round-trip not covered", dt.Name)
		}
		want[dt.Name] = n
		wantTotal += n
	}

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}

	// Mutate AFTER the dump: extra rows + a deletion, all of which the restore
	// must undo.
	f.Seed(hbSeed{project: "P", entity: "extra.go", ts: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)})
	mustExec(t, d, ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
	mustExec(t, d, ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ('intruder','\x00','\x00')`)
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username='intruder'`) })

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	summary, err := d.RestoreAll(ctx, zr)
	if err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	if summary.TotalRows != wantTotal {
		t.Errorf("summary.TotalRows = %d, want %d", summary.TotalRows, wantTotal)
	}
	for _, dt := range dumpTables {
		if got := tableCount(t, d, dt.Name); got != want[dt.Name] {
			t.Errorf("table %s: %d rows after restore, want %d", dt.Name, got, want[dt.Name])
		}
		if summary.Tables[dt.Name] != want[dt.Name] {
			t.Errorf("summary[%s] = %d, want %d", dt.Name, summary.Tables[dt.Name], want[dt.Name])
		}
	}

	// The post-dump mutations are gone.
	if n := scalarCount(t, d, ctx, `SELECT count(*) FROM users WHERE username=$1`, "intruder"); n != 0 {
		t.Errorf("intruder user survived the restore")
	}
	if n := scalarCount(t, d, ctx, `SELECT count(*) FROM curation_rules WHERE sender=$1`, sender); n != 1 {
		t.Errorf("curation rule not restored (count=%d)", n)
	}

	// The running job was normalized to failed.
	var state string
	if err := d.Pool.QueryRow(ctx, `SELECT state FROM import_jobs WHERE id=$1`, jobID).Scan(&state); err != nil {
		t.Fatalf("job state: %v", err)
	}
	if state != "failed" {
		t.Errorf("restored running job state = %q, want failed", state)
	}

	// Serial sequences continue past restored ids (no duplicate-PK on ingest).
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO heartbeats (sender, entity, ty, time_sent) VALUES ($1,'post-restore.go','file',now())`,
		sender); err != nil {
		t.Errorf("insert after restore hit a sequence collision: %v", err)
	}
	var newSpaceID int
	if err := d.Pool.QueryRow(ctx, `INSERT INTO spaces (owner, name) VALUES ($1,'after') RETURNING id`, sender).Scan(&newSpaceID); err != nil {
		t.Errorf("space insert after restore: %v", err)
	}

	// Derived data survived the round trip intact.
	s, err := d.GetDerivedStatus(ctx, sender)
	if err != nil {
		t.Fatalf("derived status: %v", err)
	}
	if !s.InSync {
		t.Errorf("derived data out of sync after restore: %+v", s)
	}
}

// buildArchive assembles an in-memory zip from name -> content.
func buildArchive(t *testing.T, entries map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

// TestRestoreValidationLeavesDataUntouched: foreign/malformed/mismatched
// archives are rejected before anything is truncated.
func TestRestoreValidationLeavesDataUntouched(t *testing.T) {
	// Validation failures never mutate anything, but run on the dedicated dump
	// DB anyway — a regression here would otherwise truncate the shared DB.
	d := openIsolatedDumpDB(t)
	ctx := context.Background()

	f := newSender(t, d, "dumpval")
	f.Seed(hbSeed{project: "P", ts: time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)})
	before := tableCount(t, d, "heartbeats")

	var currentGoose int64
	if err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&currentGoose); err != nil {
		t.Fatal(err)
	}
	mkManifest := func(app string, format int, goose int64) string {
		b, _ := json.Marshal(dumpManifest{App: app, FormatVersion: format, GooseVersion: goose})
		return string(b)
	}

	cases := []struct {
		name    string
		entries map[string]string
		wantVer bool // expect RestoreVersionError instead of RestoreValidationError
	}{
		{"no manifest", map[string]string{"random.txt": "hi"}, false},
		{"garbage manifest", map[string]string{manifestName: "{not json"}, false},
		{"foreign app", map[string]string{manifestName: mkManifest("otherapp", 1, currentGoose)}, false},
		{"future format", map[string]string{manifestName: mkManifest(dumpAppID, 99, currentGoose)}, false},
		{"goose mismatch", map[string]string{manifestName: mkManifest(dumpAppID, 1, currentGoose + 7)}, true},
		{"missing tables", map[string]string{manifestName: mkManifest(dumpAppID, 1, currentGoose)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.RestoreAll(ctx, buildArchive(t, tc.entries))
			if err == nil {
				t.Fatal("RestoreAll accepted an invalid archive")
			}
			var verr *RestoreValidationError
			var sverr *RestoreVersionError
			switch {
			case tc.wantVer && !errors.As(err, &sverr):
				t.Fatalf("want RestoreVersionError, got %T: %v", err, err)
			case !tc.wantVer && !errors.As(err, &verr):
				t.Fatalf("want RestoreValidationError, got %T: %v", err, err)
			}
			if got := tableCount(t, d, "heartbeats"); got != before {
				t.Fatalf("invalid archive mutated data: heartbeats %d -> %d", before, got)
			}
		})
	}
}
