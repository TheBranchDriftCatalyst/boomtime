package db

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

// dump_schema.go — live-catalog introspection behind the whole-DB backup
// (boom-qs80).
//
// dump.go's dumpTables is a hand-written registry: it says WHAT the archive
// carries and in WHICH order. That registry froze at the pre-books core schema
// while ~50 migrations added user-owned tables and columns, and because
// RestoreAll runs `TRUNCATE <registry> RESTART IDENTITY CASCADE`, every
// FK-linked table missing from it was silently emptied by a restore and never
// repopulated.
//
// The functions here close that loop by asking Postgres what actually exists
// instead of trusting the registry:
//
//   - schemaBaseTables / schemaColumns  → what the census test compares against
//   - foreignKeyChildren                → the parent → children edges CASCADE walks
//   - truncateCascadeClosure            → exactly what `TRUNCATE ... CASCADE` empties
//   - undumpedCascadeTargets            → the restore-time refusal set
//   - resolveSerialResetTables          → which dumped tables need setval() after COPY
//
// Everything is read-only catalog access; none of it writes.

// schemaQuerier is the read seam these helpers need. Both *pgxpool.Pool and
// *pgxpool.Conn satisfy it, so they work inside or outside a transaction.
type schemaQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// scanStrings runs q and collects a single text column.
func scanStrings(ctx context.Context, db schemaQuerier, sql string, args ...any) ([]string, error) {
	rows, err := db.Query(ctx, sql, args...)
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

// schemaBaseTables lists every ordinary (relkind='r') table in the public
// schema, alphabetically. Views, matviews, sequences and partitions are
// excluded: TRUNCATE CASCADE does not reach them and they carry no rows of
// their own.
func schemaBaseTables(ctx context.Context, db schemaQuerier) ([]string, error) {
	return scanStrings(ctx, db, `
		SELECT c.relname
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind = 'r'
		 ORDER BY c.relname`)
}

// schemaColumns returns table's live columns in physical (attnum) order —
// exactly the order a bare `COPY t TO STDOUT` would emit, and the order the
// census test compares dumpTables against.
func schemaColumns(ctx context.Context, db schemaQuerier, table string) ([]string, error) {
	return scanStrings(ctx, db, `
		SELECT a.attname
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  JOIN pg_attribute a ON a.attrelid = c.oid
		 WHERE n.nspname = 'public'
		   AND c.relkind = 'r'
		   AND c.relname = $1
		   AND a.attnum > 0
		   AND NOT a.attisdropped
		 ORDER BY a.attnum`, table)
}

// foreignKeyChildren maps parent table → the tables that REFERENCE it. This is
// the edge direction `TRUNCATE ... CASCADE` walks: truncating a parent empties
// every referencing child, transitively.
func foreignKeyChildren(ctx context.Context, db schemaQuerier) (map[string][]string, error) {
	rows, err := db.Query(ctx, `
		SELECT child.relname, parent.relname
		  FROM pg_constraint con
		  JOIN pg_class child     ON child.oid  = con.conrelid
		  JOIN pg_class parent    ON parent.oid = con.confrelid
		  JOIN pg_namespace cn    ON cn.oid = child.relnamespace
		  JOIN pg_namespace pn    ON pn.oid = parent.relnamespace
		 WHERE con.contype = 'f'
		   AND cn.nspname = 'public'
		   AND pn.nspname = 'public'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if child == parent {
			continue // self-reference: already in the seed set
		}
		out[parent] = append(out[parent], child)
	}
	return out, rows.Err()
}

// truncateCascadeClosure returns the full set of tables Postgres empties for
// `TRUNCATE <seeds> ... CASCADE`: the seeds plus every transitively referencing
// table.
func truncateCascadeClosure(seeds []string, children map[string][]string) map[string]bool {
	reached := make(map[string]bool, len(seeds))
	stack := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if !reached[s] {
			reached[s] = true
			stack = append(stack, s)
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, kid := range children[cur] {
			if !reached[kid] {
				reached[kid] = true
				stack = append(stack, kid)
			}
		}
	}
	return reached
}

// dumpedTableNames returns the dumpTables names in load order.
func dumpedTableNames() []string {
	out := make([]string, len(dumpTables))
	for i, t := range dumpTables {
		out[i] = t.Name
	}
	return out
}

// undumpedCascadeTargets returns, sorted, every table that a restore's
// `TRUNCATE <dumpTables> RESTART IDENTITY CASCADE` would empty even though the
// archive carries no data for it — i.e. rows that would be destroyed with no way
// to put them back. RestoreAll refuses when this is non-empty, BEFORE any write.
//
// Empty is the healthy state and the census test pins it; a non-empty result
// means a migration added an FK-linked table without adding it to dumpTables.
func undumpedCascadeTargets(ctx context.Context, db schemaQuerier) ([]string, error) {
	children, err := foreignKeyChildren(ctx, db)
	if err != nil {
		return nil, err
	}
	dumped := dumpedTableNames()
	inDump := make(map[string]bool, len(dumped))
	for _, n := range dumped {
		inDump[n] = true
	}
	var extra []string
	for name := range truncateCascadeClosure(dumped, children) {
		if !inDump[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return extra, nil
}

// resolveSerialResetTables returns the dumped tables whose `id` column is backed
// by a sequence, in dump order. After a COPY of explicit ids the sequence still
// points at its pre-restore value, so the next INSERT would collide with a
// restored row; RestoreAll setval()s each of these.
//
// Derived rather than listed: the old hand-written list covered 6 tables while
// the schema had grown to a dozen serial-PK tables (boom-qs80).
func resolveSerialResetTables(ctx context.Context, db schemaQuerier) ([]string, error) {
	// pg_depend rather than pg_get_serial_sequence(): that function ERRORs on a
	// table with no `id` column, and the planner is free to push the call down
	// below the pg_attribute join, so the "only tables with an id column" filter
	// cannot be relied on to shield it. Walking the dependency edge from the
	// sequence to its owning column is exact and total. deptype 'a' covers
	// serial/bigserial, 'i' covers GENERATED ... AS IDENTITY.
	withSeq, err := scanStrings(ctx, db, `
		SELECT DISTINCT c.relname
		  FROM pg_class s
		  JOIN pg_depend d
		    ON d.objid = s.oid
		   AND d.classid = 'pg_class'::regclass
		   AND d.refclassid = 'pg_class'::regclass
		   AND d.deptype IN ('a', 'i')
		  JOIN pg_class c      ON c.oid = d.refobjid
		  JOIN pg_namespace n  ON n.oid = c.relnamespace
		  JOIN pg_attribute a  ON a.attrelid = c.oid AND a.attnum = d.refobjsubid
		 WHERE s.relkind = 'S'
		   AND c.relkind = 'r'
		   AND n.nspname = 'public'
		   AND a.attname = 'id'
		   AND NOT a.attisdropped`)
	if err != nil {
		return nil, err
	}
	has := make(map[string]bool, len(withSeq))
	for _, n := range withSeq {
		has[n] = true
	}
	var out []string
	for _, t := range dumpTables {
		if has[t.Name] {
			out = append(out, t.Name)
		}
	}
	return out, nil
}
