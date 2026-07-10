package db

import (
	"context"
	"database/sql"

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
	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, sqldb, "migrations")
}
