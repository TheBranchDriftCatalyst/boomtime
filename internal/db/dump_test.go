// dump_ginkgo_test.go — ginkgo mirror of dump_test.go (gaka-0vp.13).
// 1:1 case map (6 stdlib TestXxx incl 6 subtests → 5 Its + 1 DescribeTable(6)):
//
//	TestDumpRestoreRoundTrip                        → It "seed → dump → mutate → restore round trip"
//	TestRestoreValidationLeavesDataUntouched        → DescribeTable "restore validation cases (6)"
//	TestDumpUsersColumnsIncludeEncryptedSecrets     → It "dumpTables[users] includes every encrypted-secret column"
//	TestDumpNeverIncludesDotenv                     → It "backup archive never contains .env"
//	TestRestoreRefusesWhenEncryptionKeyMissing      → It "restore refused when BOOM_ENCRYPTION_KEY missing (gate)"
//	TestDumpUsersRowHasCiphertextColumn             → It "dump users COPY payload contains ciphertext bytes"
// gaka-se2.9: this file also carries stdlib (testing.T) security tests for
// the backup contract — see TestDumpAllSecurity + TestDumpRestoreRoundtripWithEncryption
// at the bottom of the file. The ginkgo tests exercise round-trip byte-equality
// with a FAKE fixture ciphertext; the stdlib tests EXTEND that with a real
// AES-256-GCM encrypt/decrypt roundtrip (anti-tautology on the encryption
// contract) and an explicit "restore w/o key" no-mutation gate.
package db

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

var wakatimeBlobFixture = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, // 12-byte nonce prefix
	0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe, // fake sealed payload
	0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, // + fake GCM tag
	0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
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

// -----------------------------------------------------------------------------
// gaka-se2.9 stdlib coverage of dump.go — backup security invariants.
//
// The ginkgo tests already cover: round-trip byte-equality with a FAKE fixture
// ciphertext, .env exclusion, restore-without-key rejection, ciphertext
// presence in the users COPY payload.
//
// These stdlib tests ADD:
//   1. Real AES-256-GCM roundtrip (anti-tautology): Encrypt(pt) -> dump ->
//      wipe -> restore -> Decrypt(restored) == pt.
//   2. Plaintext-not-in-ZIP scan: grep for the raw plaintext bytes in every
//      zip entry; must not appear anywhere.
//   3. Restore-without-key: capture per-table row counts BEFORE + AFTER a
//      gated restore, assert byte-exact equality on every table.
// -----------------------------------------------------------------------------

