package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// OnConn binds a Database to one pinned connection: statements and session
// transactions run on it, while the dialect and hooks carry over.
func TestDatabaseOnConn(t *testing.T) {
	fake := &testutil.Fake{}
	pool := fake.DB()
	pool.SetMaxOpenConns(1)
	var seen int
	db := sqlkit.WrapDB(pool, sqlkit.WithDialect(sqlkit.MySQLDialect{}), sqlkit.WithHooks(sqlkit.Hooks{
		AfterExec: func(context.Context, sqlkit.Statement, stdsql.Result, error) error { seen++; return nil },
	}))
	ctx := context.Background()

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	bound := db.OnConn(conn)

	if bound.SQL() != nil {
		t.Error("OnConn copy should report no pool from SQL()")
	}
	if bound.Dialect().Name() != "mysql" {
		t.Errorf("dialect = %s, want mysql carried over", bound.Dialect().Name())
	}
	// With the pool capped at one connection and that connection pinned, any
	// statement not routed through conn would block forever; these complete.
	if _, err := bound.Exec(ctx, sql.RawSQL("DELETE FROM t")); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := bound.WithSession(ctx, func(s *sqlkit.Session) error {
		_, err := s.Exec(ctx, sql.RawSQL("DELETE FROM u"))
		return err
	}); err != nil {
		t.Fatalf("WithSession: %v", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 1 {
		t.Errorf("begins/commits = %d/%d, want 1/1 (sessions stay transactional)", fake.Begins(), fake.Commits())
	}
	if seen != 2 {
		t.Errorf("AfterExec hook ran %d times, want 2 (hooks carry over)", seen)
	}
}
