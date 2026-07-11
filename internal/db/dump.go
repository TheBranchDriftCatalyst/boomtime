package db

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// dump.go implements the whole-database logical backup ("Save DB") and its
// destructive restore ("Load DB"). The dump is a ZIP of per-table Postgres
// COPY text files plus a JSON manifest, produced Go-natively over the raw
// pgx COPY protocol — no pg_dump dependency in the app image, and the COPY
// text format round-trips NULLs, arrays, bytea, and timestamps exactly.

// dumpFormatVersion is bumped whenever the archive layout/manifest changes
// incompatibly. Restore requires an exact match (v1 policy).
const dumpFormatVersion = 1

// dumpAppID identifies our archives so a foreign zip can never be restored.
const dumpAppID = "boomtime"

const manifestName = "manifest.json"

// dumpTable is one table in the backup, with an explicit column list so both
// COPY directions are immune to column-order drift between binaries.
type dumpTable struct {
	Name    string
	Columns []string
}

// dumpTables lists every application table in FK-safe load order (parents
// before children); the dump writes them in the same order. goose_db_version
// is deliberately absent — schema is owned by the running binary's migrations;
// the manifest records the version for compatibility checking instead.
var dumpTables = []dumpTable{
	{"users", []string{"username", "hashed_password", "salt_used"}},
	{"projects", []string{"name", "description", "owner", "dependencies", "repository"}},
	{"auth_tokens", []string{"token", "owner", "token_expiry", "last_usage", "token_name", "token_description"}},
	{"refresh_tokens", []string{"refresh_token", "owner", "token_expiry"}},
	{"heartbeats", []string{
		"id", "editor", "plugin", "platform", "machine", "sender", "user_agent",
		"branch", "category", "cursorpos", "dependencies", "entity", "is_write",
		"language", "lineno", "file_lines", "project", "ty", "time_sent", "gap_seconds",
	}},
	{"badges", []string{"link_id", "username", "project"}},
	{"hb_rollup_daily", []string{"sender", "day", "project", "language", "editor", "platform", "machine", "total_seconds"}},
	{"curation_rules", []string{"id", "sender", "axis", "action", "match_value", "new_value", "created_at", "match_type"}},
	{"spaces", []string{"id", "owner", "name", "position", "created_at"}},
	{"space_rules", []string{"id", "space_id", "axis", "match_value", "match_type"}},
	{"import_jobs", []string{
		"id", "value", "state", "error", "created_at", "updated_at", "owner",
		"start_date", "end_date", "total_days", "processed_days", "imported_count",
		"current_day", "started_at", "finished_at",
	}},
	{"import_job_logs", []string{"id", "job_id", "ts", "level", "message"}},
}

// serialColumns lists every serial/bigserial PK that must have its sequence
// repositioned after a COPY of explicit ids (otherwise the next insert
// collides with a restored row).
var serialColumns = []string{
	"heartbeats", "curation_rules", "spaces", "space_rules", "import_jobs", "import_job_logs",
}

// dumpManifest is the archive's self-description (manifest.json).
type dumpManifest struct {
	App           string              `json:"app"`
	FormatVersion int                 `json:"formatVersion"`
	CreatedAt     time.Time           `json:"createdAt"`
	GooseVersion  int64               `json:"gooseVersion"`
	Tables        []dumpManifestTable `json:"tables"`
}

type dumpManifestTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
}

// RestoreSummary reports what a restore loaded, JSON-shaped for the frontend.
type RestoreSummary struct {
	GooseVersion int64            `json:"gooseVersion"`
	TotalRows    int64            `json:"totalRows"`
	Tables       map[string]int64 `json:"tables"`
}

// RestoreValidationError marks archive/manifest problems detected BEFORE any
// data is touched (bad app id, wrong format, missing entries). The handler
// maps it to a 400.
type RestoreValidationError struct{ Msg string }

