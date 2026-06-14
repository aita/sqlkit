package dbtest_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/dbtest"
	"github.com/aita/sqlkit/internal/sqltest"
)

func TestSnapshotRestore(t *testing.T) {
	fake := &sqltest.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	session := db.NewSession()
	defer session.Close()

	// Seed once, then snapshot.
	dbtest.Insert(t, session, newUsers(), userRow{Email: "alice@example.com"})
	restore := dbtest.Snapshot(t, session)

	// A case mutates and then restores twice.
	dbtest.Insert(t, session, newUsers(), userRow{Email: "bob@example.com"})
	restore(t)
	restore(t)

	stmts := fake.Statements()
	if countContains(stmts, "SAVEPOINT") < 1 {
		t.Fatalf("statements %v, want a SAVEPOINT", stmts)
	}
	// The same restore point is reused: two ROLLBACK TO SAVEPOINT, one per call.
	if got := countContains(stmts, "ROLLBACK TO SAVEPOINT"); got != 2 {
		t.Fatalf("ROLLBACK TO SAVEPOINT count = %d, want 2", got)
	}
	// Both rollbacks name the same savepoint that the snapshot opened.
	name := savepointName(stmts)
	if name == "" {
		t.Fatalf("no SAVEPOINT name found in %v", stmts)
	}
	if got := countContains(stmts, "ROLLBACK TO SAVEPOINT "+name); got != 2 {
		t.Fatalf("rollbacks to %q = %d, want 2", name, got)
	}
}

func TestFixtureLoadSnapshot(t *testing.T) {
	fake := &sqltest.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	session := db.NewSession()
	defer session.Close()

	restore := dbtest.NewFixture("blog").
		Add(newUsers(), userRow{Email: "alice@example.com"}).
		LoadSnapshot(t, session)

	stmts := fake.Statements()
	// The fixture insert must precede the SAVEPOINT it snapshots.
	insertAt, savepointAt := indexContains(stmts, `"users"`), indexContains(stmts, "SAVEPOINT")
	if insertAt < 0 || savepointAt < 0 || insertAt > savepointAt {
		t.Fatalf("statements %v, want users insert before SAVEPOINT", stmts)
	}
	restore(t)
	if countContains(fake.Statements(), "ROLLBACK TO SAVEPOINT") != 1 {
		t.Fatalf("statements %v, want one ROLLBACK TO SAVEPOINT", fake.Statements())
	}
}

func countContains(stmts []string, sub string) int {
	n := 0
	for _, stmt := range stmts {
		if strings.Contains(stmt, sub) {
			n++
		}
	}
	return n
}

func indexContains(stmts []string, sub string) int {
	for i, stmt := range stmts {
		if strings.Contains(stmt, sub) {
			return i
		}
	}
	return -1
}

// savepointName extracts the name from the first "SAVEPOINT <name>" statement
// (excluding ROLLBACK TO / RELEASE forms).
func savepointName(stmts []string) string {
	for _, stmt := range stmts {
		s := strings.TrimSpace(stmt)
		if strings.HasPrefix(s, "SAVEPOINT ") {
			return strings.TrimSpace(strings.TrimPrefix(s, "SAVEPOINT "))
		}
	}
	return ""
}
