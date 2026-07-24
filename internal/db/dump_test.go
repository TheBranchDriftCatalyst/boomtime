package db

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

// wakatimeBlobFixture is a deterministic non-null bytea placeholder used by
// dump round-trip tests. It is NOT valid AES-GCM ciphertext (no decrypt is
// attempted at the db-package layer) — just a byte pattern that must survive
// the export → truncate → import round trip byte-for-byte.
var wakatimeBlobFixture = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, // 12-byte nonce prefix
	0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe, // fake sealed payload
	0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, // + fake GCM tag
	0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
}

// seedFullState populates at least one row in EVERY dump table for a sender and
// returns the id of a 'running' import job (used to assert normalization).
func seedFullState(t *testing.T, d *DB, f *SenderFixture) (runningJobID int) {
	t.Helper()
	ctx := f.Ctx()
	sender := f.Sender()

	// gaka-awh.3: back-fill EVERY user-owned column that the dump now carries
	// so the round-trip test proves each column survives byte-for-byte.
	mustExec(t, d, ctx, `
		UPDATE users
		   SET encrypted_wakatime_key   = $2,
		       wakatime_key_status      = 'valid',
		       wakatime_key_checked_at  = '2026-03-01T12:00:00Z',
		       public_profile_enabled   = true,
		       public_slug              = $3
		 WHERE username = $1`,
		sender, wakatimeBlobFixture, "slug-"+sender)

	// Heartbeats (+projects via the fixture) with exact gaps, then the rollup.
	tmpl := hbSeed{
		project: "P", language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding",
	}
	f.Block(tmpl, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC), 5, 60)
	f.RefreshRollup(time.Unix(0, 0).UTC())

	// Tokens (post-v31 hashed-only).
	tokHash := sha256.Sum256([]byte("tok_" + sender))
	refreshHash := sha256.Sum256([]byte("refresh_" + sender))
	mustExec(t, d, ctx, `INSERT INTO auth_tokens (owner, hashed_token, token_name) VALUES ($1,$2,'backup-test')`,
		sender, tokHash[:])
	mustExec(t, d, ctx, `INSERT INTO refresh_tokens (owner, hashed_refresh_token, token_expiry) VALUES ($1,$2,now())`,
		sender, refreshHash[:])

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
	// gaka-awh.3: the seed now includes an encrypted_wakatime_key ciphertext,
	// so the restore-time gate requires BOOM_ENCRYPTION_KEY to be present.
	// Any non-empty value satisfies the presence-only check; the db-package
	// tests never call Decrypt, so key validity isn't exercised here.
	t.Setenv(encryptionKeyEnvName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
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

	// gaka-awh.3: the encrypted Wakatime key (+ its status metadata + public
	// profile flag/slug) survived byte-for-byte. This is the whole point of
	// widening the users dump — the earlier version silently NULL'd these
	// on every restore, wiping every user's saved key.
	var (
		gotBlob        []byte
		gotStatus      *string
		gotCheckedAt   *time.Time
		gotPublicEnab  bool
		gotSlug        *string
	)
	if err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_wakatime_key, wakatime_key_status, wakatime_key_checked_at,
		        public_profile_enabled, public_slug
		   FROM users WHERE username=$1`, sender).Scan(
		&gotBlob, &gotStatus, &gotCheckedAt, &gotPublicEnab, &gotSlug); err != nil {
		t.Fatalf("read restored user cols: %v", err)
	}
	if !bytes.Equal(gotBlob, wakatimeBlobFixture) {
		t.Errorf("encrypted_wakatime_key drift after restore:\n got=%x\nwant=%x", gotBlob, wakatimeBlobFixture)
	}
	if gotStatus == nil || *gotStatus != "valid" {
		t.Errorf("wakatime_key_status after restore = %v, want 'valid'", gotStatus)
	}
	if gotCheckedAt == nil {
		t.Errorf("wakatime_key_checked_at NULL after restore")
	}
	if !gotPublicEnab {
		t.Errorf("public_profile_enabled = false after restore, want true")
	}
	if gotSlug == nil || *gotSlug != "slug-"+sender {
		t.Errorf("public_slug after restore = %v, want %q", gotSlug, "slug-"+sender)
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

// TestDumpUsersColumnsIncludeEncryptedSecrets is the pure-Go, no-DB, anti-
// tautology unit test for the users-table column widening (gaka-awh.3).
//
// Layer coverage (why this test exists and what it catches uniquely):
//
//   - Unit layer catches the RED-TEAM regression itself: someone deleting a
//     column from dumpTables makes this test go red WITHOUT waiting for the
//     round-trip test to also flag it. It also catches a column being
//     accidentally removed even if no test happens to populate that column.
//
//   - Integration layer (TestDumpRestoreRoundTrip) is where we prove the
//     ciphertext survives an actual TRUNCATE + COPY cycle byte-for-byte.
//
//   - HTTP round-trip layer (handler_test) exercises the auth + confirm +
//     content-type + Content-Disposition surface.
func TestDumpUsersColumnsIncludeEncryptedSecrets(t *testing.T) {
	var users dumpTable
	for _, dt := range dumpTables {
		if dt.Name == "users" {
			users = dt
			break
		}
	}
	if users.Name == "" {
		t.Fatal("users not in dumpTables")
	}
	// Every column that must be dumped so a restore doesn't silently wipe a
	// user's saved wakatime key + public profile settings.
	required := []string{
		"username", "hashed_password", "salt_used",
		"encrypted_wakatime_key", "wakatime_key_status", "wakatime_key_checked_at",
		"public_profile_enabled", "public_slug",
	}
	have := make(map[string]bool, len(users.Columns))
	for _, c := range users.Columns {
		have[c] = true
	}
	for _, need := range required {
		if !have[need] {
			t.Errorf("dumpTables[users] missing required column %q — restore would silently drop it", need)
		}
	}
}

// TestDumpNeverIncludesDotenv is the regression lock for the "backup ZIP must
// never contain the process .env" invariant. The dump only lists application
// tables (see dumpTables) — there is no file-system read path — but a future
// well-meaning change ("include the config for portability") would be a
// devastating leak: BOOM_ENCRYPTION_KEY in the .env plus a ciphertext row in
// users == plaintext Wakatime key. Fail loudly if any archive entry ever
// contains ".env" in its name.
//
// Layer coverage: unit-only (no DB required), asserts the dump's file-list
// shape. Integration/E2E tests can't easily prove a NEGATIVE ("we didn't
// include this thing") without the fixed enumerated allow-list this test
// enforces.
func TestDumpNeverIncludesDotenv(t *testing.T) {
	d := openIsolatedDumpDB(t)
	ctx := context.Background()

	// Minimal seed: one user, no other rows needed — we're inspecting the
	// archive's file list, not per-table contents.
	truncateAll(t, d)
	mustExec(t, d, ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ('dotenv-guard','\x00','\x00')`)

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	// The dump's file list must be exactly: manifest.json + tables/*.copy.
	// Any name containing ".env" (case-insensitive) is an immediate fail.
	allowedTables := make(map[string]bool, len(dumpTables))
	for _, dt := range dumpTables {
		allowedTables[entryName(dt.Name)] = true
	}
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		if strings.Contains(lower, ".env") {
			t.Errorf("backup archive contains a .env-like entry %q — this is a critical leak vector", f.Name)
		}
		if f.Name == manifestName {
			continue
		}
		if !allowedTables[f.Name] {
			t.Errorf("backup archive contains unexpected entry %q (not manifest, not a known table)", f.Name)
		}
	}
}

