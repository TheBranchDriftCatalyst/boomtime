package db

import (
	"context"
	"database/sql"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migrate applies all embedded goose migrations against the pool's database.
// It is idempotent: goose skips already-applied versions.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// goose needs a *sql.DB; open one on the same DSN via the stdlib adapter.
	sqldb := stdlib.OpenDBFromPool(pool)
	defer sqldb.Close()
	return migrateDB(ctx, sqldb)
}

// MigrateURL applies migrations using a plain connection string (used by the
// run-migrations CLI command without needing a pool).
func MigrateURL(ctx context.Context, url string) error {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	return migrateDB(ctx, sqldb)
}

func migrateDB(ctx context.Context, sqldb *sql.DB) error {
	return migrateDBFS(ctx, sqldb, migrationFS)
}

// MigrateURLFS is MigrateURL against a CALLER-SUPPLIED migration FS instead of
// the host's embedded set (gaka-zp2s books-standalone). The STANDALONE
// catalyst-books binary uses it to apply its own books-only schema
// (internal/books/db.MigrationsFS) to a fresh books-only database — the host's
// full migration set (users / wakatime / stats / …) is deliberately NOT applied
// there. The default MigrateURL / Migrate the host boots with are untouched, so
// host behavior is unchanged. fsys must expose a top-level "migrations"
// directory of goose SQL files (an embed.FS of `migrations/*.sql`).
func MigrateURLFS(ctx context.Context, url string, fsys fs.FS) error {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	return migrateDBFS(ctx, sqldb, fsys)
}

func migrateDBFS(ctx context.Context, sqldb *sql.DB, fsys fs.FS) error {
	goose.SetBaseFS(fsys)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, sqldb, "migrations")
}

// SchemaVersion returns the highest applied migration version from goose's
// bookkeeping table. Zero when the table is empty. Used by /healthz.
func (d *DB) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version`,
	).Scan(&v)
	return v, err
}
