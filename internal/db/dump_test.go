// dump_ginkgo_test.go — ginkgo mirror of dump_test.go (gaka-0vp.13).
// 1:1 case map (6 stdlib TestXxx incl 6 subtests → 5 Its + 1 DescribeTable(6)):
//
//	TestDumpRestoreRoundTrip                        → It "seed → dump → mutate → restore round trip"
//	TestRestoreValidationLeavesDataUntouched        → DescribeTable "restore validation cases (6)"
//	TestDumpUsersColumnsIncludeEncryptedSecrets     → It "dumpTables[users] includes every encrypted-secret column"
//	TestDumpNeverIncludesDotenv                     → It "backup archive never contains .env"
//	TestRestoreRefusesWhenEncryptionKeyMissing      → It "restore refused when BOOM_ENCRYPTION_KEY missing (gate)"
//	TestDumpUsersRowHasCiphertextColumn             → It "dump users COPY payload contains ciphertext bytes"
package db

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// openIsolatedDumpDBG mirrors openIsolatedDumpDB for ginkgo.
func openIsolatedDumpDBG() *DB {
	if !dbReady {
		ginkgo.Skip("isolated test database unavailable: " + dbSkipMsg)
	}
	url := maintenanceURLFor(testDatabaseURL(), testDBName+"_dump")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ensureTestDatabase(ctx, url); err != nil {
		ginkgo.Skip("could not ensure " + testDBName + "_dump: " + err.Error())
	}
	Expect(MigrateURL(ctx, url)).To(Succeed())
	d, err := New(ctx, url)
	if err != nil {
		ginkgo.Skip("connect " + testDBName + "_dump: " + err.Error())
	}
	ginkgo.DeferCleanup(func() { d.Close() })
	return d
}

// truncateAllG mirrors truncateAll for ginkgo.
func truncateAllG(d *DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := d.Pool.Exec(ctx, `TRUNCATE
		import_job_logs, import_jobs,
		hb_rollup_daily, heartbeats,
		badges, space_rules, spaces,
		curation_rules, projects,
		auth_tokens, refresh_tokens, users
		RESTART IDENTITY CASCADE`)
	Expect(err).NotTo(HaveOccurred())
}

// tableCountG returns SELECT count(*).
func tableCountG(d *DB, table string) int64 {
	var n int64
	err := d.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// mustExecG errors go through gomega.
func mustExecG(d *DB, ctx context.Context, q string, args ...any) {
	_, err := d.Pool.Exec(ctx, q, args...)
	Expect(err).NotTo(HaveOccurred())
}

// seedFullStateG mirrors seedFullState.
func seedFullStateG(d *DB, f *SenderFixtureG) (runningJobID int) {
	ctx := f.Ctx()
	sender := f.Sender()

	mustExecG(d, ctx, `
		UPDATE users
		   SET encrypted_wakatime_key   = $2,
		       wakatime_key_status      = 'valid',
		       wakatime_key_checked_at  = '2026-03-01T12:00:00Z',
		       public_profile_enabled   = true,
		       public_slug              = $3
		 WHERE username = $1`,
		sender, wakatimeBlobFixture, "slug-"+sender)

	tmpl := hbSeed{
		project: "P", language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding",
	}
	f.Block(tmpl, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC), 5, 60)
	f.RefreshRollup(time.Unix(0, 0).UTC())

	tokHash := sha256.Sum256([]byte("tok_" + sender))
	refreshHash := sha256.Sum256([]byte("refresh_" + sender))
	mustExecG(d, ctx, `INSERT INTO auth_tokens (owner, hashed_token, token_name) VALUES ($1,$2,'backup-test')`, sender, tokHash[:])
	mustExecG(d, ctx, `INSERT INTO refresh_tokens (owner, hashed_refresh_token, token_expiry) VALUES ($1,$2,now())`, sender, refreshHash[:])
	mustExecG(d, ctx, `INSERT INTO badges (username, project) VALUES ($1,'P')`, sender)

	newVal := "Renamed"
	_, err := d.CreateCurationRule(ctx, sender, "project", "rename", "exact", "P", &newVal)
	Expect(err).NotTo(HaveOccurred())

	var spaceID int
	err = d.Pool.QueryRow(ctx, `INSERT INTO spaces (owner, name) VALUES ($1,'sp') RETURNING id`, sender).Scan(&spaceID)
	Expect(err).NotTo(HaveOccurred())
	mustExecG(d, ctx, `INSERT INTO space_rules (space_id, axis, match_value) VALUES ($1,'project','P')`, spaceID)

	err = d.Pool.QueryRow(ctx,
		`INSERT INTO import_jobs (value, state, owner) VALUES ('{}'::jsonb,'running',$1) RETURNING id`,
		sender).Scan(&runningJobID)
	Expect(err).NotTo(HaveOccurred())
	mustExecG(d, ctx, `INSERT INTO import_job_logs (job_id, level, message) VALUES ($1,'info','hi')`, runningJobID)
	return runningJobID
}

