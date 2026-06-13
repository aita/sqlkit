package orm_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/orm"
)

var dialect = sqlkit.PostgreSQLDialect{}

func newDB() *sqlkit.Database { return sqlkit.WrapDB((&sqltest.Fake{}).DB()) }

func assertSQL(t *testing.T, qs orm.QuerySet, wantSQL string, wantArgs []any) {
	t.Helper()
	gotSQL, gotArgs, err := qs.ToSQL(dialect)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if gotSQL != wantSQL {
		t.Fatalf("ToSQL()\n got: %s\nwant: %s", gotSQL, wantSQL)
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("ToSQL() args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("ToSQL() arg[%d] = %v, want %v", i, gotArgs[i], wantArgs[i])
		}
	}
}

func TestQuerySetBaseSelect(t *testing.T) {
	db := newDB()
	assertSQL(t, Users.Queryer(db),
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users"`, nil)
}

func TestQuerySetWhereANDs(t *testing.T) {
	db := newDB()
	qs := Users.Q(db).
		Where(Users.Age.Le(20)).
		Where(Users.Name.Eq("alice"))
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE (("users"."age" <= $1) AND ("users"."name" = $2))`,
		[]any{20, "alice"})
}

func TestQuerySetJoinToOne(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).
		Join(Users.Rels.Profile).
		Where(Users.Rels.Profile.Public.Eq(true))
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" JOIN "profiles" ON ("profiles"."user_id" = "users"."id") WHERE ("profiles"."public" = $1)`,
		[]any{true})
}

func TestQuerySetJoinToManyAndLeftJoin(t *testing.T) {
	db := newDB()
	inner := Users.Queryer(db).Join(Users.Rels.Posts)
	assertSQL(t, inner,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" JOIN "posts" ON ("posts"."user_id" = "users"."id")`, nil)

	left := Users.Queryer(db).LeftJoin(Users.Rels.Posts)
	assertSQL(t, left,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" LEFT JOIN "posts" ON ("posts"."user_id" = "users"."id")`, nil)
}

func TestQuerySetBelongsToJoin(t *testing.T) {
	db := newDB()
	qs := Posts.Q(db).
		Join(Posts.Rels.User).
		Where(Posts.Rels.User.Age.Lt(30))
	assertSQL(t, qs,
		`SELECT "posts"."id", "posts"."user_id", "posts"."title" FROM "posts" JOIN "users" ON ("users"."id" = "posts"."user_id") WHERE ("users"."age" < $1)`,
		[]any{30})
}

func TestQuerySetOrderLimitOffsetDistinct(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).
		Distinct().
		OrderBy(Users.CreatedAt.Desc()).
		Limit(50).
		Offset(100)
	assertSQL(t, qs,
		`SELECT DISTINCT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" ORDER BY "users"."created_at" DESC LIMIT 50 OFFSET 100`, nil)
}

func TestQuerySetImmutable(t *testing.T) {
	db := newDB()
	base := Users.Queryer(db)
	_ = base.Where(Users.Age.Le(20)) // must not mutate base
	assertSQL(t, base,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users"`, nil)
}

func TestQuerySetBuildEscapeHatch(t *testing.T) {
	db := newDB()
	sel := Users.Queryer(db).Where(Users.Age.Le(20)).Build()
	gotSQL, args, err := sel.ToSQL(dialect)
	if err != nil {
		t.Fatalf("Build().ToSQL() error = %v", err)
	}
	want := `SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."age" <= $1)`
	if gotSQL != want {
		t.Fatalf("Build().ToSQL() = %s, want %s", gotSQL, want)
	}
	if len(args) != 1 || args[0] != 20 {
		t.Fatalf("Build().ToSQL() args = %v, want [20]", args)
	}
}

func TestQuerySetGetByPrimaryKey(t *testing.T) {
	var gotSQL string
	var gotArgs []any
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			gotSQL, gotArgs = s, args
			return sqltest.Result{
				Columns: []string{"id", "age", "name", "created_at"},
				Rows:    [][]driver.Value{{int64(7), int64(20), "alice", "now"}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	type userRow struct {
		ID        int64
		Age       int
		Name      string
		CreatedAt string
	}
	var u userRow
	if err := Users.Queryer(db).Get(context.Background(), int64(7)).Scan(&u); err != nil {
		t.Fatalf("Get().Scan() error = %v", err)
	}
	want := `SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."id" = $1) LIMIT 1`
	if gotSQL != want {
		t.Fatalf("Get() SQL = %s, want %s", gotSQL, want)
	}
	if len(gotArgs) != 1 || gotArgs[0] != int64(7) {
		t.Fatalf("Get() args = %v, want [7]", gotArgs)
	}
	if u.ID != 7 || u.Name != "alice" {
		t.Fatalf("Get() row = %+v", u)
	}
}

func TestQuerySetGetWrongArity(t *testing.T) {
	db := newDB()
	err := Users.Queryer(db).Get(context.Background(), int64(1), int64(2)).Scan(&struct{ ID int64 }{})
	if err == nil {
		t.Fatal("Get() with too many keys: want error, got nil")
	}
}

func TestQuerySetCount(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			gotSQL = s
			return sqltest.Result{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(3)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	n, err := Users.Queryer(db).Where(Users.Age.Le(20)).Count(context.Background())
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Count() = %d, want 3", n)
	}
	if gotSQL == "" {
		t.Fatal("Count() ran no query")
	}
}

func TestQuerySetInsertDelete(t *testing.T) {
	var execs []string
	fake := &sqltest.Fake{ExecFunc: func(s string, args []any) error {
		execs = append(execs, s)
		return nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	type userRow struct {
		Age  int
		Name string
	}
	if _, err := Users.Queryer(db).Insert().Values(userRow{Age: 20, Name: "alice"}).Exec(context.Background()); err != nil {
		t.Fatalf("Insert().Exec() error = %v", err)
	}
	if _, err := Users.Queryer(db).Delete().Where(Users.ID.Eq(7)).Exec(context.Background()); err != nil {
		t.Fatalf("Delete().Exec() error = %v", err)
	}
	if len(execs) != 2 {
		t.Fatalf("expected 2 exec statements, got %d: %v", len(execs), execs)
	}
}

func TestRelationMetadata(t *testing.T) {
	if got := Users.Rels.Profile.Cardinality(); got != orm.ToOne {
		t.Errorf("Profile cardinality = %v, want ToOne", got)
	}
	if got := Users.Rels.Posts.Cardinality(); got != orm.ToMany {
		t.Errorf("Posts cardinality = %v, want ToMany", got)
	}
	if got := Users.Rels.Profile.Name(); got != "Profile" {
		t.Errorf("Profile name = %q, want Profile", got)
	}
	if got := orm.ToMany.String(); got != "ToMany" {
		t.Errorf("ToMany.String() = %q", got)
	}
}

func TestQuerySetGetNoKeys(t *testing.T) {
	db := newDB()
	err := Users.Queryer(db).Get(context.Background()).Scan(&struct{ ID int64 }{})
	if err == nil {
		t.Fatal("Get() with no keys: want error")
	}
}
