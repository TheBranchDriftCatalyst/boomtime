// dump_schema_test.go — the guard rails for boom-qs80.
//
// The bug these pin: dumpTables is a hand-written registry that RestoreAll
// TRUNCATEs with CASCADE. When it drifted behind the schema, a restore silently
// destroyed every FK-linked table missing from it and silently reset every
// column missing from a dumped table's column list (users.argon_version → 1 =
// total password lockout; users.disabled_at → NULL = a killed account comes
// back). None of the pre-existing tests could see either failure, because they
// only ever asserted things ABOUT the registry.
//
// These tests assert against the LIVE POSTGRES CATALOG instead, so a future
// migration cannot reintroduce the drift without a red test.
package db

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// liveTablesAndCols reads the whole public schema shape once.
func liveTablesAndCols(t *testing.T, d *DB) (tables []string, cols map[string][]string) {
	t.Helper()
	ctx := context.Background()
	var err error
	tables, err = schemaBaseTables(ctx, d.Pool)
	if err != nil {
		t.Fatalf("schemaBaseTables: %v", err)
	}
	cols = make(map[string][]string, len(tables))
	for _, tb := range tables {
		c, err := schemaColumns(ctx, d.Pool, tb)
		if err != nil {
			t.Fatalf("schemaColumns(%s): %v", tb, err)
		}
		cols[tb] = c
	}
	return tables, cols
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// diffSets returns (onlyInA, onlyInB).
func diffSets(a, b []string) (onlyA, onlyB []string) {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	inA := make(map[string]bool, len(a))
	for _, s := range a {
		inA[s] = true
	}
	for _, s := range a {
		if !inB[s] {
			onlyA = append(onlyA, s)
		}
	}
	for _, s := range b {
		if !inA[s] {
			onlyB = append(onlyB, s)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}

// TestDumpSchemaCensus is the drift alarm. Every subtest compares dumpTables /
// dumpExemptTables against what Postgres actually has; a migration that adds a
// table or a column and forgets the backup registry turns one of these red with
// a message naming the exact table/column.
func TestDumpSchemaCensus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	liveTables, liveCols := liveTablesAndCols(t, d)

	dumped := map[string][]string{}
	for _, dt := range dumpTables {
		if _, dup := dumped[dt.Name]; dup {
			t.Fatalf("dumpTables lists %q twice — TRUNCATE/COPY would run it twice", dt.Name)
		}
		dumped[dt.Name] = dt.Columns
	}

	t.Run("every_live_table_is_dumped_or_explicitly_exempt", func(t *testing.T) {
		for _, tb := range liveTables {
			_, isDumped := dumped[tb]
			reason, isExempt := dumpExemptTables[tb]
			switch {
			case isDumped && isExempt:
				t.Errorf("table %q is BOTH in dumpTables and dumpExemptTables — pick one", tb)
			case !isDumped && !isExempt:
				t.Errorf("table %q exists in the schema but is neither dumped nor exempted.\n"+
					"  A whole-DB backup that omits it is incomplete, and if it is FK-linked to a "+
					"dumped table, RestoreAll's TRUNCATE ... CASCADE DESTROYS it.\n"+
					"  Fix: add it to dumpTables in internal/shared/db/dump.go (FK-parents first), "+
					"or add it to dumpExemptTables with the reason it holds no restorable state.", tb)
			case isExempt && strings.TrimSpace(reason) == "":
				t.Errorf("table %q is exempt with an empty reason — say why", tb)
			}
		}
	})

	t.Run("every_dumped_table_exists", func(t *testing.T) {
		live := map[string]bool{}
		for _, tb := range liveTables {
			live[tb] = true
		}
		for name := range dumped {
			if !live[name] {
				t.Errorf("dumpTables lists %q but no such table exists — DumpAll would fail at export time", name)
			}
		}
		for name := range dumpExemptTables {
			if !live[name] {
				t.Errorf("dumpExemptTables lists %q but no such table exists — stale exemption, delete it", name)
			}
		}
	})

	t.Run("dumped_column_lists_match_the_live_schema", func(t *testing.T) {
		// This is the argon_version / ai_* / drift class of bug: COPY with an
		// explicit column list silently lands every OMITTED column on its
		// DEFAULT during restore.
		for name, gotCols := range dumped {
			want, ok := liveCols[name]
			if !ok {
				continue // reported by every_dumped_table_exists
			}
			missing, extra := diffSets(want, gotCols)
			if len(missing) > 0 {
				t.Errorf("dumpTables[%s] omits column(s) %v — a restore silently resets them to "+
					"their DEFAULT (e.g. users.argon_version=1 locks every v2 password out). "+
					"Add them to the column list in internal/shared/db/dump.go.", name, missing)
			}
			if len(extra) > 0 {
				t.Errorf("dumpTables[%s] lists column(s) %v that no longer exist — COPY will fail", name, extra)
			}
		}
	})

	t.Run("exempt_tables_are_not_reachable_by_truncate_cascade", func(t *testing.T) {
		// An exemption may only mean "we choose not to back this up". It may
		// NEVER mean "we destroy this on restore and don't back it up".
		children, err := foreignKeyChildren(ctx, d.Pool)
		if err != nil {
			t.Fatalf("foreignKeyChildren: %v", err)
		}
		reached := truncateCascadeClosure(dumpedTableNames(), children)
		for name, reason := range dumpExemptTables {
			if reached[name] {
				t.Errorf("exempt table %q IS reached by TRUNCATE ... CASCADE over dumpTables, so a "+
					"restore empties it and never refills it. The exemption (%q) is not tenable — "+
					"dump the table instead.", name, reason)
			}
		}
	})

	t.Run("restore_cascade_reaches_nothing_the_dump_does_not_carry", func(t *testing.T) {
		extra, err := undumpedCascadeTargets(ctx, d.Pool)
		if err != nil {
			t.Fatalf("undumpedCascadeTargets: %v", err)
		}
		if len(extra) > 0 {
			t.Fatalf("RestoreAll would TRUNCATE ... CASCADE into %v, which the archive does not carry "+
				"— every row in those tables is destroyed by a save→load round trip (boom-qs80)", extra)
		}
	})

	t.Run("dump_order_is_fk_safe_parents_before_children", func(t *testing.T) {
		pos := map[string]int{}
		for i, dt := range dumpTables {
			pos[dt.Name] = i
		}
		children, err := foreignKeyChildren(ctx, d.Pool)
		if err != nil {
			t.Fatalf("foreignKeyChildren: %v", err)
		}
		for parent, kids := range children {
			pp, ok := pos[parent]
			if !ok {
				continue
			}
			for _, kid := range kids {
				kp, ok := pos[kid]
				if !ok {
					continue
				}
				if kp < pp {
					t.Errorf("dumpTables loads child %q (index %d) BEFORE its FK parent %q (index %d) — "+
						"the restore COPY will fail with a foreign-key violation", kid, kp, parent, pp)
				}
			}
		}
	})

	t.Run("every_dumped_serial_id_table_gets_its_sequence_reset", func(t *testing.T) {
		got, err := resolveSerialResetTables(ctx, d.Pool)
		if err != nil {
			t.Fatalf("resolveSerialResetTables: %v", err)
		}
		has := map[string]bool{}
		for _, n := range got {
			has[n] = true
		}
		// Independently recompute from the catalog: a dumped table with an `id`
		// column whose default calls nextval() must be in the reset set, or the
		// first post-restore insert collides with a restored row.
		for name := range dumped {
			var usesNextval bool
			err := d.Pool.QueryRow(ctx, `
				SELECT coalesce(pg_get_expr(ad.adbin, ad.adrelid), '') LIKE 'nextval%'
				  FROM pg_class c
				  JOIN pg_namespace n ON n.oid = c.relnamespace
				  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'id'
				  LEFT JOIN pg_attrdef ad ON ad.adrelid = c.oid AND ad.adnum = a.attnum
				 WHERE n.nspname = 'public' AND c.relname = $1 AND NOT a.attisdropped`, name).Scan(&usesNextval)
			if err != nil {
				continue // no id column
			}
			if usesNextval && !has[name] {
				t.Errorf("dumped table %q has a sequence-backed id but is not in the sequence-reset set "+
					"— the next insert after a restore collides with a restored row", name)
			}
		}
	})
}

// TestRestoreRefusesWhenCascadeWouldReachUndumpedTable is the runtime half of
// the guard, and it is deliberately NOT a test about the registry: it creates a
// REAL table with a REAL foreign key to users, exactly like a future migration
// would, and proves that (a) RestoreAll refuses, (b) it refuses BEFORE writing
// anything, so the rows that would have been destroyed are still there.
//
// Remove the undumpedCascadeTargets() call from RestoreAll and this test fails
// with the probe row gone — which is precisely the boom-qs80 incident.
func TestRestoreRefusesWhenCascadeWouldReachUndumpedTable(t *testing.T) {
	t.Setenv(encryptionKeyEnvName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
	d := openIsolatedDumpDB(t)
	ctx := context.Background()

	const probe = "dump_census_probe"
	drop := func() { _, _ = d.Pool.Exec(ctx, `DROP TABLE IF EXISTS `+probe) }
	drop() // in case a previous crashed run left it behind
	t.Cleanup(drop)

	truncateAll(t, d)
	sender := mkSender("cascade_probe")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	// A brand-new FK-linked table, precisely the shape migrations keep adding.
	if _, err := d.Pool.Exec(ctx, `CREATE TABLE `+probe+` (
			owner TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
			note  TEXT NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO `+probe+` (owner, note) VALUES ($1,'precious')`, sender); err != nil {
		t.Fatalf("seed probe row: %v", err)
	}

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	_, err = d.RestoreAll(ctx, zr)
	if err == nil {
		t.Fatal("RestoreAll accepted an archive whose TRUNCATE ... CASCADE empties an undumped, " +
			"FK-linked table — its rows are destroyed with nothing to restore them from (boom-qs80)")
	}
	var verr *RestoreValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want RestoreValidationError (400, nothing touched), got %T: %v", err, err)
	}
	if !strings.Contains(verr.Msg, probe) {
		t.Fatalf("refusal must name the endangered table %q, got %q", probe, verr.Msg)
	}

	// The whole point: nothing was written.
	if got := tableCount(t, d, probe); got != 1 {
		t.Fatalf("refused restore still destroyed the undumped table's rows: %s has %d rows, want 1", probe, got)
	}
	if got := tableCount(t, d, "users"); got == 0 {
		t.Fatal("refused restore still truncated users")
	}
}

// TestDumpRoundTripPreservesLateAddedColumns pins the columns whose omission was
// silent-but-fatal: users.argon_version (v2 hash verified with v1 params → every
// post-boom-awh.6 user permanently locked out), users.disabled_at (a killed
// account silently comes back), plus role/capabilities/timezone,
// heartbeats.ai_*/workout_*, and import_jobs.drift.
//
// The user row is DELETED between dump and restore so the restore must
// re-INSERT it via COPY — which is what makes an omitted column land on its
// DEFAULT. Asserting after a mere UPDATE would be tautological.
func TestDumpRoundTripPreservesLateAddedColumns(t *testing.T) {
	t.Setenv(encryptionKeyEnvName, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=")
	d := openIsolatedDumpDB(t)
	ctx := context.Background()
	truncateAll(t, d)

	sender := mkSender("latecols")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	disabled := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	if _, err := d.Pool.Exec(ctx, `
		UPDATE users SET argon_version = 2,
		                 timezone      = 'America/Los_Angeles',
		                 role          = 'readonly',
		                 capabilities  = '{"admin":false,"import":true}'::jsonb,
		                 disabled_at   = $2
		 WHERE username = $1`, sender, disabled); err != nil {
		t.Fatalf("seed user columns: %v", err)
	}

	ensureProjects(t, d, ctx, sender, "P")
	if _, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats (sender, project, entity, ty, time_sent,
		                        ai_input_tokens, ai_output_tokens, ai_line_changes,
		                        human_line_changes, ai_prompt_length, ai_session,
		                        ai_subscription_plan, workout_kind, workout_duration_s,
		                        workout_kcal, workout_avg_hr, workout_distance_m)
		VALUES ($1,'P','ai.go','file','2026-04-01T09:00:00',
		        111, 222, 33, 44, 555, 'sess-x', 'max', 'run', 1800, 250.5, 148, 5000.25)`,
		sender); err != nil {
		t.Fatalf("seed ai/workout heartbeat: %v", err)
	}
	var jobID int
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO import_jobs (value, state, owner, drift)
		 VALUES ('{}'::jsonb,'done',$1,'{"missing":7}'::jsonb) RETURNING id`, sender).Scan(&jobID); err != nil {
		t.Fatalf("seed import job: %v", err)
	}

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}

	// Delete everything the user owns so restore must re-INSERT the rows.
	if _, err := d.Pool.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if _, err := d.Pool.Exec(ctx, `TRUNCATE import_jobs CASCADE`); err != nil {
		t.Fatalf("wipe jobs: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if _, err := d.RestoreAll(ctx, zr); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	var (
		argon     int
		tz, role  string
		caps      []byte
		disabled2 *time.Time
	)
	if err := d.Pool.QueryRow(ctx,
		`SELECT argon_version, timezone, role, capabilities, disabled_at FROM users WHERE username=$1`,
		sender).Scan(&argon, &tz, &role, &caps, &disabled2); err != nil {
		t.Fatalf("read restored user: %v", err)
	}
	if argon != 2 {
		t.Fatalf("argon_version=%d after restore, want 2 — a v2 Argon2id hash verified with v1 params "+
			"always fails, so every user is permanently locked out of login (boom-qs80)", argon)
	}
	if disabled2 == nil {
		t.Fatal("disabled_at is NULL after restore — a disabled (kill-switched) account was silently " +
			"RE-ENABLED by the backup restore")
	}
	if !disabled2.UTC().Equal(disabled) {
		t.Fatalf("disabled_at=%v after restore, want %v", disabled2.UTC(), disabled)
	}
	if role != "readonly" {
		t.Fatalf("role=%q after restore, want %q — restore silently reset the user's authz role", role, "readonly")
	}
	if tz != "America/Los_Angeles" {
		t.Fatalf("timezone=%q after restore, want America/Los_Angeles — day bucketing silently moves", tz)
	}
	if !bytes.Contains(caps, []byte(`"import"`)) {
		t.Fatalf("capabilities=%s after restore — per-user capability overrides were dropped", caps)
	}

	var (
		aiIn, aiOut, aiLines, humanLines, aiPrompt, workoutDur, avgHR int
		aiSession, aiPlan, workoutKind                                string
		kcal, distance                                                float64
	)
	if err := d.Pool.QueryRow(ctx, `
		SELECT ai_input_tokens, ai_output_tokens, ai_line_changes, human_line_changes,
		       ai_prompt_length, ai_session, ai_subscription_plan,
		       workout_kind, workout_duration_s, workout_kcal, workout_avg_hr, workout_distance_m
		  FROM heartbeats WHERE sender=$1 AND entity='ai.go'`, sender).Scan(
		&aiIn, &aiOut, &aiLines, &humanLines, &aiPrompt, &aiSession, &aiPlan,
		&workoutKind, &workoutDur, &kcal, &avgHR, &distance); err != nil {
		t.Fatalf("read restored heartbeat (NULLs here mean the columns were dropped): %v", err)
	}
	if aiIn != 111 || aiOut != 222 || aiLines != 33 || humanLines != 44 || aiPrompt != 555 ||
		aiSession != "sess-x" || aiPlan != "max" {
		t.Fatalf("AI-assistance columns did not survive restore: in=%d out=%d lines=%d human=%d prompt=%d session=%q plan=%q",
			aiIn, aiOut, aiLines, humanLines, aiPrompt, aiSession, aiPlan)
	}
	if workoutKind != "run" || workoutDur != 1800 || avgHR != 148 {
		t.Fatalf("workout columns did not survive restore: kind=%q dur=%d hr=%d — rollups recompute to 0",
			workoutKind, workoutDur, avgHR)
	}

	var drift []byte
	if err := d.Pool.QueryRow(ctx, `SELECT drift FROM import_jobs WHERE owner=$1`, sender).Scan(&drift); err != nil {
		t.Fatalf("read restored import job: %v", err)
	}
	if !bytes.Contains(drift, []byte(`"missing"`)) {
		t.Fatalf("import_jobs.drift=%s after restore — the drift report was dropped", drift)
	}
}

// TestRestoreGateCatchesNonWakatimeCiphertext pins the generalized
// restore-without-BOOM_ENCRYPTION_KEY gate: a user who never saved a Wakatime
// key but DID connect Amazon/Hardcover still carries ciphertext, and restoring
// that archive into a process with no key strands those secrets forever. The
// single-column check (encrypted_wakatime_key only) let it through.
func TestRestoreGateCatchesNonWakatimeCiphertext(t *testing.T) {
	aead, b64Key := newSecurityAEAD(t)
	t.Setenv(encryptionKeyEnvName, b64Key)

	d := openIsolatedDumpDB(t)
	ctx := context.Background()
	truncateAll(t, d)

	sender := mkSender("amazon_only")
	cleanupSender(t, d, ctx, sender)
	ensureUser(t, d, ctx, sender)

	// NOTE: encrypted_wakatime_key stays NULL — that is the whole point.
	if _, err := d.Pool.Exec(ctx, `
		UPDATE users SET encrypted_amazon_device = $2, amazon_device_status = 'valid'
		 WHERE username = $1`, sender, aead.Seal(t, []byte("amazon-device-cred"))); err != nil {
		t.Fatalf("seed amazon ciphertext: %v", err)
	}

	var buf bytes.Buffer
	if err := d.DumpAll(ctx, &buf); err != nil {
		t.Fatalf("DumpAll: %v", err)
	}
	archive := buf.Bytes()
	before := snapshotAllTableCounts(t, d)

	t.Setenv(encryptionKeyEnvName, "")
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	_, err = d.RestoreAll(ctx, zr)
	if err == nil {
		t.Fatal("RestoreAll accepted an archive carrying Amazon/Hardcover ciphertext with no " +
			"BOOM_ENCRYPTION_KEY set — those device credentials are now stranded under a key nobody has")
	}
	var verr *RestoreValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want RestoreValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(verr.Msg, "BOOM_ENCRYPTION_KEY") {
		t.Fatalf("gate error should name BOOM_ENCRYPTION_KEY, got %q", verr.Msg)
	}
	for name, want := range before {
		if got := tableCount(t, d, name); got != want {
			t.Fatalf("gated restore mutated %s: before=%d after=%d", name, want, got)
		}
	}

	// Sanity (anti-tautology): with the key set, the same archive restores and
	// the ciphertext decrypts back to the original plaintext.
	t.Setenv(encryptionKeyEnvName, b64Key)
	zr2, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if _, err := d.RestoreAll(ctx, zr2); err != nil {
		t.Fatalf("RestoreAll with key set: %v", err)
	}
	var restored []byte
	if err := d.Pool.QueryRow(ctx,
		`SELECT encrypted_amazon_device FROM users WHERE username=$1`, sender).Scan(&restored); err != nil {
		t.Fatalf("read restored amazon ciphertext: %v", err)
	}
	pt, err := aead.Open(restored)
	if err != nil {
		t.Fatalf("restored Amazon ciphertext no longer decrypts: %v", err)
	}
	if string(pt) != "amazon-device-cred" {
		t.Fatalf("decrypted %q, want %q", pt, "amazon-device-cred")
	}
}

// TestTruncateCascadeClosure is a pure-Go unit test of the closure walk (no DB):
// it must follow FK edges transitively, and must not loop on a cycle.
func TestTruncateCascadeClosure(t *testing.T) {
	children := map[string][]string{
		"users":      {"projects", "reading_items"},
		"projects":   {"heartbeats"},
		"heartbeats": {"workout_details", "users"}, // deliberate cycle back to a seed
	}
	got := truncateCascadeClosure([]string{"users"}, children)
	want := []string{"users", "projects", "reading_items", "heartbeats", "workout_details"}
	if len(got) != len(want) {
		t.Fatalf("closure size %d, want %d (%v)", len(got), len(want), got)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("closure missing transitively-reached table %q", w)
		}
	}
	// A seed set that touches nothing reaches only itself.
	solo := truncateCascadeClosure([]string{"jobs"}, children)
	if len(solo) != 1 || !solo["jobs"] {
		t.Fatalf("closure of an unreferenced table should be itself only, got %v", sortedCopy(keysOf(solo)))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