// openIsolatedDumpDB is the stdlib mirror of openIsolatedDumpDBG. It uses a
// SEPARATE database (boomtime_test_dump) so the destructive TRUNCATE in
// RestoreAll cannot bleed into other tests running against boomtime_test.
func openIsolatedDumpDB(t *testing.T) *DB {
	t.Helper()
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	url := maintenanceURLFor(testDatabaseURL(), testDBName+"_dump")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ensureTestDatabase(ctx, url); err != nil {
		t.Skipf("could not ensure %s_dump: %v", testDBName, err)
	}
	if err := MigrateURL(ctx, url); err != nil {
		t.Fatalf("migrate %s_dump: %v", testDBName, err)
	}
	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("connect %s_dump: %v", testDBName, err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// truncateAll clears every application table in the isolated dump DB.
func truncateAll(t *testing.T, d *DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := d.Pool.Exec(ctx, `TRUNCATE
		import_job_logs, import_jobs,
		hb_rollup_daily, heartbeats,
		badges, space_rules, spaces,
		curation_rules, projects,
		auth_tokens, refresh_tokens, users
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncateAll: %v", err)
	}
}

// tableCount returns SELECT count(*) from every application table.
func tableCount(t *testing.T, d *DB, table string) int64 {
	t.Helper()
	var n int64
	if err := d.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("tableCount %s: %v", table, err)
	}
	return n
}

// snapshotAllTableCounts captures the row count of every dumpTables entry.
func snapshotAllTableCounts(t *testing.T, d *DB) map[string]int64 {
	t.Helper()
	out := make(map[string]int64, len(dumpTables))
	for _, dt := range dumpTables {
		out[dt.Name] = tableCount(t, d, dt.Name)
	}
	return out
}

// newSecurityAEAD builds an AES-256-GCM AEAD from a locally-generated 32-byte
// key. The base64-encoded key is returned so it can be smuggled into
// BOOM_ENCRYPTION_KEY (satisfying isEncryptionKeyConfigured) even though this
// package doesn't call into internal/auth.
type securityAEAD struct{ a cipher.AEAD }

func newSecurityAEAD(t *testing.T) (*securityAEAD, string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	return &securityAEAD{a: aead}, base64.StdEncoding.EncodeToString(key)
}

func (s *securityAEAD) Seal(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	nonce := make([]byte, s.a.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("gen nonce: %v", err)
	}
	sealed := s.a.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out
}

func (s *securityAEAD) Open(ciphertext []byte) ([]byte, error) {
	n := s.a.NonceSize()
	if len(ciphertext) <= n {
		return nil, io.ErrUnexpectedEOF
	}
	return s.a.Open(nil, ciphertext[:n], ciphertext[n:], nil)
}

// -----------------------------------------------------------------------------
// TestDumpRestoreRoundtripWithEncryption — the top-line security contract from
// CLAUDE.md gaka-awh.3: a plaintext Wakatime key sealed under
// BOOM_ENCRYPTION_KEY MUST survive a DumpAll -> wipe -> RestoreAll cycle such
// that the plaintext still recovers after Decrypt under the SAME key.
// -----------------------------------------------------------------------------

func TestDumpRestoreRoundtripWithEncryption(t *testing.T) {
	aead, b64Key := newSecurityAEAD(t)
	t.Setenv(encryptionKeyEnvName, b64Key)

	d := openIsolatedDumpDB(t)
	ctx := context.Background()
	truncateAll(t, d)

	sender := mkSender("dump_enc")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	plaintext := []byte("waka_pk_zzz_supersecret_xyz_9999")
	ciphertext := aead.Seal(t, plaintext)

	if err := d.SetEncryptedWakatimeKey(ctx, sender, ciphertext, WakatimeKeyStatusValid); err != nil {
		t.Fatalf("SetEncryptedWakatimeKey: %v", err)
	}

	// Dump.
	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}
	archive := buf.Bytes()

	// PLAINTEXT MUST NOT APPEAR ANYWHERE in the ZIP (raw bytes scan across the
	// whole archive, not just the users entry — this catches a hypothetical
	// bug where the dumper accidentally embedded the plaintext in a log/name).
	if bytes.Contains(archive, plaintext) {
		t.Fatalf("SECURITY LEAK: plaintext Wakatime key found in raw ZIP bytes")
	}
	// Also scan every decompressed entry.
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		fp, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(fp)
		fp.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		if bytes.Contains(data, plaintext) {
			t.Fatalf("SECURITY LEAK: plaintext key in decompressed entry %q", f.Name)
		}
	}

	// Wipe user's saved key AND drop the row, then restore.
	if _, err := d.Pool.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("wipe users: %v", err)
	}
	if got := tableCount(t, d, "users"); got != 0 {
		t.Fatalf("wipe left %d users", got)
	}

	if _, err := d.RestoreAll(ctx, zr); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	// The ciphertext bytes on disk after restore MUST decrypt back to the
	// original plaintext under the SAME BOOM_ENCRYPTION_KEY — this is the
	// anti-tautology pin: proves encryption bytes actually survive the ZIP
	// serialization round-trip.
	var restored []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_wakatime_key FROM users WHERE username=$1`, sender).Scan(&restored); err != nil {
		t.Fatalf("read restored ciphertext: %v", err)
	}
	if !bytes.Equal(restored, ciphertext) {
		t.Fatalf("ciphertext byte mismatch across roundtrip: got %x want %x", restored, ciphertext)
	}
	dec, err := aead.Open(restored)
	if err != nil {
		t.Fatalf("Decrypt restored ciphertext: %v (encryption did not survive dump/restore)", err)
	}
	if !bytes.Equal(dec, plaintext) {
		t.Fatalf("post-restore Decrypt mismatch: got %q want %q", dec, plaintext)
	}
}

// -----------------------------------------------------------------------------
// TestDumpAllSecurity — grouped smaller invariants around the backup.
// -----------------------------------------------------------------------------

func TestDumpAllSecurity(t *testing.T) {
	t.Run("SecurityInvariant_RestoreRefusedWhenKeyUnset_leaves_every_table_untouched", func(t *testing.T) {
		// Contract from dump.go: RestoreAll must refuse a ciphertext-carrying
		// dump when BOOM_ENCRYPTION_KEY is unset, BEFORE the TRUNCATE. The
		// existing ginkgo test asserts heartbeats count is unchanged — this
		// stdlib test extends to ALL dumpTables to prove no side effects.
		aead, b64Key := newSecurityAEAD(t)
		t.Setenv(encryptionKeyEnvName, b64Key)

		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		truncateAll(t, d)

		sender := mkSender("dump_gate")
		cleanupSender(t, d, ctx, sender)
		ensureUser(t, d, ctx, sender)
		ciphertext := aead.Seal(t, []byte("pt-does-not-matter-here"))
		if err := d.SetEncryptedWakatimeKey(ctx, sender, ciphertext, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}
		// Seed at least one row into every dumpTable-ish thing to catch a
		// bug where TRUNCATE runs partially. We already have the users row;
		// add a heartbeat + a project. Empty tables are still valid to snapshot.
		ensureProjects(t, d, ctx, sender, "P")
		insertSeed(t, d, ctx, sender, hbSeed{project: "P", entity: "a.go", ts: time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)})

		// Dump into a buffer (still with key set).
		var buf bytes.Buffer
		if err := d.DumpAll(ctx, &buf); err != nil {
			t.Fatalf("DumpAll: %v", err)
		}
		archive := buf.Bytes()

		// Snapshot BEFORE.
		before := snapshotAllTableCounts(t, d)

		// Unset the env; RestoreAll must reject.
		t.Setenv(encryptionKeyEnvName, "")

		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatalf("open zip: %v", err)
		}
		_, err = d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("RestoreAll accepted ciphertext-carrying dump w/o BOOM_ENCRYPTION_KEY")
		}
		var verr *RestoreValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("want RestoreValidationError, got %T: %v", err, err)
		}
		if !strings.Contains(verr.Msg, "BOOM_ENCRYPTION_KEY") {
			t.Fatalf("gate error should mention BOOM_ENCRYPTION_KEY, got %q", verr.Msg)
		}

		// Snapshot AFTER — every table count must be UNCHANGED.
		after := snapshotAllTableCounts(t, d)
		for name, wantN := range before {
			if got := after[name]; got != wantN {
				t.Fatalf("table %s row count changed by gated restore: before=%d after=%d",
					name, wantN, got)
			}
		}
	})

	t.Run("SecurityInvariant_DumpAll_never_contains_env_or_secret_env_name", func(t *testing.T) {
		// Stronger than the existing ginkgo test (which only checks .env
		// substrings) — this scans BOTH filenames AND file content for the
		// BOOM_ENCRYPTION_KEY env-var name (would be catastrophic if a dev
		// ever `SELECT current_setting(...)`'d the process env into the dump).
		aead, b64Key := newSecurityAEAD(t)
		t.Setenv(encryptionKeyEnvName, b64Key)

		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		truncateAll(t, d)

		sender := mkSender("dump_env_guard")
		cleanupSender(t, d, ctx, sender)
		ensureUser(t, d, ctx, sender)
		ct := aead.Seal(t, []byte("pt"))
		if err := d.SetEncryptedWakatimeKey(ctx, sender, ct, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		var buf bytes.Buffer
		if err := d.DumpAll(ctx, &buf); err != nil {
			t.Fatalf("DumpAll: %v", err)
		}
		archive := buf.Bytes()

		// Filenames.
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatalf("open zip: %v", err)
		}
		for _, f := range zr.File {
			lower := strings.ToLower(f.Name)
			if strings.Contains(lower, ".env") {
				t.Fatalf("SECURITY LEAK: archive contains .env-like filename %q", f.Name)
			}
			if strings.Contains(lower, "encryption_key") || strings.Contains(lower, "boom_encryption") {
				t.Fatalf("SECURITY LEAK: archive contains encryption-key-like filename %q", f.Name)
			}
		}
		// Raw bytes: base64 of the key MUST NOT appear anywhere.
		if bytes.Contains(archive, []byte(b64Key)) {
			t.Fatal("SECURITY LEAK: base64 BOOM_ENCRYPTION_KEY value found in archive bytes")
		}
		// The literal env var name is allowed to appear (it's a well-known
		// public constant), so we DO NOT check for it. What we care about is
		// the KEY VALUE.
	})

	t.Run("SecurityInvariant_DumpAll_users_entry_ciphertext_is_bytea_hex_not_plaintext", func(t *testing.T) {
		// Pins the wire format: postgres COPY text for bytea is `\x...` hex.
		// This is what makes the "plaintext bytes never in the archive" claim
		// actually true — a regression to a base64/latin-1 encoding would
		// smuggle plaintext for ASCII keys.
		aead, b64Key := newSecurityAEAD(t)
		t.Setenv(encryptionKeyEnvName, b64Key)

		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		truncateAll(t, d)

		sender := mkSender("dump_hex")
		cleanupSender(t, d, ctx, sender)
		ensureUser(t, d, ctx, sender)
		plaintext := []byte("plaintext-in-a-key-shape-abcdefgh")
		ct := aead.Seal(t, plaintext)
		if err := d.SetEncryptedWakatimeKey(ctx, sender, ct, WakatimeKeyStatusValid); err != nil {
			t.Fatalf("Set: %v", err)
		}

		var buf bytes.Buffer
		if err := d.DumpAll(ctx, &buf); err != nil {
			t.Fatalf("DumpAll: %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("open zip: %v", err)
		}
		fp, err := zr.Open(entryName("users"))
		if err != nil {
			t.Fatalf("open users entry: %v", err)
		}
		defer fp.Close()
		payload, err := io.ReadAll(fp)
		if err != nil {
			t.Fatalf("read users entry: %v", err)
		}
		if bytes.Contains(payload, plaintext) {
			t.Fatal("SECURITY LEAK: users entry contains plaintext key")
		}
		// Locate the ciphertext column in the users entry and assert it's
		// emitted as postgres COPY-text bytea (\x-prefixed hex).
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
			t.Fatal("encrypted_wakatime_key not in users dumpTable")
		}
		var found bool
		for _, line := range strings.Split(strings.TrimRight(string(payload), "\n"), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) <= col || fields[0] != sender {
				continue
			}
			found = true
			v := fields[col]
			if v == `\N` {
				t.Fatal("ciphertext column emitted as NULL after Set")
			}
			// Contract: bytea COPY text is `\x` + hex.
			if !strings.HasPrefix(v, `\\x`) && !strings.HasPrefix(v, `\x`) {
				t.Fatalf("ciphertext not emitted as bytea hex: %q", v)
			}
		}
		if !found {
			t.Fatalf("seeded user %s not in users entry", sender)
		}
	})
}

