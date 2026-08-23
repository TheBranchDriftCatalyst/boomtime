// Package db carries the STANDALONE catalyst-books migration set (boom-zp2s
// books-standalone). It is DELIBERATELY separate from the host's
// internal/shared/db migration FS: the standalone binary (cmd/catalyst-books)
// runs against its own fresh, books-only Postgres database — no wakatime /
// stats / code tables, and no full users-model — so it needs a schema that
// creates ONLY the books data tables (plus a minimal owner-credential row).
//
// The single consolidated forward migration (00001_books_baseline.sql) reflects
// the FINAL shape of every table internal/books reads or writes, distilled from
// the host's incremental migrations 00057–00080. The one structural difference
// from the host: the books data tables carry `owner text NOT NULL` as a PLAIN
// column instead of `owner text NOT NULL REFERENCES public.users(username) ON
// DELETE CASCADE`. Stripping the FK is what lets the books schema stand alone
// without the host's full users table.
//
// This package is imported ONLY by cmd/catalyst-books, which hands MigrationsFS
// to db.MigrateURLFS. internal/shared/db never imports it (it takes an fs.FS),
// so there is no dependency cycle and the host's default MigrateURL is untouched.
package db

import "embed"

//go:embed migrations/*.sql
var migrationFS embed.FS

// MigrationsFS is the embedded books-only goose migration set. It exposes a
// top-level "migrations" directory, matching what db.MigrateURLFS passes to
// goose.UpContext.
var MigrationsFS embed.FS = migrationFS
