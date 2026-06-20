package sqltest_test

import (
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
