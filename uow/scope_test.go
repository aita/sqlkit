package uow_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/uow"
)

// --- hand-written bindings mirroring generated code ---

const usersChecksum = "users-checksum"

type usersTable struct {
	info  sqlkit.TableInfo
	ID    sqlkit.Column[int64]
	Email sqlkit.Column[string]
	Name  sqlkit.Column[string]
}

func newUsers() usersTable { return newUsersFull(usersChecksum, "") }

func newUsersFull(checksum, generator string) usersTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "users", Checksum: checksum, Generator: generator}
	return usersTable{
		info:  info,
		ID:    sqlkit.NewColumn[int64](info, "id"),
		Email: sqlkit.NewColumn[string](info, "email"),
		Name:  sqlkit.NewColumn[string](info, "name"),
	}
}

func (t usersTable) Info() sqlkit.TableInfo             { return t.info }
func (t usersTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t usersTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Email, t.Name} }
func (t usersTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t usersTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }

type userRow struct {
	ID    int64
	Email string
	Name  string
}

// usersMeta is the catalog table whose Key().Checksum a valid users binding
// carries.
func usersMeta() meta.Table {
	return meta.Table{
		Schema: meta.Schema{Name: "public"},
		Name:   "users",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0)},
			{Name: "name", Type: meta.Of("postgres", "text", 0, 0, 0)},
		},
	}
}

// --- helpers ---

func ctx() context.Context { return context.Background() }

// usersResult scripts a one-row users result for QueryFunc.
func usersResult(id int64, email, name string) func(string, []any) (sqltest.Result, error) {
	return func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{
			Columns: []string{"id", "email", "name"},
			Rows:    [][]driver.Value{{id, email, name}},
		}, nil
	}
}

func verbs(calls []sqltest.Call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = strings.Fields(c.SQL)[0]
	}
	return out
}

// --- staging: Add / Update / Remove ---

func TestAddValueBatchesIntoOneInsert(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}, userRow{Email: "b@x"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(fake.Execs()); got != 1 {
		t.Fatalf("got %d execs, want 1 batched insert: %v", got, fake.Statements())
	}
	if got := len(fake.Execs()[0].Args); got != 4 {
		t.Fatalf("insert args = %d, want 4 (two rows x email,name)", got)
	}
}

func TestWithBatchSizeSplitsValueInserts(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithBatchSize(2))
	if err := scope.Add(newUsers(),
		userRow{Email: "a@x"}, userRow{Email: "b@x"},
		userRow{Email: "c@x"}, userRow{Email: "d@x"},
		userRow{Email: "e@x"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// 5 rows in batches of 2 => 3 INSERT statements (2, 2, 1).
	if got := len(fake.Execs()); got != 3 {
		t.Fatalf("got %d execs, want 3 batched inserts: %v", got, fake.Statements())
	}
}

func TestWithBatchSizeKeepsColumnSetAcrossBatches(t *testing.T) {
	// id is defaulted. Only a later batch carries a non-zero id, so every batch
	// must include the id column — the staged rows are compiled as one insert
	// (column set derived from all rows) before being split.
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithBatchSize(2))
	if err := scope.Add(newUsers(),
		userRow{Email: "a@x"}, // id zero
		userRow{Email: "b@x"}, // id zero
		userRow{ID: 9, Email: "c@x"},
	); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 2 {
		t.Fatalf("got %d execs, want 2 batches: %v", len(execs), fake.Statements())
	}
	for i, want := range []int{6, 3} { // (id,email,name)x2, then x1
		if got := len(execs[i].Args); got != want {
			t.Fatalf("execs[%d] args = %d, want %d; SQL = %q", i, got, want, execs[i].SQL)
		}
		if !strings.Contains(execs[i].SQL, `"id"`) {
			t.Fatalf("execs[%d] omitted id: %q", i, execs[i].SQL)
		}
	}
}

func TestAddPointerWritesBackViaReturning(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: usersResult(99, "c@x", "")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB())) // postgres default => RETURNING
	carol := &userRow{Email: "c@x"}
	if err := scope.Add(newUsers(), carol); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if carol.ID != 99 {
		t.Fatalf("carol.ID = %d, want 99 written back from RETURNING", carol.ID)
	}
	if !strings.Contains(fake.Queries()[0].SQL, "RETURNING") {
		t.Fatalf("expected RETURNING insert, got %q", fake.Queries()[0].SQL)
	}
}

func TestAddPointerWritesBackViaLastInsertId(t *testing.T) {
	fake := &sqltest.Fake{LastInsertID: 42}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sql.MySQLDialect{})))
	carol := &userRow{Email: "c@x"}
	if err := scope.Add(newUsers(), carol); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if carol.ID != 42 {
		t.Fatalf("carol.ID = %d, want 42 from LastInsertId", carol.ID)
	}
}

