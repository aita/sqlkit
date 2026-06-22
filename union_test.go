package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

func TestUnionFirstAppliesLimit(t *testing.T) {
	var gotSQL string
	fake := &testutil.Fake{
		QueryFunc: func(sqlText string, args []any) (testutil.Result, error) {
			gotSQL = sqlText
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var id int64
	err := db.Select(Users.ID).From(Users).
		Union(sqlkit.Select(Users.ID).From(Users)).
		First(context.Background()).Scan(&id)
	if err != nil {
		t.Fatalf("First().Scan() error = %v", err)
	}
	if id != 1 || !strings.Contains(gotSQL, "LIMIT 1") {
		t.Fatalf("id = %d, sql = %q, want LIMIT 1", id, gotSQL)
	}
}

func TestUnionOneMultipleRowsIsErrTooManyRows(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}, {int64(2)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var id int64
	err := db.Select(Users.ID).From(Users).
		Union(sqlkit.Select(Users.ID).From(Users)).
		One(context.Background()).Scan(&id)
	if !errors.Is(err, sqlkit.ErrTooManyRows) {
		t.Fatalf("One().Scan() = %v, want ErrTooManyRows", err)
	}
}

func TestUnionPageAppliesLimitAndOffset(t *testing.T) {
	var gotSQL string
	fake := &testutil.Fake{
		QueryFunc: func(sqlText string, args []any) (testutil.Result, error) {
			gotSQL = sqlText
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ids []int64
	err := db.Select(Users.ID).From(Users).
		Union(sqlkit.Select(Users.ID).From(Users)).
		Page(context.Background(), 10, 30).Scan(&ids)
	if err != nil {
		t.Fatalf("Page().Scan() error = %v", err)
	}
	if !strings.Contains(gotSQL, "LIMIT 10 OFFSET 30") {
		t.Fatalf("sql = %q, want LIMIT 10 OFFSET 30", gotSQL)
	}
}

func TestUnionChain(t *testing.T) {
	query := sqlkit.Select(Users.ID).From(Users).Where(Users.Name.IsNotNull()).
		Union(sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Lt(10))).
		OrderBy(Users.ID.Asc()).
		Limit(20)

	assertToSQL(t, query,
		`(SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."name" IS NOT NULL)) UNION (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" < $1)) ORDER BY "public"."users"."id" LIMIT 20`,
		[]any{int64(10)})
}

func TestUnionHelperVariadic(t *testing.T) {
	query := sqlkit.Union(
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Eq(1)),
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Eq(2)),
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Eq(3)),
	).OrderBy(sql.Asc(sql.Col("id"))).Limit(10)

	assertToSQL(t, query,
		`(SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = $1)) UNION (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = $2)) UNION (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = $3)) ORDER BY "id" LIMIT 10`,
		[]any{int64(1), int64(2), int64(3)})
}

func TestSetOpHelpers(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Lt(10))

	assertToSQL(t, sqlkit.UnionAll(a, b),
		`(SELECT "public"."users"."id" FROM "public"."users") UNION ALL (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" < $1))`,
		[]any{int64(10)})
	assertToSQL(t, sqlkit.Intersect(a, b),
		`(SELECT "public"."users"."id" FROM "public"."users") INTERSECT (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" < $1))`,
		[]any{int64(10)})
	assertToSQL(t, sqlkit.Except(a, b),
		`(SELECT "public"."users"."id" FROM "public"."users") EXCEPT (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" < $1))`,
		[]any{int64(10)})
}

func TestUnionHelperBindsExecutor(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var users []MyUserDTO
	err := sqlkit.UnionAll(
		db.Select().From(Users).Where(Users.ID.Eq(1)),
		sqlkit.Select().From(Users).Where(Users.ID.Eq(2)),
	).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Union helper All().Scan() error = %v", err)
	}
	if len(users) != 2 || users[1].Mail != "b@example.com" {
		t.Fatalf("users = %#v", users)
	}
}

func TestUnionScans(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var users []MyUserDTO
	err := db.Select().From(Users).Where(Users.ID.Eq(1)).
		UnionAll(sqlkit.Select().From(Users).Where(Users.ID.Eq(2))).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Union All().Scan() error = %v", err)
	}
	if len(users) != 2 || users[1].Mail != "b@example.com" {
		t.Fatalf("users = %#v", users)
	}
}

