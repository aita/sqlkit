package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// A parsed statement reaches a Router as a bare AST (sql.InsertStmt, ...) or,
// for a construct a frontend can't model, a sql.RawQuery. Run through the
// row-returning path (Op == OpQuery, e.g. INSERT ... RETURNING), it must still
// classify by what it is, not fall back to Op and report a read — that would
// route a write to a replica.
func TestParsedStatementKindAndIsWrite(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	cases := []struct {
		name  string
		query sql.Query
		kind  sqlkit.StatementKind
		write bool
	}{
		{"bare-insert", sql.InsertInto(users).Columns("email").Values("a").Returning(sql.Col("id")).Stmt(), sqlkit.KindInsert, true},
		{"bare-update", sql.Update(users).Set(sql.Set("email", "a")).Returning(sql.Col("id")).Stmt(), sqlkit.KindUpdate, true},
		{"bare-delete", sql.DeleteFrom(users).Returning(sql.Col("id")).Stmt(), sqlkit.KindDelete, true},
		{"bare-select", sql.Select(sql.Star()).From(users).Stmt(), sqlkit.KindSelect, false},
		{"raw-write", sql.RawSQL("INSERT INTO users (email) VALUES ('a') RETURNING id"), sqlkit.KindRawUnknown, true},
		{"raw-cte-write", sql.RawSQL("WITH d AS (DELETE FROM users RETURNING id) SELECT * FROM d"), sqlkit.KindRawUnknown, true},
		{"raw-locking-select", sql.RawSQL("SELECT * FROM users FOR UPDATE SKIP LOCKED"), sqlkit.KindRawRead, true},
		{"raw-select", sql.RawSQL("SELECT 1"), sqlkit.KindRawRead, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt := sqlkit.Statement{Op: sqlkit.OpQuery, Query: tc.query}
			if got := stmt.Kind(); got != tc.kind {
				t.Errorf("Kind() = %v, want %v", got, tc.kind)
			}
			if got := stmt.IsWrite(); got != tc.write {
				t.Errorf("IsWrite() = %v, want %v", got, tc.write)
			}
		})
	}
}