// buildArchiveG assembles an in-memory zip from name -> content.
func buildArchiveG(entries map[string]string) *zip.Reader {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte(content))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(zw.Close()).To(Succeed())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	Expect(err).NotTo(HaveOccurred())
	return zr
}

// setEnvG mirrors t.Setenv with restore-on-cleanup.
func setEnvG(key, val string) {
	prev, ok := os.LookupEnv(key)
	os.Setenv(key, val)
	ginkgo.DeferCleanup(func() {
		if ok {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
	})
}

// newSenderInDump mirrors newSender for an isolated dump DB.
func newSenderInDump(d *DB, prefix string) *SenderFixtureG {
	ctx := context.Background()
	name := mkSender(prefix)
	cleanupSenderG(d, ctx, name)
	ensureUserG(d, ctx, name)
	return &SenderFixtureG{db: d, ctx: ctx, name: name}
}

var _ = ginkgo.Describe("dump + restore", func() {
	ginkgo.It("seed → dump → mutate → restore round trip preserves every table byte-for-byte (gaka-awh.3)", func() {
		setEnvG(encryptionKeyEnvName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
		d := openIsolatedDumpDBG()
		ctx := context.Background()

		truncateAllG(d)

		f := newSenderInDump(d, "dump")
		sender := f.Sender()
		jobID := seedFullStateG(d, f)

		want := map[string]int64{}
		var wantTotal int64
		for _, dt := range dumpTables {
			n := tableCountG(d, dt.Name)
			Expect(n).NotTo(BeZero(), "seed left dump table %s empty", dt.Name)
			want[dt.Name] = n
			wantTotal += n
		}

		var buf bytes.Buffer
		Expect(d.DumpAll(ctx, &buf)).To(Succeed())

		f.Seed(hbSeed{project: "P", entity: "extra.go", ts: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)})
		mustExecG(d, ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
		mustExecG(d, ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ('intruder','\x00','\x00')`)
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username='intruder'`) })

		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		Expect(err).NotTo(HaveOccurred())
		summary, err := d.RestoreAll(ctx, zr)
		Expect(err).NotTo(HaveOccurred())

		Expect(summary.TotalRows).To(Equal(wantTotal))
		for _, dt := range dumpTables {
			Expect(tableCountG(d, dt.Name)).To(Equal(want[dt.Name]), "table %s rows after restore", dt.Name)
			Expect(summary.Tables[dt.Name]).To(Equal(want[dt.Name]), "summary[%s]", dt.Name)
		}

		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM users WHERE username=$1`, "intruder")).To(Equal(0))
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM curation_rules WHERE sender=$1`, sender)).To(Equal(1))

		var state string
		err = d.Pool.QueryRow(ctx, `SELECT state FROM import_jobs WHERE id=$1`, jobID).Scan(&state)
		Expect(err).NotTo(HaveOccurred())
		Expect(state).To(Equal("failed"), "running job normalized to failed")

		_, err = d.Pool.Exec(ctx,
			`INSERT INTO heartbeats (sender, entity, ty, time_sent) VALUES ($1,'post-restore.go','file',now())`, sender)
		Expect(err).NotTo(HaveOccurred())
		var newSpaceID int
		err = d.Pool.QueryRow(ctx, `INSERT INTO spaces (owner, name) VALUES ($1,'after') RETURNING id`, sender).Scan(&newSpaceID)
		Expect(err).NotTo(HaveOccurred())

		s, err := d.GetDerivedStatus(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.InSync).To(BeTrue())

		var (
			gotBlob       []byte
			gotStatus     *string
			gotCheckedAt  *time.Time
			gotPublicEnab bool
			gotSlug       *string
		)
		err = d.Pool.QueryRow(ctx,
			`SELECT encrypted_wakatime_key, wakatime_key_status, wakatime_key_checked_at,
			        public_profile_enabled, public_slug
			   FROM users WHERE username=$1`, sender).Scan(
			&gotBlob, &gotStatus, &gotCheckedAt, &gotPublicEnab, &gotSlug)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(gotBlob, wakatimeBlobFixture)).To(BeTrue())
		Expect(gotStatus).NotTo(BeNil())
		Expect(*gotStatus).To(Equal("valid"))
		Expect(gotCheckedAt).NotTo(BeNil())
		Expect(gotPublicEnab).To(BeTrue())
		Expect(gotSlug).NotTo(BeNil())
		Expect(*gotSlug).To(Equal("slug-" + sender))
	})

	ginkgo.DescribeTable("restore validation rejects invalid archives BEFORE any TRUNCATE",
		func(entries map[string]string, wantVer bool, name string) {
			d := openIsolatedDumpDBG()
			ctx := context.Background()

			f := newSenderInDump(d, "dumpval")
			f.Seed(hbSeed{project: "P", ts: time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)})
			before := tableCountG(d, "heartbeats")

			var currentGoose int64
			err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&currentGoose)
			Expect(err).NotTo(HaveOccurred())

			// Late-bind case-specific manifests that depend on currentGoose.
			mkManifest := func(app string, format int, goose int64) string {
				b, _ := json.Marshal(dumpManifest{App: app, FormatVersion: format, GooseVersion: goose})
				return string(b)
			}
			// Materialize any nil-templated goose values against currentGoose.
			finalEntries := make(map[string]string, len(entries))
			for k, v := range entries {
				if v == "__CURRENT_GOOSE__" {
					finalEntries[k] = mkManifest(dumpAppID, 1, currentGoose)
				} else if v == "__GOOSE_PLUS_7__" {
					finalEntries[k] = mkManifest(dumpAppID, 1, currentGoose+7)
				} else if v == "__FOREIGN_APP__" {
					finalEntries[k] = mkManifest("otherapp", 1, currentGoose)
				} else if v == "__FUTURE_FORMAT__" {
					finalEntries[k] = mkManifest(dumpAppID, 99, currentGoose)
				} else {
					finalEntries[k] = v
				}
			}

			_, err = d.RestoreAll(ctx, buildArchiveG(finalEntries))
			Expect(err).To(HaveOccurred(), "RestoreAll accepted an invalid archive: %s", name)
			var verr *RestoreValidationError
			var sverr *RestoreVersionError
			if wantVer {
				Expect(errors.As(err, &sverr)).To(BeTrue(), "want RestoreVersionError, got %T", err)
			} else {
				Expect(errors.As(err, &verr)).To(BeTrue(), "want RestoreValidationError, got %T", err)
			}
			Expect(tableCountG(d, "heartbeats")).To(Equal(before), "invalid archive mutated data")
		},
		ginkgo.Entry("no manifest", map[string]string{"random.txt": "hi"}, false, "no manifest"),
		ginkgo.Entry("garbage manifest", map[string]string{manifestName: "{not json"}, false, "garbage manifest"),
		ginkgo.Entry("foreign app", map[string]string{manifestName: "__FOREIGN_APP__"}, false, "foreign app"),
		ginkgo.Entry("future format", map[string]string{manifestName: "__FUTURE_FORMAT__"}, false, "future format"),
		ginkgo.Entry("goose mismatch", map[string]string{manifestName: "__GOOSE_PLUS_7__"}, true, "goose mismatch"),
		ginkgo.Entry("missing tables", map[string]string{manifestName: "__CURRENT_GOOSE__"}, false, "missing tables"),
	)

	ginkgo.It("dumpTables[users] includes every encrypted-secret column so restore doesn't silently drop it (gaka-awh.3)", func() {
		var users dumpTable
		for _, dt := range dumpTables {
			if dt.Name == "users" {
				users = dt
				break
			}
		}
		Expect(users.Name).NotTo(BeEmpty(), "users not in dumpTables")
		required := []string{
			"username", "hashed_password", "salt_used",
			"encrypted_wakatime_key", "wakatime_key_status", "wakatime_key_checked_at",
			"public_profile_enabled", "public_slug",
		}
		have := map[string]bool{}
		for _, c := range users.Columns {
			have[c] = true
		}
		for _, need := range required {
			Expect(have[need]).To(BeTrue(), "dumpTables[users] missing required column %q", need)
		}
	})

	ginkgo.It("backup archive never contains a .env-like entry", func() {
		d := openIsolatedDumpDBG()
		ctx := context.Background()

		truncateAllG(d)
		mustExecG(d, ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ('dotenv-guard','\x00','\x00')`)

		var buf bytes.Buffer
		Expect(d.DumpAll(ctx, &buf)).To(Succeed())
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		Expect(err).NotTo(HaveOccurred())

		allowedTables := map[string]bool{}
		for _, dt := range dumpTables {
			allowedTables[entryName(dt.Name)] = true
		}
		for _, f := range zr.File {
			lower := strings.ToLower(f.Name)
			Expect(strings.Contains(lower, ".env")).To(BeFalse(), "backup archive contains .env-like entry %q — critical leak", f.Name)
			if f.Name == manifestName {
				continue
			}
			Expect(allowedTables[f.Name]).To(BeTrue(), "backup archive contains unexpected entry %q", f.Name)
		}
	})

	ginkgo.It("restore refused BEFORE any TRUNCATE when ciphertext present + BOOM_ENCRYPTION_KEY unset (gaka-awh.3 gate)", func() {
		d := openIsolatedDumpDBG()
		ctx := context.Background()

		truncateAllG(d)
		f := newSenderInDump(d, "encgate")
		sender := f.Sender()

		mustExecG(d, ctx, `
			UPDATE users
			   SET encrypted_wakatime_key  = $2,
			       wakatime_key_status     = 'valid',
			       wakatime_key_checked_at = now()
			 WHERE username = $1`, sender, wakatimeBlobFixture)
		tmpl := hbSeed{project: "P", language: "Go", editor: "vim", plugin: "pl",
			machine: "m", platform: "linux", branch: "main", category: "Coding"}
		f.Block(tmpl, time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC), 2, 60)

		beforeHB := tableCountG(d, "heartbeats")

		var buf bytes.Buffer
		Expect(d.DumpAll(ctx, &buf)).To(Succeed())
		archive := buf.Bytes()

		setEnvG(encryptionKeyEnvName, "")

		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		Expect(err).NotTo(HaveOccurred())
		_, err = d.RestoreAll(ctx, zr)
		Expect(err).To(HaveOccurred())
		var verr *RestoreValidationError
		Expect(errors.As(err, &verr)).To(BeTrue(), "want RestoreValidationError, got %T", err)
		Expect(strings.Contains(verr.Msg, "BOOM_ENCRYPTION_KEY")).To(BeTrue(), "gate error should mention BOOM_ENCRYPTION_KEY")
		Expect(tableCountG(d, "heartbeats")).To(Equal(beforeHB), "gated restore mutated data")

		setEnvG(encryptionKeyEnvName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
		zr2, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		Expect(err).NotTo(HaveOccurred())
		_, err = d.RestoreAll(ctx, zr2)
		Expect(err).NotTo(HaveOccurred())
		var got []byte
		err = d.Pool.QueryRow(ctx, `SELECT encrypted_wakatime_key FROM users WHERE username=$1`, sender).Scan(&got)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(got, wakatimeBlobFixture)).To(BeTrue())
	})

	ginkgo.It("dump users COPY payload carries ciphertext bytes verbatim in the expected column position", func() {
		d := openIsolatedDumpDBG()
		ctx := context.Background()

		truncateAllG(d)
		sender := mkSender("cipher-dump")
		mustExecG(d, ctx, `INSERT INTO users (username, hashed_password, salt_used, encrypted_wakatime_key, wakatime_key_status)
			VALUES ($1, '\x00', '\x00', $2, 'valid')`, sender, wakatimeBlobFixture)

		var buf bytes.Buffer
		Expect(d.DumpAll(ctx, &buf)).To(Succeed())
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		Expect(err).NotTo(HaveOccurred())

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
		Expect(col).To(BeNumerically(">=", 0))

		fp, err := zr.Open(entryName("users"))
		Expect(err).NotTo(HaveOccurred())
		defer fp.Close()
		payload, err := io.ReadAll(fp)
		Expect(err).NotTo(HaveOccurred())

		var found bool
		for _, line := range strings.Split(strings.TrimRight(string(payload), "\n"), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) <= col || fields[0] != sender {
				continue
			}
			found = true
			Expect(fields[col]).NotTo(Equal(`\N`), "ciphertext emitted as NULL")
			wantHex := `\\x` + bytesToHex(wakatimeBlobFixture)
			Expect(fields[col]).To(Equal(wantHex))
		}
		Expect(found).To(BeTrue(), "did not find seeded user %q in users COPY payload", sender)
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
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

func tableCount(t *testing.T, d *DB, table string) int64 {
	t.Helper()
	var n int64
	if err := d.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

var wakatimeBlobFixture = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, // 12-byte nonce prefix
	0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe, // fake sealed payload
	0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, // + fake GCM tag
	0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
}

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

// -- dump extras (gaka-0vp.17 extractor missed these due to brace-in-string) --
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

func bytesToHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}