func TestUnionCount(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sqlText string, args []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(5)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	n, err := db.Select(Users.ID).From(Users).
		UnionAll(sqlkit.Select(Users.ID).From(Users)).
		Count(context.Background())
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if n != 5 {
		t.Fatalf("Count() = %d, want 5", n)
	}
	want := `SELECT COUNT(*) FROM ((SELECT "public"."users"."id" FROM "public"."users") UNION ALL (SELECT "public"."users"."id" FROM "public"."users")) AS "q"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("Count() SQL = %q,\nwant %q", got, want)
	}
}

// TestSetOperationChains covers the multi-operand Intersect and Except helpers,
// which left-fold the trailing operands onto the started compound (the Union
// helper is exercised elsewhere; this pins the other set operations).
func TestSetOperationChains(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)
	c := sqlkit.Select(Posts.UserID).From(Posts)

	assertToSQL(t, sqlkit.Intersect(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") INTERSECT (SELECT "public"."posts"."id" FROM "public"."posts") INTERSECT (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
		nil)

	assertToSQL(t, sqlkit.Except(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") EXCEPT (SELECT "public"."posts"."id" FROM "public"."posts") EXCEPT (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
		nil)
}

// TestUnionOrderLimitOffset covers the OrderBy/Limit/Offset modifiers applied to
// the combined result of a set operation.
func TestUnionOrderLimitOffset(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)

	assertToSQL(t, sqlkit.Union(a, b).OrderBy(sql.Asc(sql.Col("id"))).Limit(5).Offset(2),
		`(SELECT "public"."users"."id" FROM "public"."users") UNION (SELECT "public"."posts"."id" FROM "public"."posts") ORDER BY "id" LIMIT 5 OFFSET 2`,
		nil)
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

// TestIntersectAllExceptAllHelpers covers the package-level IntersectAll and
// ExceptAll helpers (the multiset variants), including folding a third operand
// onto the chain, which exercises UnionQuery.IntersectAll/ExceptAll too.
func TestIntersectAllExceptAllHelpers(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)
	c := sqlkit.Select(Posts.UserID).From(Posts)

	assertToSQL(t, sqlkit.IntersectAll(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") INTERSECT ALL (SELECT "public"."posts"."id" FROM "public"."posts") INTERSECT ALL (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
		nil)
	assertToSQL(t, sqlkit.ExceptAll(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") EXCEPT ALL (SELECT "public"."posts"."id" FROM "public"."posts") EXCEPT ALL (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
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

// TestUnionAppendKeepsDeferredIN guards against a regression where appending an
// operand to an existing set operation dropped that operand's deferred column IN.
// A lone "col IN (values)" is held back from the SelectQuery's query (so All can
// chunk it) and folded in only by finalQuery; the UnionQuery append methods must
// finalize each operand, or the WHERE clause vanishes from the compiled SQL. The
// third operand here is appended through UnionQuery.Union via foldSetOp.
func TestUnionAppendKeepsDeferredIN(t *testing.T) {
	deferred := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.In(1, 2, 3))
	union := sqlkit.Union(
		sqlkit.Select(Users.ID).From(Users),
		sqlkit.Select(Users.ID).From(Users),
		deferred,
	)
	assertToSQL(t, union,
		`(SELECT "public"."users"."id" FROM "public"."users") UNION (SELECT "public"."users"."id" FROM "public"."users") UNION (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" IN ($1, $2, $3)))`,
		[]any{int64(1), int64(2), int64(3)})
}

func TestTypedUnionAsSubquery(t *testing.T) {
	sub := sqlkit.Union(
		sqlkit.Select(Users.ID).From(Users),
		sqlkit.Select(Posts.ID).From(Posts),
	).As("ids")
	query := sqlkit.Select(sub.Col("id")).From(sub)

	assertToSQL(t, query,
		`SELECT "ids"."id" FROM ((SELECT "public"."users"."id" FROM "public"."users") UNION (SELECT "public"."posts"."id" FROM "public"."posts")) AS "ids"`,
		nil)
}