func (e *RestoreValidationError) Error() string { return e.Msg }

// RestoreVersionError is a schema-version mismatch between the archive and the
// running database — nothing is touched. The handler maps it to a 409.
type RestoreVersionError struct{ Archive, Current int64 }

func (e *RestoreVersionError) Error() string {
	return fmt.Sprintf("backup schema version %d does not match this server's %d", e.Archive, e.Current)
}

func copyToSQL(t dumpTable) string {
	return fmt.Sprintf("COPY %s (%s) TO STDOUT", t.Name, strings.Join(t.Columns, ", "))
}

func copyFromSQL(t dumpTable) string {
	return fmt.Sprintf("COPY %s (%s) FROM STDIN", t.Name, strings.Join(t.Columns, ", "))
}

func entryName(table string) string { return "tables/" + table + ".copy" }

// DumpAll streams a full logical dump of every application table to w as a ZIP
// archive. The whole dump runs in one REPEATABLE READ read-only transaction on
// a single connection, so every table comes from the same MVCC snapshot. The
// manifest is written last (row counts are only known after each COPY) — entry
// order within the archive is irrelevant to the reader.
func (d *DB) DumpAll(ctx context.Context, w io.Writer) error {
	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return err
	}
	// Read-only tx: rollback is always safe, incl. after a successful read.
	defer func() { _, _ = conn.Exec(ctx, "ROLLBACK") }()

	var goose int64
	if err := conn.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version`).Scan(&goose); err != nil {
		return err
	}

	zw := zip.NewWriter(w)
	manifest := dumpManifest{
		App:           dumpAppID,
		FormatVersion: dumpFormatVersion,
		CreatedAt:     time.Now().UTC(),
		GooseVersion:  goose,
	}

	for _, t := range dumpTables {
		entry, err := zw.Create(entryName(t.Name))
		if err != nil {
			return err
		}
		tag, err := conn.Conn().PgConn().CopyTo(ctx, entry, copyToSQL(t))
		if err != nil {
			return fmt.Errorf("dump %s: %w", t.Name, err)
		}
		manifest.Tables = append(manifest.Tables, dumpManifestTable{
			Name:    t.Name,
			Columns: t.Columns,
			Rows:    tag.RowsAffected(),
		})
	}

	me, err := zw.Create(manifestName)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(me)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return err
	}
	return zw.Close()
}

// readManifest locates and decodes manifest.json from the archive.
func readManifest(zr *zip.Reader) (*dumpManifest, error) {
	f, err := zr.Open(manifestName)
	if err != nil {
		return nil, &RestoreValidationError{Msg: "archive has no " + manifestName + " — not a boomtime backup"}
	}
	defer f.Close()
	var m dumpManifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, &RestoreValidationError{Msg: "unreadable " + manifestName + ": " + err.Error()}
	}
	return &m, nil
}

// validateManifest checks the archive against this binary's expectations,
// returning a RestoreValidationError / RestoreVersionError without touching
// any data. v1 policy: exact table set, exact column lists, exact goose
// version (the manifest records columns so a future version can do tolerant
// column-intersection restores).
func validateManifest(m *dumpManifest, zr *zip.Reader, currentGoose int64) error {
	if m.App != dumpAppID {
		return &RestoreValidationError{Msg: "archive was not produced by boomtime (app=" + m.App + ")"}
	}
	if m.FormatVersion != dumpFormatVersion {
		return &RestoreValidationError{Msg: fmt.Sprintf("unsupported backup format version %d (want %d)", m.FormatVersion, dumpFormatVersion)}
	}
	if m.GooseVersion != currentGoose {
		return &RestoreVersionError{Archive: m.GooseVersion, Current: currentGoose}
	}
	byName := make(map[string]dumpManifestTable, len(m.Tables))
	for _, t := range m.Tables {
		byName[t.Name] = t
	}
	for _, want := range dumpTables {
		got, ok := byName[want.Name]
		if !ok {
			return &RestoreValidationError{Msg: "backup is missing table " + want.Name}
		}
		if strings.Join(got.Columns, ",") != strings.Join(want.Columns, ",") {
			return &RestoreValidationError{Msg: "backup column set for " + want.Name + " does not match this server"}
		}
		if _, err := zr.Open(entryName(want.Name)); err != nil {
			return &RestoreValidationError{Msg: "backup is missing data entry for " + want.Name}
		}
	}
	return nil
}

// RestoreAll replaces the ENTIRE application state with the archive's contents.
// Validation happens up front (nothing is touched on a bad/foreign/mismatched
// archive); the destructive part is one transaction — TRUNCATE everything,
// COPY each table in FK order, reposition serial sequences, normalize
// interrupted import jobs — so any failure rolls back to the pre-restore state.
func (d *DB) RestoreAll(ctx context.Context, zr *zip.Reader) (RestoreSummary, error) {
	summary := RestoreSummary{Tables: make(map[string]int64, len(dumpTables))}

	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return summary, err
	}
	defer conn.Release()

	var currentGoose int64
	if err := conn.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version`).Scan(&currentGoose); err != nil {
		return summary, err
	}

	m, err := readManifest(zr)
	if err != nil {
		return summary, err
	}
	if err := validateManifest(m, zr, currentGoose); err != nil {
		return summary, err
	}
	summary.GooseVersion = m.GooseVersion

	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		return summary, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.Exec(ctx, "ROLLBACK")
		}
	}()

	names := make([]string, len(dumpTables))
	for i, t := range dumpTables {
		names[i] = t.Name
	}
	if _, err := conn.Exec(ctx, "TRUNCATE "+strings.Join(names, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		return summary, err
	}

	for _, t := range dumpTables {
		f, err := zr.Open(entryName(t.Name))
		if err != nil {
			return summary, &RestoreValidationError{Msg: "backup is missing data entry for " + t.Name}
		}
		tag, err := conn.Conn().PgConn().CopyFrom(ctx, f, copyFromSQL(t))
		f.Close()
		if err != nil {
			return summary, fmt.Errorf("restore %s: %w", t.Name, err)
		}
		summary.Tables[t.Name] = tag.RowsAffected()
		summary.TotalRows += tag.RowsAffected()
	}

	// Reposition every serial PK sequence past the restored ids; an untouched
	// (empty-table) sequence yields 1/is_called=false, i.e. the next id is 1.
	for _, table := range serialColumns {
		q := fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%s','id'), coalesce(max(id), 1), max(id) IS NOT NULL) FROM %s`,
			table, table)
		if _, err := conn.Exec(ctx, q); err != nil {
			return summary, fmt.Errorf("restore sequences for %s: %w", table, err)
		}
	}

	// Jobs that were queued/running when the backup was taken can never resume
	// in this process; normalize them exactly like startup recovery does.
	if _, err := conn.Exec(ctx, `
		UPDATE import_jobs
		SET state = 'failed', error = 'interrupted by backup restore',
		    finished_at = now(), current_day = NULL, updated_at = now()
		WHERE state IN ('queued','running')`); err != nil {
		return summary, err
	}

	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return summary, err
	}
	committed = true
	return summary, nil
}

// Senders returns every distinct heartbeat sender (used for the post-restore
// derived-data rebuild).
func (d *DB) Senders(ctx context.Context) ([]string, error) {
	rows, err := d.Pool.Query(ctx, `SELECT DISTINCT sender FROM heartbeats WHERE sender IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// HasActiveImportJobs reports whether ANY owner has a queued/running import job
// (a restore replaces the whole import_jobs table, so every owner counts).
func (d *DB) HasActiveImportJobs(ctx context.Context) (bool, error) {
	var active bool
	err := d.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM import_jobs WHERE state IN ('queued','running'))`,
	).Scan(&active)
	return active, err
}
