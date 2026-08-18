// Package domains is the registration seam for boomtime's data-fusion domains
// (catalyst-waka, catalyst-books, catalyst-audiobooks, and future
// catalyst-health / catalyst-media). See docs/design/catalyst-domains-spike.md.
//
// Today it centralizes the ENCRYPTED-COLUMN and BACKUP-COLUMN registries so the
// key-rotation command (cmd/boomtime/rotate.go) and the whole-DB backup
// (internal/db/dump.go) iterate a LIST instead of hardcoding each domain by
// name. Hardcoding is exactly what would silently STRAND a new domain's
// encrypted secret on the next key rotation and DROP its table from backups —
// the incident class internal/auth's encryption docs and gaka-awh warn about.
// A new domain adds one entry here and is picked up by both paths automatically.
//
// This package is intentionally DEPENDENCY-FREE (it imports no other internal/*
// package) so internal/db can read it without an import cycle. Domain LOGIC
// (ingest, Amazon device signing, Hardcover sync) lives in
// internal/domains/<name>; only the cross-cutting column contract lives here.
// The fuller Module contract (routes/jobs/migrations/widgets) is the later
// evolution described in the spike — this is the P0 slice that pays off first.
package domaincols

// EncryptedColumn is a per-row AES-256-GCM-sealed column owned by a domain. The
// rotate-encryption-key command re-encrypts every one of these under the new
// key, in a single transaction.
type EncryptedColumn struct {
	Domain    string // owning domain, for log/report clarity ("waka", "amazon")
	Table     string // e.g. "users"
	Column    string // e.g. "encrypted_wakatime_key"
	KeyColumn string // row identity for reporting/update, e.g. "username"
}

// BackupColumns declares DOMAIN-owned columns a domain contributes to the
// whole-DB export's row for Table (the ciphertext + its status/metadata
// siblings), so a backup->restore round-trip preserves them. Core-owned columns
// stay listed directly in internal/db/dump.go.
type BackupColumns struct {
	Domain  string
	Table   string
	Columns []string
}

// encryptedColumns is THE list the rotation command iterates. Every per-user
// AES-GCM secret in the DB must appear here or it is stranded on key rotation.
//
// NOTE (waka/github): these existed as hardcoded rotation targets before this
// registry; they are listed here now so rotation is fully list-driven and the
// smoke test still covers them. amazon is the first NEW domain to ride the seam.
var encryptedColumns = []EncryptedColumn{
	{Domain: "waka", Table: "users", Column: "encrypted_wakatime_key", KeyColumn: "username"},
	{Domain: "github", Table: "users", Column: "encrypted_github_token", KeyColumn: "username"},
	{Domain: "amazon", Table: "users", Column: "encrypted_amazon_device", KeyColumn: "username"},
	{Domain: "hardcover", Table: "users", Column: "encrypted_hardcover_key", KeyColumn: "username"},
}

// backupColumns are NEW-domain columns appended to the whole-DB export. The
// pre-existing core + waka columns remain enumerated in internal/db/dump.go;
// this registry is how a fresh domain (amazon → catalyst-books/audiobooks) gets
// into the backup without a dump.go edit.
var backupColumns = []BackupColumns{
	{Domain: "amazon", Table: "users", Columns: []string{
		"encrypted_amazon_device", "amazon_device_status", "amazon_device_checked_at",
	}},
	{Domain: "hardcover", Table: "users", Columns: []string{
		"encrypted_hardcover_key", "hardcover_key_status", "hardcover_key_checked_at",
	}},
}

// EncryptedColumns returns a copy of the per-user encrypted columns to rotate.
func EncryptedColumns() []EncryptedColumn {
	return append([]EncryptedColumn(nil), encryptedColumns...)
}

// EncryptedColumnsFor returns the encrypted columns owned by the named domain(s),
// in declaration order — so a domain Module (internal/shared/domain) can surface
// its own slice without duplicating the data. This list stays the single source of
// truth; the Module registry just aggregates per-domain views of it.
func EncryptedColumnsFor(domainNames ...string) []EncryptedColumn {
	want := make(map[string]bool, len(domainNames))
	for _, d := range domainNames {
		want[d] = true
	}
	var out []EncryptedColumn
	for _, c := range encryptedColumns {
		if want[c.Domain] {
			out = append(out, c)
		}
	}
	return out
}

// BackupColumnsFor returns the backup column sets owned by the named domain(s), in
// declaration order — the per-domain view for a Module.
func BackupColumnsFor(domainNames ...string) []BackupColumns {
	want := make(map[string]bool, len(domainNames))
	for _, d := range domainNames {
		want[d] = true
	}
	var out []BackupColumns
	for _, b := range backupColumns {
		if want[b.Domain] {
			out = append(out, b)
		}
	}
	return out
}

// AllBackupColumns returns a copy of the domain-owned column sets to add to the
// backup, for merging in dump.go.
func AllBackupColumns() []BackupColumns {
	return append([]BackupColumns(nil), backupColumns...)
}

// UserBackupColumns is a convenience returning just the domain-owned columns on
// the `users` table (the common case), in registration order.
func UserBackupColumns() []string {
	var out []string
	for _, b := range backupColumns {
		if b.Table == "users" {
			out = append(out, b.Columns...)
		}
	}
	return out
}