// TestRestoreRefusesWhenEncryptionKeyMissing proves the restore-time gate:
// when the archive carries an encrypted_wakatime_key ciphertext AND the
// current process has no BOOM_ENCRYPTION_KEY, the restore is refused BEFORE
// any TRUNCATE. Without this gate, a cross-environment restore would silently
// leave every user's saved key encrypted under a key nobody has.
//
// Layer coverage: integration (requires an isolated DB) — this is the only
// layer that can prove "nothing was truncated" because the check happens
// inside RestoreAll, after manifest validation but before the destructive tx.
func TestRestoreRefusesWhenEncryptionKeyMissing(t *testing.T) {
	d := openIsolatedDumpDB(t)
	ctx := context.Background()

	truncateAll(t, d)
	f := newSender(t, d, "encgate")
	sender := f.Sender()

	// Seed just enough to satisfy round-trip expectations + set the ciphertext.
	mustExec(t, d, ctx, `
		UPDATE users
		   SET encrypted_wakatime_key  = $2,
		       wakatime_key_status     = 'valid',
		       wakatime_key_checked_at = now()
		 WHERE username = $1`, sender, wakatimeBlobFixture)
	tmpl := hbSeed{project: "P", language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding"}
	f.Block(tmpl, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC), 2, 60)

	// Snapshot the current heartbeat count — a refused restore must leave it
	// UNCHANGED (no TRUNCATE has run).
	beforeHB := tableCount(t, d, "heartbeats")

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}
	archive := buf.Bytes()

	// Clear the encryption env for this test only. t.Setenv restores on cleanup.
	t.Setenv(encryptionKeyEnvName, "")

	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	_, err = d.RestoreAll(ctx, zr)
	if err == nil {
		t.Fatal("RestoreAll accepted a dump with ciphertext under an unset BOOM_ENCRYPTION_KEY")
	}
	var verr *RestoreValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want RestoreValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(verr.Msg, "BOOM_ENCRYPTION_KEY") {
		t.Errorf("gate error message does not mention BOOM_ENCRYPTION_KEY: %q", verr.Msg)
	}
	// Nothing was truncated: the pre-restore heartbeat count is intact.
	if after := tableCount(t, d, "heartbeats"); after != beforeHB {
		t.Errorf("gated restore mutated data: heartbeats %d -> %d", beforeHB, after)
	}

	// Sanity: re-set the env, restore succeeds, ciphertext survives.
	t.Setenv(encryptionKeyEnvName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=") // any non-empty value satisfies the presence gate
	zr2, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("zip.NewReader 2: %v", err)
	}
	if _, err := d.RestoreAll(ctx, zr2); err != nil {
		t.Fatalf("RestoreAll with env set: %v", err)
	}
	var got []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, sender).Scan(&got); err != nil {
		t.Fatalf("read ciphertext post-restore: %v", err)
	}
	if !bytes.Equal(got, wakatimeBlobFixture) {
		t.Errorf("ciphertext drift after gate-permitted restore: got %x want %x", got, wakatimeBlobFixture)
	}
}