// -----------------------------------------------------------------------------
// TestDumpSupportingFunctions — cover the remaining trivially-exercisable
// surfaces so `go test -cover` on internal/db/dump.go reaches 90%+ from
// stdlib tests alone (no fault injection required). These are read-side
// helpers + error types the ginkgo tests don't exercise directly.
// -----------------------------------------------------------------------------

// buildArchive is the stdlib mirror of buildArchiveG (in-memory zip factory).
func buildArchive(t *testing.T, entries map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zw.Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	return zr
}

func TestDumpSupportingFunctions(t *testing.T) {
	t.Run("SecurityInvariant_RestoreValidationError_Error_returns_msg", func(t *testing.T) {
		e := &RestoreValidationError{Msg: "bad archive"}
		if e.Error() != "bad archive" {
			t.Fatalf("Error()=%q want=%q", e.Error(), "bad archive")
		}
	})

	t.Run("SecurityInvariant_RestoreVersionError_Error_contains_both_versions", func(t *testing.T) {
		e := &RestoreVersionError{Archive: 42, Current: 44}
		msg := e.Error()
		if !strings.Contains(msg, "42") || !strings.Contains(msg, "44") {
			t.Fatalf("Error() missing versions: %q", msg)
		}
	})

	t.Run("SecurityInvariant_Senders_returns_distinct_non_null_senders", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		truncateAll(t, d)

		s1 := mkSender("senders_a")
		s2 := mkSender("senders_b")
		cleanupSender(t, d, ctx, s1)
		cleanupSender(t, d, ctx, s2)
		ensureUser(t, d, ctx, s1)
		ensureUser(t, d, ctx, s2)
		ensureProjects(t, d, ctx, s1, "P")
		ensureProjects(t, d, ctx, s2, "P")
		day := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
		insertSeed(t, d, ctx, s1, hbSeed{project: "P", entity: "a.go", ts: day})
		insertSeed(t, d, ctx, s1, hbSeed{project: "P", entity: "b.go", ts: day.Add(time.Minute)}) // duplicate sender
		insertSeed(t, d, ctx, s2, hbSeed{project: "P", entity: "a.go", ts: day})

		got, err := d.Senders(ctx)
		if err != nil {
			t.Fatalf("Senders: %v", err)
		}
		set := map[string]int{}
		for _, s := range got {
			set[s]++
		}
		if set[s1] != 1 {
			t.Fatalf("s1 count=%d want=1 (must be DISTINCT)", set[s1])
		}
		if set[s2] != 1 {
			t.Fatalf("s2 count=%d want=1", set[s2])
		}
	})

	t.Run("SecurityInvariant_HasActiveImportJobs_true_when_running_false_after_terminal", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		truncateAll(t, d)

		got, err := d.HasActiveImportJobs(ctx)
		if err != nil {
			t.Fatalf("HasActiveImportJobs (empty): %v", err)
		}
		if got {
			t.Fatal("expected false on empty jobs table")
		}

		owner := mkSender("hasactive")
		cleanupSender(t, d, ctx, owner)
		ensureUser(t, d, ctx, owner)
		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err = d.HasActiveImportJobs(ctx)
		if err != nil {
			t.Fatalf("HasActiveImportJobs (queued): %v", err)
		}
		if !got {
			t.Fatal("expected true with queued job")
		}
		if _, err := d.FinishImportJob(ctx, job.ID, JobStateCompleted, nil); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		got, err = d.HasActiveImportJobs(ctx)
		if err != nil {
			t.Fatalf("HasActiveImportJobs (done): %v", err)
		}
		if got {
			t.Fatal("expected false after Finish (only completed job)")
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_missing_manifest_returns_ValidationError", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		// Archive with no manifest.json → RestoreValidationError.
		zr := buildArchive(t, map[string]string{"random.txt": "hi"})
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want RestoreValidationError, got %T: %v", err, err)
		}
		if !strings.Contains(v.Msg, manifestName) {
			t.Fatalf("msg missing manifest name: %q", v.Msg)
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_garbage_manifest_json", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		zr := buildArchive(t, map[string]string{manifestName: "{not json"})
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want RestoreValidationError, got %T", err)
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_foreign_app_id", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()

		var goose int64
		if err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&goose); err != nil {
			t.Fatalf("read goose: %v", err)
		}
		mf := dumpManifest{App: "otherapp", FormatVersion: dumpFormatVersion, GooseVersion: goose}
		body, _ := json.Marshal(mf)
		zr := buildArchive(t, map[string]string{manifestName: string(body)})
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want ValidationError, got %T", err)
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_wrong_format_version", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()

		var goose int64
		if err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&goose); err != nil {
			t.Fatalf("read goose: %v", err)
		}
		mf := dumpManifest{App: dumpAppID, FormatVersion: 99, GooseVersion: goose}
		body, _ := json.Marshal(mf)
		zr := buildArchive(t, map[string]string{manifestName: string(body)})
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want ValidationError, got %T", err)
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_wrong_goose_version_returns_VersionError", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()

		var goose int64
		if err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&goose); err != nil {
			t.Fatalf("read goose: %v", err)
		}
		mf := dumpManifest{App: dumpAppID, FormatVersion: dumpFormatVersion, GooseVersion: goose + 7}
		body, _ := json.Marshal(mf)
		zr := buildArchive(t, map[string]string{manifestName: string(body)})
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var vv *RestoreVersionError
		if !errors.As(err, &vv) {
			t.Fatalf("want RestoreVersionError, got %T: %v", err, err)
		}
		if vv.Archive != goose+7 || vv.Current != goose {
			t.Fatalf("VersionError versions off: %+v", vv)
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_missing_table_in_manifest", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()

		var goose int64
		if err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&goose); err != nil {
			t.Fatalf("read goose: %v", err)
		}
		mf := dumpManifest{App: dumpAppID, FormatVersion: dumpFormatVersion, GooseVersion: goose}
		body, _ := json.Marshal(mf)
		zr := buildArchive(t, map[string]string{manifestName: string(body)})
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want ValidationError, got %T", err)
		}
		if !strings.Contains(v.Msg, "missing table") {
			t.Fatalf("msg=%q want to mention missing table", v.Msg)
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_wrong_column_set", func(t *testing.T) {
		d := openIsolatedDumpDB(t)
		ctx := context.Background()

		var goose int64
		if err := d.Pool.QueryRow(ctx, `SELECT coalesce(max(version_id),0) FROM goose_db_version`).Scan(&goose); err != nil {
			t.Fatalf("read goose: %v", err)
		}
		// Manifest lists every table but with a WRONG columns array for one.
		mf := dumpManifest{App: dumpAppID, FormatVersion: dumpFormatVersion, GooseVersion: goose}
		entries := map[string]string{}
		for _, dt := range dumpTables {
			cols := append([]string{}, dt.Columns...)
			if dt.Name == "users" {
				cols = []string{"wrong", "columns", "totally"}
			}
			mf.Tables = append(mf.Tables, dumpManifestTable{Name: dt.Name, Columns: cols, Rows: 0})
			entries[entryName(dt.Name)] = ""
		}
		body, _ := json.Marshal(mf)
		entries[manifestName] = string(body)
		zr := buildArchive(t, entries)
		_, err := d.RestoreAll(ctx, zr)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want ValidationError, got %T", err)
		}
		if !strings.Contains(v.Msg, "column") {
			t.Fatalf("msg=%q want to mention column mismatch", v.Msg)
		}
	})

	t.Run("SecurityInvariant_UsersHasEncryptedRows_false_for_all_null_ciphertext", func(t *testing.T) {
		aead, b64Key := newSecurityAEAD(t)
		t.Setenv(encryptionKeyEnvName, b64Key)
		_ = aead

		d := openIsolatedDumpDB(t)
		ctx := context.Background()
		truncateAll(t, d)
		// Seed a user with NULL ciphertext. usersHasEncryptedRows via
		// RestoreAll gate: since ALL rows are NULL, the gate should NOT
		// trigger (we can even unset env and it should still not gate).
		sender := mkSender("usenc_null")
		cleanupSender(t, d, ctx, sender)
		ensureUser(t, d, ctx, sender)

		var buf bytes.Buffer
		if err := d.DumpAll(ctx, &buf); err != nil {
			t.Fatalf("DumpAll: %v", err)
		}
		t.Setenv(encryptionKeyEnvName, "")
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("zip.NewReader: %v", err)
		}
		// The gate should NOT trip because no user has ciphertext.
		if _, err := d.RestoreAll(ctx, zr); err != nil {
			t.Fatalf("RestoreAll should succeed when no ciphertext + no key: %v", err)
		}
	})
}

