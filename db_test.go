package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// usersCatalog builds a representative metadata table that the WithMetadata
// tests compile into an identity map.
func usersCatalog() meta.Table {
	public := meta.Schema{Name: "public"}
	return meta.Table{
		Schema: public,
		Name:   "users",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0), PrimaryKey: true},
			{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0)},
		},
	}
}

func TestWithMetadataExposesIdentities(t *testing.T) {
	db := sqlkit.WrapDB((&testutil.Fake{}).DB(),
		sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersCatalog()}}))
	im := db.Identities()
	if im == nil {
		t.Fatal("Identities() = nil, want the compiled identity map")
	}
	if _, ok := im.KeyForName("public", "users"); !ok {
		t.Fatal("identity map does not contain public.users")
	}
	// A session derived from the database inherits the same identity map.
	if got := db.NewSession().Identities(); got != im {
		t.Fatalf("session Identities() = %p, want inherited %p", got, im)
	}
}

func TestWithoutMetadataIdentitiesNil(t *testing.T) {
	db := sqlkit.WrapDB((&testutil.Fake{}).DB())
	if db.Identities() != nil {
		t.Fatal("Identities() should be nil when no metadata is bound")
	}
}

// WrapDB cannot return an error, so a metadata conflict leaves the database
// without identities; Open is the constructor that surfaces the failure.
func TestWithMetadataConflictLeavesIdentitiesNil(t *testing.T) {
	drifted := usersCatalog()
	drifted.Columns[1].Type = meta.Of("postgres", "text", 0, 0, 0)
	db := sqlkit.WrapDB((&testutil.Fake{}).DB(),
		sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersCatalog(), drifted}}))
	if db.Identities() != nil {
		t.Fatal("Identities() should be nil when the bound metadata conflicts")
	}
}

func TestDatabaseQueryRawScan(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var users []MyUserDTO
	err := db.RawSQL(context.Background(), "SELECT id, email, name FROM users WHERE id > $1", 0).Scan(&users)
	if err != nil {
		t.Fatalf("Query().Scan() error = %v", err)
	}
	if len(users) != 2 || users[0].Mail != "a@example.com" {
		t.Fatalf("users = %#v", users)
	}
}

func TestDatabaseUnionMethod(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var users []MyUserDTO
	// Operands carry no executor; db.Union binds the result to db.
	err := db.UnionAll(
		sqlkit.Select().From(Users).Where(Users.ID.Eq(1)),
		sqlkit.Select().From(Users).Where(Users.ID.Eq(2)),
	).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("db.UnionAll().Scan() error = %v", err)
	}
	if len(users) != 2 || users[1].Mail != "b@example.com" {
		t.Fatalf("users = %#v", users)
	}
}

// TestDatabaseSQLAndDialect covers the SQL() and Dialect() accessors.
func TestDatabaseSQLAndDialect(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	if db.SQL() == nil {
		t.Fatal("SQL() = nil, want the wrapped *sql.DB handle")
	}
	if _, ok := db.Dialect().(sql.PostgreSQLDialect); !ok {
		t.Fatalf("Dialect() = %T, want PostgreSQLDialect (the default)", db.Dialect())
	}
}

// TestDatabaseSetOpMethods covers db.Union/Intersect/Except, which mirror the
// package-level helpers but bind the result to the database. Each is run so the
// binding (and thus the executor) is exercised end to end.
func TestDatabaseSetOpMethods(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)

	for name, q := range map[string]sqlkit.UnionQuery{
		"Union":     db.Union(a, b),
		"Intersect": db.Intersect(a, b),
		"Except":    db.Except(a, b),
	} {
		var ids []int64
		if err := q.All(ctx).Scan(&ids); err != nil {
			t.Fatalf("db.%s().All().Scan() error = %v", name, err)
		}
	}
}

// TestDatabaseExec covers Database.Exec, the auto-commit path for an arbitrary
// sql.Query (here an UPDATE built with the sql package).
func TestDatabaseExec(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	stmt := sql.Update(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}).Set(sql.Set("email", sql.Val("x@x")))
	if _, err := db.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(execs))
	}
}
