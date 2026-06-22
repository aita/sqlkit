package sqltest_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sqltest"
)

// TestTxRollsBack runs an inner subtest that seeds through a Tx session, then
// checks that when the subtest returned its transaction was begun and rolled
// back, never committed — the isolation a parallel case relies on.
func TestTxRollsBack(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	tdb := sqltest.New(db, schemaModel(t))
	tdb.ApplySchema(t) // applies schema via auto-commit, no Begin

	t.Run("inner", func(t *testing.T) {
		s := tdb.Tx(t)
		sqltest.InsertRows(t, s, newUsers(), userRow{Email: "alice@example.com"})
	})

	// The session's first statement begins the transaction; Tx's cleanup rolls
	// it back. A committed test transaction would leak writes to a parallel case.
	if got := fake.Begins(); got != 1 {
		t.Fatalf("transactions begun = %d, want 1", got)
	}
	if got := fake.Commits(); got != 0 {
		t.Fatalf("transactions committed = %d, want 0 (Tx must roll back)", got)
	}
}

// TestTxEmptyIsCheap confirms a Tx whose case ran no statement neither begins
// nor commits a transaction: the rollback on cleanup is a no-op, not an error.
func TestTxEmptyIsCheap(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	tdb := sqltest.New(db, schemaModel(t))

	t.Run("inner", func(t *testing.T) {
		_ = tdb.Tx(t)
	})

	if got := fake.Begins(); got != 0 {
		t.Fatalf("transactions begun = %d, want 0 for an unused session", got)
	}
}

// TestTruncateFKSafe checks that DB.Truncate clears the named tables in one
// PostgreSQL TRUNCATE ... CASCADE, so foreign keys never block the reset
// regardless of the order the tables are passed.
func TestTruncateFKSafe(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	tdb := sqltest.New(db, schemaModel(t))

	before := len(fake.Statements())
	tdb.Truncate(t, newPosts(), newUsers())

	truncates := fake.Statements()[before:]
	if len(truncates) != 1 {
		t.Fatalf("truncate statements = %v, want a single CASCADE statement", truncates)
	}
	stmt := truncates[0]
	if !strings.HasPrefix(stmt, "TRUNCATE TABLE ") || !strings.HasSuffix(stmt, " CASCADE") {
		t.Fatalf("truncate statement = %q, want TRUNCATE TABLE ... CASCADE", stmt)
	}
	if !strings.Contains(stmt, `"posts"`) || !strings.Contains(stmt, `"users"`) {
		t.Fatalf("truncate statement = %q, want both tables", stmt)
	}
}

// TestTruncateAllTables clears every table in the schema when none are named,
// covering truncateNames' no-argument branch.
func TestTruncateAllTables(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	tdb := sqltest.New(db, schemaModel(t))

	before := len(fake.Statements())
	tdb.Truncate(t) // no tables: clears the whole schema

	truncates := fake.Statements()[before:]
	if len(truncates) != 1 {
		t.Fatalf("truncate statements = %v, want one CASCADE statement", truncates)
	}
	if !strings.Contains(truncates[0], `"users"`) || !strings.Contains(truncates[0], `"posts"`) {
		t.Fatalf("truncate-all = %q, want both schema tables", truncates[0])
	}
}

// TestTruncateMySQL covers the MySQL branch: foreign-key checks are toggled off
// and each table truncated individually, never a single CASCADE.
func TestTruncateMySQL(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.MySQLDialect{})
	tdb := sqltest.New(db, schemaModel(t))

	before := len(fake.Statements())
	tdb.Truncate(t, newUsers(), newPosts())

	stmts := fake.Statements()[before:]
	joined := strings.Join(stmts, "\n")
	if countContains(stmts, "SET FOREIGN_KEY_CHECKS = 0") != 1 {
		t.Fatalf("statements %v, want FK checks disabled once", stmts)
	}
	if countContains(stmts, "SET FOREIGN_KEY_CHECKS = 1") != 1 {
		t.Fatalf("statements %v, want FK checks re-enabled", stmts)
	}
	if countContains(stmts, "TRUNCATE TABLE") != 2 {
		t.Fatalf("statements %v, want two individual TRUNCATE statements", stmts)
	}
	if strings.Contains(joined, "CASCADE") {
		t.Fatalf("MySQL truncate must not use CASCADE: %s", joined)
	}
}

// TestDropTablesMySQL covers the MySQL drop branch (FK checks toggled, no
// CASCADE) through ApplySchema, which drops before creating.
func TestDropTablesMySQL(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.MySQLDialect{})
	sqltest.New(db, schemaModel(t)).ApplySchema(t)

	joined := strings.Join(fake.Statements(), "\n")
	if !strings.Contains(joined, "SET FOREIGN_KEY_CHECKS = 0") {
		t.Fatalf("MySQL drop must disable FK checks:\n%s", joined)
	}
	if strings.Contains(joined, "DROP TABLE IF EXISTS") && strings.Contains(joined, "DROP TABLE IF EXISTS `posts` CASCADE") {
		t.Fatalf("MySQL drop must not use CASCADE:\n%s", joined)
	}
}

// TestDBSeedingWrappers covers the DB seeding sugar that delegates to the
// package-level helpers: Database, InsertRows, InsertID, and Load.
func TestDBSeedingWrappers(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(11)}}}, nil
		},
	}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	tdb := sqltest.New(db, schemaModel(t))

	if tdb.Database() != db {
		t.Fatalf("Database() returned a different handle")
	}

	tdb.InsertRows(t, newUsers(), userRow{Email: "a@example.com"})
	if id := tdb.InsertID(t, newUsers(), userRow{Email: "b@example.com"}); id != 11 {
		t.Fatalf("InsertID = %d, want 11", id)
	}
	tdb.Load(t, sqltest.NewFixture("seed").Add(newPosts(), postRow{UserID: 11, Title: "Hi"}))

	joined := strings.Join(fake.Statements(), "\n")
	if !strings.Contains(joined, `"users"`) || !strings.Contains(joined, `"posts"`) {
		t.Fatalf("seeding statements missing a table:\n%s", joined)
	}
}