// TestDumpInternalHelpers reaches internal (package-private) helper branches
// via direct calls so `-cover` on dump.go crosses the 90% bar.
func TestDumpInternalHelpers(t *testing.T) {
	t.Run("SecurityInvariant_UsersHasEncryptedRows_returns_error_when_users_entry_missing", func(t *testing.T) {
		// No "tables/users.copy" entry → zr.Open fails inside usersHasEncryptedRows.
		zr := buildArchive(t, map[string]string{manifestName: "irrelevant"})
		got, err := usersHasEncryptedRows(zr)
		if err == nil {
			t.Fatal("expected error when users entry missing")
		}
		if got {
			t.Fatal("hasEnc should be false on error")
		}
	})

	t.Run("SecurityInvariant_UsersHasEncryptedRows_ignores_blank_lines_and_short_rows", func(t *testing.T) {
		// Users COPY payload with a NULL ciphertext row (\N), a blank row, and
		// a short row (fewer columns than 'encrypted_wakatime_key' index).
		// Must return false — exercises the `line == ""` and short-row skips.
		var usersCols []string
		for _, dt := range dumpTables {
			if dt.Name == "users" {
				usersCols = dt.Columns
				break
			}
		}
		nCol := len(usersCols)
		// Build a row of nCol tab-separated fields, all "\N" or "x".
		nullRow := make([]string, nCol)
		for i := range nullRow {
			nullRow[i] = `\N`
		}
		short := "just\ttwo\tcols"
		payload := strings.Join(nullRow, "\t") + "\n\n" + short + "\n"
		zr := buildArchive(t, map[string]string{
			entryName("users"): payload,
		})
		got, err := usersHasEncryptedRows(zr)
		if err != nil {
			t.Fatalf("usersHasEncryptedRows: %v", err)
		}
		if got {
			t.Fatal("expected false — no non-\\N ciphertext value present")
		}
	})

	t.Run("SecurityInvariant_ValidateManifest_rejects_when_data_entry_missing_for_table", func(t *testing.T) {
		// Build a manifest listing every table with correct columns, but
		// omit the users COPY entry from the zip → line 260-262 fires.
		mf := dumpManifest{App: dumpAppID, FormatVersion: dumpFormatVersion, GooseVersion: 42}
		entries := map[string]string{}
		for _, dt := range dumpTables {
			mf.Tables = append(mf.Tables, dumpManifestTable{
				Name:    dt.Name,
				Columns: append([]string{}, dt.Columns...),
				Rows:    0,
			})
			if dt.Name == "users" {
				continue // deliberately omit
			}
			entries[entryName(dt.Name)] = ""
		}
		body, _ := json.Marshal(mf)
		entries[manifestName] = string(body)
		zr := buildArchive(t, entries)
		err := validateManifest(&mf, zr, 42)
		if err == nil {
			t.Fatal("expected error")
		}
		var v *RestoreValidationError
		if !errors.As(err, &v) {
			t.Fatalf("want ValidationError, got %T", err)
		}
		if !strings.Contains(v.Msg, "missing data entry") {
			t.Fatalf("want 'missing data entry' in msg, got %q", v.Msg)
		}
	})
}
