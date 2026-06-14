package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

// TestDatabaseSQLAndDialect covers the SQL() and Dialect() accessors.
func TestDatabaseSQLAndDialect(t *testing.T) {
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}}, nil
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
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	stmt := sql.Update(sql.Table{Schema: "public", Name: "users"}).Set(sql.Set("email", sql.Val("x@x")))
	if _, err := db.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(execs))
	}
}

// TestUnionAllAppendsThirdOperand covers UnionQuery.UnionAll, the append-operand
// method reached when the variadic helper folds a third operand onto the chain.
func TestUnionAllAppendsThirdOperand(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)
	c := sqlkit.Select(Posts.UserID).From(Posts)
	assertToSQL(t, sqlkit.UnionAll(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") UNION ALL (SELECT "public"."posts"."id" FROM "public"."posts") UNION ALL (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
		nil)
}

// TestUnionAsSubquerySource covers UnionQuery.SQLSource: a set operation used as
// a FROM source (derived table).
func TestUnionAsSubquerySource(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.UserID).From(Posts)
	union := sqlkit.Union(a, b)
	q := sqlkit.Select().From(union.SQLSource())
	if _, _, err := q.ToSQL(sql.PostgreSQLDialect{}); err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
}
