package uow_test

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/uow"
)

// --- Get / Find edge cases ---

// TestGetReportsNotFound covers Get's ErrNoRows branch: a missing row yields
// found=false with no error and nothing tracked.
func TestGetReportsNotFound(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{Columns: []string{"id", "email", "name"}}, nil // no rows
	}}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	var u userRow
	found, err := scope.Get(ctx(), newUsers(), &u, int64(404))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Fatalf("Get() found = true, want false")
	}
	// A not-found row is not managed, so flush emits nothing.
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if n := len(fake.Execs()); n != 0 {
		t.Fatalf("execs = %d, want 0", n)
	}
}

// TestGetWrongPrimaryKeyArity covers the arity guard: a single-column key
// rejects two supplied values before touching the database.
func TestGetWrongPrimaryKeyArity(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(1), int64(2)); err == nil {
		t.Fatal("Get() with two values for a one-column key should error")
	}
	if n := len(fake.Queries()); n != 0 {
		t.Fatalf("queries = %d, want 0 (rejected before querying)", n)
	}
}

// TestFindWithPredicatesAppliesWhere covers Find's predicate branch: the WHERE
// is compiled into the load query.
func TestFindWithPredicatesAppliesWhere(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{
			Columns: []string{"id", "email", "name"},
			Rows:    [][]driver.Value{{int64(1), "a@x", "A"}},
		}, nil
	}}
	users := newUsers()
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	var rows []userRow
	if err := scope.Find(ctx(), users, &rows, users.Email.Eq("a@x")); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	q := fake.Queries()[0]
	if !strings.Contains(q.SQL, "WHERE") || len(q.Args) != 1 || q.Args[0] != "a@x" {
		t.Fatalf("Find() query = %q args=%v, want a WHERE on email", q.SQL, q.Args)
	}
}

// TestFindIntoPointerSlice covers Find's pointer-element branch: elements of a
// []*Row are tracked through their own pointers.
func TestFindIntoPointerSlice(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{
			Columns: []string{"id", "email", "name"},
			Rows:    [][]driver.Value{{int64(1), "a@x", "A"}, {int64(2), "b@x", "B"}},
		}, nil
	}}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	var rows []*userRow
	if err := scope.Find(ctx(), newUsers(), &rows); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	rows[0].Name = "Renamed" // mutate through the tracked pointer
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if got := verbs(fake.Execs()); len(got) != 1 || got[0] != "UPDATE" {
		t.Fatalf("verbs = %v, want one UPDATE for the changed row", got)
	}
}

// TestFindIntoNonSliceErrors covers Find's destination guard: a pointer to a
// struct (not a slice) is rejected.
func TestFindIntoNonSliceErrors(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: usersResult(1, "a@x", "A")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	var one userRow
	if err := scope.Find(ctx(), newUsers(), &one); err == nil {
		t.Fatal("Find() into a non-slice destination should error")
	}
}

// TestRollbackOnRootScopeErrors covers the guard that Rollback is only valid on
// a nested scope.
func TestRollbackOnRootScopeErrors(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Rollback(ctx()); err == nil {
		t.Fatal("Rollback() on a root scope should error")
	}
}

// --- change detection: normalize / valueEqual over pointers and time.Time ---

const profilesChecksum = "profiles-checksum"

type profilesTable struct {
	info    sqlkit.TableInfo
	ID      sqlkit.Column[int64]
	Bio     sqlkit.Column[*string]
	Updated sqlkit.Column[time.Time]
}

func newProfiles() profilesTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "profiles", Checksum: profilesChecksum}
	return profilesTable{
		info:    info,
		ID:      sqlkit.NewColumn[int64](info, "id"),
		Bio:     sqlkit.NewColumn[*string](info, "bio"),
		Updated: sqlkit.NewColumn[time.Time](info, "updated"),
	}
}

func (t profilesTable) Info() sqlkit.TableInfo             { return t.info }
func (t profilesTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t profilesTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.Bio, t.Updated}
}
func (t profilesTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t profilesTable) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

type profileRow struct {
	ID      int64
	Bio     *string
	Updated time.Time
}

func strptr(s string) *string { return &s }

// TestAttachNoChangeAcrossPointerAndTime covers normalize (pointer deref) and
// valueEqual (time.Time.Equal): an attached row that is not mutated — including
// its nullable pointer and timestamp fields — emits no UPDATE.
func TestAttachNoChangeAcrossPointerAndTime(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	row := &profileRow{ID: 1, Bio: strptr("hi"), Updated: time.Now()}
	if err := scope.Attach(newProfiles(), row); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if n := len(fake.Execs()); n != 0 {
		t.Fatalf("execs = %d, want 0 (nothing changed): %v", n, fake.Statements())
	}
}

// TestAttachDetectsInPlacePointerMutation covers the snapshot design: mutating
// the pointee in place is still detected (normalize snapshots a copy of the
// pointee), so only the changed column is written.
func TestAttachDetectsInPlacePointerMutation(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	row := &profileRow{ID: 1, Bio: strptr("hi"), Updated: time.Unix(0, 0).UTC()}
	if err := scope.Attach(newProfiles(), row); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	*row.Bio = "bye" // mutate the pointee in place
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 || !strings.HasPrefix(execs[0].SQL, "UPDATE") {
		t.Fatalf("execs = %#v, want one UPDATE", execs)
	}
	// Only bio changed: args are [bio, pk].
	if len(execs[0].Args) != 2 || execs[0].Args[1] != int64(1) {
		t.Fatalf("update args = %v, want [bye, 1]", execs[0].Args)
	}
}