// usersCopyPayload is a helper that returns the raw COPY text bytes of the
// users table entry from the given archive. Useful for asserts that "the
// ciphertext column IS present in the emitted CSV/COPY output".
func usersCopyPayload(t *testing.T, zr *zip.Reader) []byte {
	t.Helper()
	f, err := zr.Open(entryName("users"))
	if err != nil {
		t.Fatalf("open users entry: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read users entry: %v", err)
	}
	return data
}

// TestDumpUsersRowHasCiphertextColumn is the anti-tautology sibling of
// TestDumpUsersColumnsIncludeEncryptedSecrets: it inspects the ACTUAL emitted
// COPY-text payload for the users table and asserts the ciphertext bytes are
// serialised in the encrypted_wakatime_key column position. If the column is
// removed from dumpTables OR skipped by the COPY, this test fails.
//
// Layer coverage: integration (needs a real dump). Combined with the unit
// test above, deleting the column from dumpTables makes BOTH tests fail —
// impossible to silently drop the ciphertext again without CI going red.
func TestDumpUsersRowHasCiphertextColumn(t *testing.T) {
	d := openIsolatedDumpDB(t)
	ctx := context.Background()

	truncateAll(t, d)
	sender := mkSender("cipher-dump")
	mustExec(t, d, ctx, `INSERT INTO users (username, hashed_password, salt_used, encrypted_wakatime_key, wakatime_key_status)
		VALUES ($1, '\x00', '\x00', $2, 'valid')`, sender, wakatimeBlobFixture)

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	// Find the column index in the current users column list.
	var usersCols []string
	for _, dt := range dumpTables {
		if dt.Name == "users" {
			usersCols = dt.Columns
			break
		}
	}
	col := -1
	for i, c := range usersCols {
		if c == "encrypted_wakatime_key" {
			col = i
			break
		}
	}
	if col < 0 {
		t.Fatal("encrypted_wakatime_key missing from dumpTables[users]")
	}

	payload := usersCopyPayload(t, zr)
	// Find the row for our sender and pull the ciphertext column.
	var found bool
	for _, line := range strings.Split(strings.TrimRight(string(payload), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) <= col {
			continue
		}
		if fields[0] != sender {
			continue
		}
		found = true
		if fields[col] == `\N` {
			t.Errorf("encrypted_wakatime_key emitted as NULL for %s — expected the seeded ciphertext", sender)
		}
		// COPY text encodes bytea as `\x` hex prefix. Verify the bytes are
		// present verbatim in that encoded form.
		wantHex := `\\x` + bytesToHex(wakatimeBlobFixture)
		if fields[col] != wantHex {
			t.Errorf("ciphertext column drift: got %q want %q", fields[col], wantHex)
		}
	}
	if !found {
		t.Fatalf("did not find seeded user %q in users COPY payload", sender)
	}
}

// bytesToHex is a tiny helper that mirrors Postgres bytea's hex output ('\xAB…').
func bytesToHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}
