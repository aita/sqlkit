package sqltest

import (
	"context"
	stdsql "database/sql"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
)

// Errors the table-name seeding helpers report when the row maps are malformed.
var (
	errEmptyRow       = errors.New("sqltest: row has no columns")
	errColumnMismatch = errors.New("sqltest: rows must all carry the same columns")
)

// Execer is the write surface the table-name seeding helpers need, satisfied by
// *sql.DB, *sql.Tx, and *sql.Conn. It lets InsertMap seed through a raw
// database/sql handle — including a *sql.Tx a test rolls back for isolation —
// without the sqlkit query builder.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (stdsql.Result, error)
}

// ExecQueryer adds the read surface InsertMapID needs to read a generated key
// back with RETURNING. *sql.DB and *sql.Tx satisfy it.
type ExecQueryer interface {
	Execer
	QueryContext(ctx context.Context, query string, args ...any) (*stdsql.Rows, error)
}

// InsertMap seeds rows into table through ex, addressing columns by name with no
// sqlkit.Table binding or row struct — the catalog-free counterpart of
// InsertRows. Each row is a column→value map; every row must carry the same set
// of columns. dialect selects identifier quoting and the bind-placeholder style
// ($1 on PostgreSQL, ? on MySQL), so the same call works on either engine.
//
// ex is any Execer (*sql.DB for auto-commit, or a *sql.Tx the case rolls back
// for isolation). All rows insert in one multi-row statement; an empty rows list
// is a no-op. Foreign-key ordering across tables is the caller's responsibility,
// the same as InsertRows.
func InsertMap(t *testing.T, ex Execer, dialect sqlkit.Dialect, table string, rows ...map[string]any) {
	t.Helper()
	if len(rows) == 0 {
		return
	}
	cols, err := columnsOf(rows)
	if err != nil {
		t.Fatalf("sqltest: insert into %s: %v", table, err)
	}
	query := buildInsert(dialect, table, cols, len(rows))
	args := flattenArgs(cols, rows)
	if _, err := ex.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("sqltest: insert into %s: %v", table, err)
	}
}

// InsertMapID inserts a single row addressed by column name and returns the
// value the database generated for idColumn, smoothing over the same
// cross-dialect wrinkle as InsertID: it reads the key back with RETURNING on
// PostgreSQL and falls back to Result.LastInsertId on MySQL. It is the
// catalog-free counterpart of InsertID for a single auto-generated integer key.
func InsertMapID(t *testing.T, ex ExecQueryer, dialect sqlkit.Dialect, table, idColumn string, row map[string]any) int64 {
	t.Helper()
	cols, err := columnsOf([]map[string]any{row})
	if err != nil {
		t.Fatalf("sqltest: insert into %s: %v", table, err)
	}
	query := buildInsert(dialect, table, cols, 1)
	args := flattenArgs(cols, []map[string]any{row})
	ctx := context.Background()
	if dialect.SupportsReturning() {
		query += " RETURNING " + dialect.QuoteName(idColumn)
		rows, err := ex.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("sqltest: insert into %s: %v", table, err)
		}
		defer rows.Close()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				t.Fatalf("sqltest: insert into %s: %v", table, err)
			}
			t.Fatalf("sqltest: insert into %s returned no key", table)
		}
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("sqltest: insert into %s: scan key: %v", table, err)
		}
		return id
	}
	res, err := ex.ExecContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("sqltest: insert into %s: %v", table, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("sqltest: last insert id for %s: %v", table, err)
	}
	return id
}

// InsertMap seeds rows into table through the DB's auto-commit handle, addressing
// columns by name. It is shorthand for InsertMap(t, d.Database().SQL(),
// d.Database().Dialect(), table, rows...); seed into a *sql.Tx instead when cases
// run in parallel, so the rows roll back with the case.
func (d *DB) InsertMap(t *testing.T, table string, rows ...map[string]any) {
	t.Helper()
	InsertMap(t, d.db.SQL(), d.db.Dialect(), table, rows...)
}

// InsertMapID inserts a single row through the DB's auto-commit handle and
// returns its database-generated key. It is shorthand for InsertMapID(t,
// d.Database().SQL(), d.Database().Dialect(), table, idColumn, row).
func (d *DB) InsertMapID(t *testing.T, table, idColumn string, row map[string]any) int64 {
	t.Helper()
	return InsertMapID(t, d.db.SQL(), d.db.Dialect(), table, idColumn, row)
}

// columnsOf returns the sorted column names shared by every row, failing if a
// row is empty or the rows disagree on their columns. Sorting makes the rendered
// SQL deterministic regardless of map iteration order.
func columnsOf(rows []map[string]any) ([]string, error) {
	first := rows[0]
	if len(first) == 0 {
		return nil, errEmptyRow
	}
	cols := make([]string, 0, len(first))
	for c := range first {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, row := range rows[1:] {
		if len(row) != len(cols) {
			return nil, errColumnMismatch
		}
		for _, c := range cols {
			if _, ok := row[c]; !ok {
				return nil, errColumnMismatch
			}
		}
	}
	return cols, nil
}

// flattenArgs lays each row's values out in column order, row after row, to match
// the placeholders buildInsert emits.
func flattenArgs(cols []string, rows []map[string]any) []any {
	args := make([]any, 0, len(cols)*len(rows))
	for _, row := range rows {
		for _, c := range cols {
			args = append(args, row[c])
		}
	}
	return args
}

// buildInsert renders a multi-row INSERT addressing cols on table, with
// rowCount parenthesised value groups of dialect-styled placeholders.
func buildInsert(dialect sqlkit.Dialect, table string, cols []string, rowCount int) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(dialect.QuoteIdent(table))
	b.WriteString(" (")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(dialect.QuoteName(c))
	}
	b.WriteString(") VALUES ")
	pos := 1
	for r := 0; r < rowCount; r++ {
		if r > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for i := range cols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(dialect.Placeholder(pos))
			pos++
		}
		b.WriteByte(')')
	}
	return b.String()
}
