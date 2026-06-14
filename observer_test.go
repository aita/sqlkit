package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// captureObserver records every result it observes, as the destination given to
// Scan.
type captureObserver struct {
	stmts []sqlkit.Statement
	dests []any
}

func (c *captureObserver) ObserveResult(_ context.Context, stmt sqlkit.Statement, dest any) {
	c.stmts = append(c.stmts, stmt)
	c.dests = append(c.dests, dest)
}

// twoUsersResult is a canned two-row result for the Users fixture.
func twoUsersResult() sqltest.Result {
	return sqltest.Result{
		Columns: []string{"id", "email", "name"},
		Rows: [][]driver.Value{
			{int64(7), "a@example.com", nil},
			{int64(8), "b@example.com", nil},
		},
	}
}

// TestResultObserverSeesScannedSlice verifies an observer receives the whole
// materialized slice once, with the statement that produced it.
func TestResultObserverSeesScannedSlice(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) { return twoUsersResult(), nil }}
	obs := &captureObserver{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithResultObserver(obs))

	var users []User
	if err := db.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("observed %d results, want 1", len(obs.dests))
	}
	got, ok := obs.dests[0].(*[]User)
	if !ok || len(*got) != 2 || (*got)[0].ID != 7 || (*got)[1].ID != 8 {
		t.Fatalf("observed dest = %+v, want the two scanned users", obs.dests[0])
	}
	if obs.stmts[0].SQL == "" || obs.stmts[0].Op != sqlkit.OpQuery {
		t.Fatalf("observed statement = %+v, want the SELECT it scanned", obs.stmts[0])
	}
}

// TestResultObserverSingleRow verifies a single-row destination yields one
// observation carrying that struct.
func TestResultObserverSingleRow(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) { return usersResult(), nil }}
	obs := &captureObserver{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithResultObserver(obs))

	var user User
	if err := db.Select().From(Users).Where(Users.ID.Eq(7)).One(context.Background()).Scan(&user); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("observed %d results, want 1", len(obs.dests))
	}
	if got, ok := obs.dests[0].(*User); !ok || got.ID != 7 {
		t.Fatalf("observed dest = %+v, want the one scanned user", obs.dests[0])
	}
}

// TestResultObserverInheritedBySession verifies a session runs the database's
// observers over results scanned through its transaction.
func TestResultObserverInheritedBySession(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) { return usersResult(), nil }}
	obs := &captureObserver{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithResultObserver(obs))
	session := db.NewSession()
	defer session.Close()

	var users []User
	if err := session.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("session observed %d results, want the database's observer to fire once", len(obs.dests))
	}
}

// TestResultObserverNotRunForWrites verifies observers are a read-side hook: a
// write that returns no rows produces no observations.
func TestResultObserverNotRunForWrites(t *testing.T) {
	fake := &sqltest.Fake{}
	obs := &captureObserver{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithResultObserver(obs))

	if _, err := db.Delete(Users).Where(Users.ID.Eq(7)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(obs.dests) != 0 {
		t.Fatalf("observed %d results for a write, want 0", len(obs.dests))
	}
}