func TestExplicitUpdateWritesAllColumns(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Update(newUsers(), userRow{ID: 5, Email: "e@x", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	upd := fake.Execs()[0]
	if !strings.HasPrefix(upd.SQL, "UPDATE") {
		t.Fatalf("not an update: %q", upd.SQL)
	}
	// SET email, name (not id) ... WHERE id = 5
	if len(upd.Args) != 3 || upd.Args[len(upd.Args)-1] != int64(5) {
		t.Fatalf("update args = %v, want [email, name, 5]", upd.Args)
	}
}

func TestRemoveDeletesByPrimaryKey(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Remove(newUsers(), userRow{ID: 9}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	del := fake.Execs()[0]
	if !strings.HasPrefix(del.SQL, "DELETE") {
		t.Fatalf("not a delete: %q", del.SQL)
	}
	if len(del.Args) != 1 || del.Args[0] != int64(9) {
		t.Fatalf("delete args = %v, want [9]", del.Args)
	}
}

// --- tracking: Get / Find + automatic dirty UPDATE ---

func TestManagedDirtyUpdatesOnlyChangedColumns(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: usersResult(7, "a@x", "Alice")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var u userRow
	found, err := scope.Get(ctx(), newUsers(), &u, int64(7))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	u.Name = "Bob" // only name changes
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := verbs(fake.Execs()); len(got) != 1 || got[0] != "UPDATE" {
		t.Fatalf("verbs = %v, want one UPDATE", got)
	}
	upd := fake.Execs()[0]
	// Only the name column is set, then the PK predicate: args == ["Bob", 7].
	if len(upd.Args) != 2 || upd.Args[0] != "Bob" || upd.Args[1] != int64(7) {
		t.Fatalf("update args = %v, want [Bob, 7]", upd.Args)
	}
}

func TestManagedNoChangeEmitsNothing(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: usersResult(7, "a@x", "Alice")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(7)); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0 (no change): %v", got, fake.Statements())
	}
}

func TestFindTracksSliceElements(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{
			Columns: []string{"id", "email", "name"},
			Rows:    [][]driver.Value{{int64(1), "a@x", "A"}, {int64(2), "b@x", "B"}},
		}, nil
	}}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var users []userRow
	if err := scope.Find(ctx(), newUsers(), &users); err != nil {
		t.Fatalf("find: %v", err)
	}
	users[1].Name = "Bobby" // mutate the second row only
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := verbs(fake.Execs()); len(got) != 1 || got[0] != "UPDATE" {
		t.Fatalf("verbs = %v, want one UPDATE for the changed row", got)
	}
	if upd := fake.Execs()[0]; upd.Args[len(upd.Args)-1] != int64(2) {
		t.Fatalf("update targeted pk %v, want 2", upd.Args[len(upd.Args)-1])
	}
}

func TestManagedPrimaryKeyChangeIsRejected(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: usersResult(7, "a@x", "Alice")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(7)); err != nil {
		t.Fatal(err)
	}
	u.ID = 8 // identity must not change
	if err := scope.Flush(ctx()); !errors.Is(err, uow.ErrPrimaryKeyChanged) {
		t.Fatalf("flush err = %v, want ErrPrimaryKeyChanged", err)
	}
}

// --- nested scopes (savepoints) ---

func TestNestedScopeUsesSavepoint(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	err := db.WithSession(func(s *sqlkit.Session) error {
		scope := uow.NewScope(s)
		if err := scope.Add(newUsers(), userRow{Email: "root@x"}); err != nil {
			return err
		}
		if err := scope.Flush(ctx()); err != nil {
			return err
		}
		child := scope.NewScope()
		if err := child.Add(newUsers(), userRow{Email: "child@x"}); err != nil {
			return err
		}
		return child.Flush(ctx())
	})
	if err != nil {
		t.Fatalf("with session: %v", err)
	}
	stmts := strings.Join(fake.Statements(), "\n")
	if !strings.Contains(stmts, "SAVEPOINT sp_1") {
		t.Fatalf("expected a SAVEPOINT for the nested scope:\n%s", stmts)
	}
}

func TestNestedScopeRollback(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	err := db.WithSession(func(s *sqlkit.Session) error {
		child := uow.NewScope(s).NewScope()
		if err := child.Add(newUsers(), userRow{Email: "child@x"}); err != nil {
			return err
		}
		if err := child.Flush(ctx()); err != nil {
			return err
		}
		return child.Rollback(ctx())
	})
	if err != nil {
		t.Fatalf("with session: %v", err)
	}
	stmts := fake.Statements()
	joined := strings.Join(stmts, "\n")
	if !strings.Contains(joined, "ROLLBACK TO SAVEPOINT sp_1") || !strings.Contains(joined, "RELEASE SAVEPOINT sp_1") {
		t.Fatalf("expected ROLLBACK TO + RELEASE for the nested scope:\n%s", joined)
	}
}

