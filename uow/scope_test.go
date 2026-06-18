package uow_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
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
func usersResult(id int64, email, name string) func(string, []any) (testutil.Result, error) {
	return func(string, []any) (testutil.Result, error) {
		return testutil.Result{
			Columns: []string{"id", "email", "name"},
			Rows:    [][]driver.Value{{id, email, name}},
		}, nil
	}
}

func verbs(calls []testutil.Call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = strings.Fields(c.SQL)[0]
	}
	return out
}

// --- staging: Add / Update / Remove ---

func TestAddValueBatchesIntoOneInsert(t *testing.T) {
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{QueryFunc: usersResult(99, "c@x", "")}
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
	fake := &testutil.Fake{LastInsertID: 42}
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
	fake := &testutil.Fake{}
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

func TestAttachUpdatesOnlyChangedColumns(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	u := &userRow{ID: 7, Email: "a@x", Name: "Alice"} // detached, e.g. from a cache
	if err := scope.Attach(newUsers(), u); err != nil {
		t.Fatal(err)
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

func TestAttachNoChangeEmitsNothing(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	u := &userRow{ID: 7, Email: "a@x", Name: "Alice"}
	if err := scope.Attach(newUsers(), u); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0 (no change): %v", got, fake.Statements())
	}
}

func TestAttachRejectsNonPointer(t *testing.T) {
	scope := uow.NewScope(sqlkit.WrapDB((&testutil.Fake{}).DB()))
	err := scope.Attach(newUsers(), userRow{ID: 7, Email: "a@x", Name: "Alice"})
	if !errors.Is(err, uow.ErrAttachRequiresPointer) {
		t.Fatalf("Attach(value) error = %v, want ErrAttachRequiresPointer", err)
	}
}

func TestRemoveDeletesByPrimaryKey(t *testing.T) {
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{QueryFunc: usersResult(7, "a@x", "Alice")}
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
	fake := &testutil.Fake{QueryFunc: usersResult(7, "a@x", "Alice")}
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
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{
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
	fake := &testutil.Fake{QueryFunc: usersResult(7, "a@x", "Alice")}
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
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{}
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
	scope := uow.NewScope(sqlkit.WrapDB((&testutil.Fake{}).DB()))
	if err := scope.Add(newUsersFull(usersChecksum, "fmt1+sqlkitv1"), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	err := scope.Add(newUsersFull(usersChecksum, "fmt1+sqlkitv2"), userRow{Email: "b@x"})
	if !errors.Is(err, uow.ErrBindingConflict) {
		t.Fatalf("err = %v, want ErrBindingConflict", err)
	}
}

func TestStructureMismatchConflicts(t *testing.T) {
	scope := uow.NewScope(sqlkit.WrapDB((&testutil.Fake{}).DB()))
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
	fake := &testutil.Fake{}
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
	scope := uow.NewScope(sqlkit.WrapDB((&testutil.Fake{}).DB()), uow.WithIdentities(im))
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
	scope := uow.NewScope(sqlkit.WrapDB((&testutil.Fake{}).DB()), uow.WithIdentities(im))
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}); err == nil {
		t.Fatal("expected an error staging a table absent from the identity map")
	}
}

// A database bound with sqlkit.WithMetadata hands its identity map to every
// scope opened over it, so validation happens without an explicit
// WithIdentities.
func TestWithMetadataAutoAdoptsIdentities(t *testing.T) {
	fake := &testutil.Fake{}
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
	db := sqlkit.WrapDB((&testutil.Fake{}).DB(), sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersMeta()}}))
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
	db := sqlkit.WrapDB((&testutil.Fake{}).DB(), sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{{
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

// --- Item 7: uow flush DELETE batching + bulk UPDATE. ---

func TestRemoveBatchesIntoChunkedInDeletes(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithBatchSize(2))
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if err := scope.Remove(newUsers(), userRow{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("deletes = %d, want 3 (2+2+1 keys): %v", len(execs), verbs(execs))
	}
	for _, e := range execs {
		if !strings.HasPrefix(e.SQL, "DELETE") || !strings.Contains(e.SQL, "IN (") {
			t.Fatalf("not a chunked IN delete: %q", e.SQL)
		}
	}
}

func TestRemoveBatchSizeChunksDeletes(t *testing.T) {
	fake := &testutil.Fake{}
	// No bind-param limit, but an explicit batch size still merges and chunks.
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithBatchSize(2))
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if err := scope.Remove(newUsers(), userRow{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n := len(fake.Execs()); n != 3 {
		t.Fatalf("deletes = %d, want 3 (batch size 2)", n)
	}
}

func TestExplicitUpdateBatchesViaValuesJoin(t *testing.T) {
	fake := &testutil.Fake{}
	// Auto-split is off by default; enable it so multiple row updates combine
	// into one VALUES-join statement.
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true)))
	if err := scope.Update(newUsers(),
		userRow{ID: 1, Email: "a@x", Name: "A"},
		userRow{ID: 2, Email: "b@x", Name: "B"},
		userRow{ID: 3, Email: "c@x", Name: "C"},
	); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("updates = %d, want 1 bulk update: %v", len(execs), verbs(execs))
	}
	if !strings.Contains(execs[0].SQL, "VALUES") || !strings.Contains(execs[0].SQL, "FROM") {
		t.Fatalf("not a VALUES-join update: %q", execs[0].SQL)
	}
	// Explicit update sets every non-PK column (email, name); each row contributes
	// the key plus those two values => 3 rows x 3 = 9 args.
	if len(execs[0].Args) != 9 {
		t.Fatalf("args = %d, want 9 (3 rows x [id, email, name])", len(execs[0].Args))
	}
}

