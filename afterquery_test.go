package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// captureResults records, via an AfterQuery hook, every successful result it
// sees as the destination given to Scan.
type captureResults struct {
	stmts []sqlkit.Statement
	dests []any
}

func (c *captureResults) hooks() sqlkit.Hooks {
	return sqlkit.Hooks{
		AfterQuery: func(_ context.Context, stmt sqlkit.Statement, dest any, err error) error {
			if err != nil {
				return err
			}
			c.stmts = append(c.stmts, stmt)
			c.dests = append(c.dests, dest)
			return nil
		},
	}
}

// twoUsersResult is a canned two-row result for the Users fixture.
func twoUsersResult() testutil.Result {
	return testutil.Result{
		Columns: []string{"id", "email", "name"},
		Rows: [][]driver.Value{
			{int64(7), "a@example.com", nil},
			{int64(8), "b@example.com", nil},
		},
	}
}

// TestAfterQuerySeesScannedSlice verifies AfterQuery receives the whole
// materialized slice once, with the statement that produced it.
func TestAfterQuerySeesScannedSlice(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return twoUsersResult(), nil }}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))

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

// TestAfterQuerySingleRow verifies a single-row destination yields one
// observation carrying that struct.
func TestAfterQuerySingleRow(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))

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

// TestAfterQueryInheritedBySession verifies a session runs the database's
// AfterQuery hook over results scanned through its transaction.
func TestAfterQueryInheritedBySession(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))
	session := db.NewSession()
	defer session.Close()

	var users []User
	if err := session.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("session observed %d results, want the database's hook to fire once", len(obs.dests))
	}
}

// TestAfterQueryNotRunForWrites verifies AfterQuery is a read-side hook: a write
// fires AfterExec, never AfterQuery.
func TestAfterQueryNotRunForWrites(t *testing.T) {
	fake := &testutil.Fake{}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))

	if _, err := db.Delete(Users).Where(Users.ID.Eq(7)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(obs.dests) != 0 {
		t.Fatalf("AfterQuery observed %d results for a write, want 0", len(obs.dests))
	}
}
