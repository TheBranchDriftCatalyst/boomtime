// encrypted_columns.go: GENERIC per-user encrypted-column read + rotate, so the
// rotate-encryption-key command iterates the internal/domains registry instead
// of hardcoding wakatime/github/amazon by name (the "stranded secret on
// rotation" incident class). Table/column/key identifiers come from the
// registry (compile-time constants), never user input, and are quoted
// defensively with pgx.Identifier.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EncryptedColumnRow is one row's key + ciphertext for generic rotation.
type EncryptedColumnRow struct {
	Key        string // the KeyColumn value (e.g. username)
	Ciphertext []byte
}

// ListEncryptedColumn returns (keyColumn, column) for every row where the
// encrypted column is non-NULL.
func (d *DB) ListEncryptedColumn(ctx context.Context, table, column, keyColumn string) ([]EncryptedColumnRow, error) {
	q := fmt.Sprintf(
		`SELECT %s, %s FROM %s WHERE %s IS NOT NULL`,
		pgx.Identifier{keyColumn}.Sanitize(),
		pgx.Identifier{column}.Sanitize(),
		pgx.Identifier{table}.Sanitize(),
		pgx.Identifier{column}.Sanitize(),
	)
	rows, err := d.Pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EncryptedColumnRow
	for rows.Next() {
		var r EncryptedColumnRow
		if err := rows.Scan(&r.Key, &r.Ciphertext); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EncryptedColumnUpdate is a batch of re-encrypted rows for one column.
type EncryptedColumnUpdate struct {
	Table     string
	Column    string
	KeyColumn string
	Rows      []EncryptedColumnRow // Ciphertext already re-encrypted under the new key
}

// RotateEncryptedColumns writes every re-encrypted ciphertext across ALL given
// columns in a SINGLE transaction — either every column's rows are rewritten
// under the new key or none are. Returns per-column update counts keyed by
// "table.column".
func (d *DB) RotateEncryptedColumns(ctx context.Context, updates []EncryptedColumnUpdate) (map[string]int, error) {
	counts := make(map[string]int, len(updates))
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	for _, u := range updates {
		stmt := fmt.Sprintf(
			`UPDATE %s SET %s = $1 WHERE %s = $2`,
			pgx.Identifier{u.Table}.Sanitize(),
			pgx.Identifier{u.Column}.Sanitize(),
			pgx.Identifier{u.KeyColumn}.Sanitize(),
		)
		n := 0
		for _, r := range u.Rows {
			tag, err := tx.Exec(ctx, stmt, r.Ciphertext, r.Key)
			if err != nil {
				return nil, fmt.Errorf("rotate %s.%s for key %q: %w", u.Table, u.Column, r.Key, err)
			}
			n += int(tag.RowsAffected())
		}
		counts[u.Table+"."+u.Column] = n
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return counts, nil
}