func TestExplicitUpdateBulkChunksByBatchSize(t *testing.T) {
	fake := &testutil.Fake{}
	// 3 values per row (id, email, name); batch size 2 => 2 rows per statement.
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithBatchSize(2))
	if err := scope.Update(newUsers(),
		userRow{ID: 1, Email: "a@x", Name: "A"},
		userRow{ID: 2, Email: "b@x", Name: "B"},
		userRow{ID: 3, Email: "c@x", Name: "C"},
		userRow{ID: 4, Email: "d@x", Name: "D"},
		userRow{ID: 5, Email: "e@x", Name: "E"},
	); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("updates = %d, want 3 (2+2+1 rows)", len(execs))
	}
	for _, e := range execs {
		if !strings.Contains(e.SQL, "VALUES") {
			t.Fatalf("chunk is not a VALUES-join update: %q", e.SQL)
		}
	}
}

func TestScopeAutoChunkFalseFlushesPerRow(t *testing.T) {
	fake := &testutil.Fake{}
	// The executor has auto-chunk enabled; the scope overrides it off.
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true)), uow.WithAutoChunk(false))
	for _, id := range []int64{1, 2, 3} {
		if err := scope.Remove(newUsers(), userRow{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.Update(newUsers(),
		userRow{ID: 10, Email: "x@x", Name: "X"},
		userRow{ID: 11, Email: "y@x", Name: "Y"},
	); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 5 {
		t.Fatalf("execs = %d, want 5 (3 deletes + 2 updates, all per-row)", len(execs))
	}
	for _, e := range execs {
		if strings.Contains(e.SQL, "VALUES") || strings.Contains(e.SQL, "IN (") {
			t.Fatalf("scope auto-split off should flush per row, got %q", e.SQL)
		}
	}
}

func TestScopeAutoChunkTrueOverDisabledExecutor(t *testing.T) {
	fake := &testutil.Fake{}
	// The executor has auto-chunk off; the scope turns it on and sizes its own
	// chunks to the configured batch size.
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(false))
	scope := uow.NewScope(db, uow.WithAutoChunk(true), uow.WithBatchSize(2))
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if err := scope.Remove(newUsers(), userRow{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("deletes = %d, want 3 (2+2+1, scope-sized)", len(execs))
	}
	for _, e := range execs {
		if !strings.Contains(e.SQL, "IN (") {
			t.Fatalf("not a chunked IN delete: %q", e.SQL)
		}
	}
}

// --- Get / Find edge cases ---

// TestGetReportsNotFound covers Get's ErrNoRows branch: a missing row yields
// found=false with no error and nothing tracked.
func TestGetReportsNotFound(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id", "email", "name"}}, nil // no rows
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
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{
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
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{
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
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "A")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))

	var one userRow
	if err := scope.Find(ctx(), newUsers(), &one); err == nil {
		t.Fatal("Find() into a non-slice destination should error")
	}
}

// TestRollbackOnRootScopeErrors covers the guard that Rollback is only valid on
// a nested scope.
func TestRollbackOnRootScopeErrors(t *testing.T) {
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{}
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
	fake := &testutil.Fake{}
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