func TestNestedScopeRequiresTransaction(t *testing.T) {
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB())) // a Database is not savepoint-capable
	child := scope.NewScope()
	if err := child.Add(newUsers(), userRow{Email: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := child.Flush(ctx()); !errors.Is(err, uow.ErrNestingRequiresTx) {
		t.Fatalf("flush err = %v, want ErrNestingRequiresTx", err)
	}
}

// --- identity reconciliation ---

func TestGeneratorMismatchConflicts(t *testing.T) {
	scope := uow.NewScope(sqlkit.WrapDB((&sqltest.Fake{}).DB()))
	if err := scope.Add(newUsersFull(usersChecksum, "fmt1+sqlkitv1"), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	err := scope.Add(newUsersFull(usersChecksum, "fmt1+sqlkitv2"), userRow{Email: "b@x"})
	if !errors.Is(err, uow.ErrBindingConflict) {
		t.Fatalf("err = %v, want ErrBindingConflict", err)
	}
}

func TestStructureMismatchConflicts(t *testing.T) {
	scope := uow.NewScope(sqlkit.WrapDB((&sqltest.Fake{}).DB()))
	if err := scope.Add(newUsersFull("checksum-a", "gen"), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	err := scope.Add(newUsersFull("checksum-b", "gen"), userRow{Email: "b@x"})
	if !errors.Is(err, uow.ErrBindingConflict) {
		t.Fatalf("err = %v, want ErrBindingConflict", err)
	}
}

func TestWithIdentitiesMatching(t *testing.T) {
	im, err := meta.Metadata{Tables: []meta.Table{usersMeta()}}.Identities()
	if err != nil {
		t.Fatal(err)
	}
	fake := &sqltest.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithIdentities(im))
	binding := newUsersFull(usersMeta().Key().Checksum, "")
	if err := scope.Add(binding, userRow{Email: "a@x"}); err != nil {
		t.Fatalf("Add with matching identity: %v", err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func TestWithIdentitiesConflict(t *testing.T) {
	im, err := meta.Metadata{Tables: []meta.Table{usersMeta()}}.Identities()
	if err != nil {
		t.Fatal(err)
	}
	scope := uow.NewScope(sqlkit.WrapDB((&sqltest.Fake{}).DB()), uow.WithIdentities(im))
	// A binding whose checksum differs from the catalog table.
	err = scope.Add(newUsersFull("drifted", ""), userRow{Email: "a@x"})
	if !errors.Is(err, meta.ErrTableConflict) {
		t.Fatalf("err = %v, want ErrTableConflict", err)
	}
}

func TestWithIdentitiesUnknownTable(t *testing.T) {
	im, err := meta.Metadata{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "orgs",
		Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0), PrimaryKey: true}},
	}}}.Identities()
	if err != nil {
		t.Fatal(err)
	}
	scope := uow.NewScope(sqlkit.WrapDB((&sqltest.Fake{}).DB()), uow.WithIdentities(im))
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}); err == nil {
		t.Fatal("expected an error staging a table absent from the identity map")
	}
}

// A database bound with sqlkit.WithMetadata hands its identity map to every
// scope opened over it, so validation happens without an explicit
// WithIdentities.
func TestWithMetadataAutoAdoptsIdentities(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersMeta()}}))
	scope := uow.NewScope(db)
	binding := newUsersFull(usersMeta().Key().Checksum, "")
	if err := scope.Add(binding, userRow{Email: "a@x"}); err != nil {
		t.Fatalf("Add with matching catalog binding: %v", err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func TestWithMetadataRejectsDriftedBinding(t *testing.T) {
	db := sqlkit.WrapDB((&sqltest.Fake{}).DB(), sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersMeta()}}))
	scope := uow.NewScope(db)
	// A binding whose checksum differs from the catalog table.
	err := scope.Add(newUsersFull("drifted", ""), userRow{Email: "a@x"})
	if !errors.Is(err, meta.ErrTableConflict) {
		t.Fatalf("err = %v, want ErrTableConflict", err)
	}
}

// An explicit WithIdentities takes precedence over the database's bound
// metadata.
func TestWithIdentitiesOverridesMetadata(t *testing.T) {
	// The database's catalog omits users, which would reject the binding...
	db := sqlkit.WrapDB((&sqltest.Fake{}).DB(), sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "orgs",
		Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0), PrimaryKey: true}},
	}}}))
	// ...but an explicit identity map that includes users overrides it.
	im, err := meta.Metadata{Tables: []meta.Table{usersMeta()}}.Identities()
	if err != nil {
		t.Fatal(err)
	}
	scope := uow.NewScope(db, uow.WithIdentities(im))
	binding := newUsersFull(usersMeta().Key().Checksum, "")
	if err := scope.Add(binding, userRow{Email: "a@x"}); err != nil {
		t.Fatalf("Add with overriding identity map: %v", err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
}
