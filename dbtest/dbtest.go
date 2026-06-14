package dbtest

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
)

// Insert inserts rows into table and fails the test on error. Each row may be a
// struct, a pointer to a struct, or a slice of either — the same shapes
// InsertQuery.Values accepts — so generated row structs and caller-defined DTOs
// both work. It does not read anything back; use InsertID when you need a
// generated key, or InsertReturning to scan the inserted rows.
//
// ex is any sqlkit.Querier, satisfied by both *sqlkit.Database (auto-commit)
// and *sqlkit.Session (inside a transaction).
func Insert(t *testing.T, ex sqlkit.Querier, table sqlkit.Table, rows ...any) {
	t.Helper()
	if len(rows) == 0 {
		return
	}
	if _, err := ex.Insert(table).Values(rows...).Exec(context.Background()); err != nil {
		t.Fatalf("dbtest: insert into %s: %v", table.Info().QualifiedName(), err)
	}
}

// InsertID inserts a single row and returns its database-generated primary key,
// smoothing over the dialect difference: on dialects with RETURNING
// (PostgreSQL) it reads the key back with RETURNING <pk>; otherwise (MySQL) it
// falls back to Result.LastInsertId. It assumes a single auto-generated
// integer primary-key column, which is the common test fixture; tables with a
// composite or non-integer key are not supported.
func InsertID(t *testing.T, ex sqlkit.Querier, table sqlkit.Table, row any) int64 {
	t.Helper()
	ctx := context.Background()
	pk := table.PrimaryKey()
	if len(pk) != 1 {
		t.Fatalf("dbtest: InsertID requires a single-column primary key on %s, found %d", table.Info().QualifiedName(), len(pk))
	}
	if ex.Dialect().SupportsReturning() {
		var ids []int64
		err := ex.Insert(table).Values(row).Returning(pk[0]).All(ctx).Scan(&ids)
		if err != nil {
			t.Fatalf("dbtest: insert into %s: %v", table.Info().QualifiedName(), err)
		}
		if len(ids) != 1 {
			t.Fatalf("dbtest: insert into %s returned %d keys, want 1", table.Info().QualifiedName(), len(ids))
		}
		return ids[0]
	}
	res, err := ex.Insert(table).Values(row).Exec(ctx)
	if err != nil {
		t.Fatalf("dbtest: insert into %s: %v", table.Info().QualifiedName(), err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("dbtest: last insert id for %s: %v", table.Info().QualifiedName(), err)
	}
	return id
}

// InsertReturning inserts rows and scans the inserted rows — with
// database-generated columns filled in — back into a slice of Row. It relies on
// RETURNING, so it is PostgreSQL-only; on a dialect without RETURNING (MySQL) it
// skips the test rather than silently returning unpopulated rows. Use Insert or
// InsertID on those dialects.
func InsertReturning[Row any](t *testing.T, ex sqlkit.Querier, table sqlkit.Table, rows ...Row) []Row {
	t.Helper()
	if !ex.Dialect().SupportsReturning() {
		t.Skipf("dbtest: InsertReturning needs RETURNING, unsupported by %T", ex.Dialect())
	}
	if len(rows) == 0 {
		return nil
	}
	values := make([]any, len(rows))
	for i := range rows {
		values[i] = rows[i]
	}
	var out []Row
	if err := ex.Insert(table).Values(values...).Returning().All(context.Background()).Scan(&out); err != nil {
		t.Fatalf("dbtest: insert into %s: %v", table.Info().QualifiedName(), err)
	}
	return out
}

// compile-time assertions that both concrete handles satisfy Executor,
// including its Dialect method, so the helpers accept either one.
var (
	_ sqlkit.Querier = (*sqlkit.Database)(nil)
	_ sqlkit.Querier = (*sqlkit.Session)(nil)
)
