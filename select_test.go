package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

func TestTypedSelectWithCTE(t *testing.T) {
	active := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Gt(10)).CTE("active")
	query := sqlkit.Select(active.Col("id")).From(active).WithCTE(active)

	assertToSQL(t, query,
		`WITH "active" AS (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" > $1)) SELECT "active"."id" FROM "active"`,
		[]any{int64(10)})
}

// A recursive CTE: build the body with UnionAll first, then convert with the
// Recursive option, referencing the CTE by name in the recursive term.
func TestTypedSelectWithRecursiveCTE(t *testing.T) {
	base := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Eq(1))
	step := sqlkit.Select(sql.Col("users.id")).
		From(Users).
		Join(sql.Tbl("tree"), sqlkit.Cond(sql.Eq(sql.Col("users.parent_id"), sql.Col("tree.id"))))
	tree := base.UnionAll(step).CTE("tree", sql.Recursive(), sql.Columns("id"))
	query := sqlkit.Select(tree.Col("id")).From(tree).WithCTE(tree)

	assertToSQL(t, query,
		`WITH RECURSIVE "tree" ("id") AS ((SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = $1)) UNION ALL (SELECT "users"."id" FROM "public"."users" JOIN "tree" ON ("users"."parent_id" = "tree"."id"))) SELECT "tree"."id" FROM "tree"`,
		[]any{int64(1)})
}

func TestSelectFromExpandsColumns(t *testing.T) {
	query := sqlkit.Select().From(Users).
		Where(Users.ID.Gt(10), Users.Name.IsNotNull()).
		OrderBy(Users.Email.Asc(), Users.ID.Desc()).
		Limit(5).
		Offset(10)

	assertToSQL(t, query,
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE (("public"."users"."id" > $1) AND ("public"."users"."name" IS NOT NULL)) ORDER BY "public"."users"."email", "public"."users"."id" DESC LIMIT 5 OFFSET 10`,
		[]any{int64(10)})
}

func TestSelectExplicitColumns(t *testing.T) {
	query := sqlkit.Select(Users.ID, Users.Email).From(Users)
	assertToSQL(t, query,
		`SELECT "public"."users"."id", "public"."users"."email" FROM "public"."users"`,
		nil)
}

func TestSelectStarOfTable(t *testing.T) {
	query := sqlkit.Select(sqlkit.Star(Users), Posts.Title).From(Users).
		Join(Posts, Posts.UserID.EqExpr(Users.ID))
	assertToSQL(t, query,
		`SELECT "public"."users".*, "public"."posts"."title" FROM "public"."users" JOIN "public"."posts" ON ("public"."posts"."user_id" = "public"."users"."id")`,
		nil)
}

func TestSelectStarOfAliasedTable(t *testing.T) {
	u := Users.As("u")
	query := sqlkit.Select(sqlkit.Star(u)).From(u)
	assertToSQL(t, query, `SELECT "u".* FROM "public"."users" AS "u"`, nil)
}

func TestSelectJoinFilters(t *testing.T) {
	query := sqlkit.Select().From(Posts).
		Join(Users, Posts.UserID.EqExpr(Users.ID)).
		Where(Users.Email.Eq("a@example.com"))

	assertToSQL(t, query,
		`SELECT "public"."posts"."id", "public"."posts"."user_id", "public"."posts"."title" FROM "public"."posts" JOIN "public"."users" ON ("public"."posts"."user_id" = "public"."users"."id") WHERE ("public"."users"."email" = $1)`,
		[]any{"a@example.com"})
}

func TestAllScanIntoCustomDTO(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var users []MyUserDTO
	err := db.Select().From(Users).Where(Users.ID.Gt(0)).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("All().Scan() error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("rows = %d, want 2", len(users))
	}
	if users[0].Identifier != 1 || users[0].Mail != "a@example.com" || *users[0].Name != "Alice" {
		t.Fatalf("row 0 = %#v", users[0])
	}
	if users[1].Name != nil {
		t.Fatalf("row 1 name = %v, want nil", *users[1].Name)
	}
}

func TestAllScanIntoSingleStruct(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var one MyUserDTO
	err := db.Select().From(Users).All(context.Background()).Scan(&one)
	if err != nil {
		t.Fatalf("Scan(&struct) error = %v", err)
	}
	if one.Identifier != 1 {
		t.Fatalf("one = %#v", one)
	}
}

func TestAllScanSingleStructNoRowsIsErrNoRows(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var one MyUserDTO
	err := db.Select().From(Users).All(context.Background()).Scan(&one)
	if !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("Scan() = %v, want ErrNoRows", err)
	}
}

func TestFirstAppliesLimitAndScansFirstRow(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			gotSQL = sqlText
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(1), "a@example.com", "Alice"}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var one MyUserDTO
	if err := db.Select().From(Users).First(context.Background()).Scan(&one); err != nil {
		t.Fatalf("First().Scan() error = %v", err)
	}
	if one.Identifier != 1 {
		t.Fatalf("one = %#v", one)
	}
	if !strings.Contains(gotSQL, "LIMIT 1") {
		t.Fatalf("sql = %q, want LIMIT 1 pushed into the query", gotSQL)
	}
}

func TestFirstNoRowsIsErrNoRows(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var one MyUserDTO
	err := db.Select().From(Users).First(context.Background()).Scan(&one)
	if !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("First().Scan() = %v, want ErrNoRows", err)
	}
}

func TestOneAppliesLimitAndScansSingleRow(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			gotSQL = sqlText
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(1), "a@example.com", "Alice"}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var one MyUserDTO
	if err := db.Select().From(Users).One(context.Background()).Scan(&one); err != nil {
		t.Fatalf("One().Scan() error = %v", err)
	}
	if one.Identifier != 1 {
		t.Fatalf("one = %#v", one)
	}
	if !strings.Contains(gotSQL, "LIMIT 2") {
		t.Fatalf("sql = %q, want LIMIT 2 pushed into the query", gotSQL)
	}
}

func TestOneMultipleRowsIsErrTooManyRows(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB()) // two rows

	var one MyUserDTO
	err := db.Select().From(Users).One(context.Background()).Scan(&one)
	if !errors.Is(err, sqlkit.ErrTooManyRows) {
		t.Fatalf("One().Scan() = %v, want ErrTooManyRows", err)
	}
}

func TestOneNoRowsIsErrNoRows(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var one MyUserDTO
	err := db.Select().From(Users).One(context.Background()).Scan(&one)
	if !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("One().Scan() = %v, want ErrNoRows", err)
	}
}

func TestOneRejectsSliceDestination(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())

	var many []MyUserDTO
	err := db.Select().From(Users).One(context.Background()).Scan(&many)
	if err == nil || !strings.Contains(err.Error(), "single-row destination") {
		t.Fatalf("One().Scan(&slice) = %v, want single-row destination error", err)
	}
}

func TestOneScansScalar(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var id int64
	if err := db.Select(Users.ID).From(Users).One(context.Background()).Scan(&id); err != nil {
		t.Fatalf("One().Scan(&int64) error = %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
}

func TestFirstScansScalar(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var id int64
	if err := db.Select(Users.ID).From(Users).First(context.Background()).Scan(&id); err != nil {
		t.Fatalf("First().Scan(&int64) error = %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
}

func TestPageAppliesLimitAndOffset(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			gotSQL = sqlText
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(3), "c@example.com", "Carol"}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []MyUserDTO
	if err := db.Select().From(Users).Page(context.Background(), 20, 40).Scan(&users); err != nil {
		t.Fatalf("Page().Scan() error = %v", err)
	}
	if len(users) != 1 || users[0].Identifier != 3 {
		t.Fatalf("users = %#v", users)
	}
	if !strings.Contains(gotSQL, "LIMIT 20 OFFSET 40") {
		t.Fatalf("sql = %q, want LIMIT 20 OFFSET 40 pushed into the query", gotSQL)
	}
}

func TestAllScanScalarSlice(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}, {int64(2)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ids []int64
	if err := db.Select(Users.ID).From(Users).All(context.Background()).Scan(&ids); err != nil {
		t.Fatalf("Scan(&[]int64) error = %v", err)
	}
	if len(ids) != 2 || ids[1] != 2 {
		t.Fatalf("ids = %#v", ids)
	}
}

func TestScanFailsOnUnmappedColumn(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"mystery"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []MyUserDTO
	if err := db.RawSQL(context.Background(), "SELECT mystery FROM users").Scan(&users); err == nil {
		t.Fatal("Scan with unmapped column succeeded, want error")
	}
}

func TestSelectChainGroupByHaving(t *testing.T) {
	query := sqlkit.Select(Posts.UserID).From(Posts).
		GroupBy(Posts.UserID).
		Having(sqlkit.Cond(sql.Gt(sql.CountAll(), sql.Int(1))))

	assertToSQL(t, query,
		`SELECT "public"."posts"."user_id" FROM "public"."posts" GROUP BY "public"."posts"."user_id" HAVING (COUNT(*) > $1)`,
		[]any{1})
}

func TestSelectDistinct(t *testing.T) {
	query := sqlkit.Select(Users.Email).From(Users).Distinct().Where(Users.Name.IsNotNull())

	assertToSQL(t, query,
		`SELECT DISTINCT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."name" IS NOT NULL)`,
		nil)
}

func TestTypedScalarSubqueryProjection(t *testing.T) {
	// Correlated scalar subquery as a projection: count each user's posts.
	count := sqlkit.Select(sql.CountAll()).From(Posts).
		Where(Posts.UserID.EqExpr(Users.ID))

	query := sqlkit.Select(Users.ID, sql.As(sqlkit.SubExpr[int64](count), "post_count")).From(Users)

	assertToSQL(t, query,
		`SELECT "public"."users"."id", (SELECT COUNT(*) FROM "public"."posts" WHERE ("public"."posts"."user_id" = "public"."users"."id")) AS "post_count" FROM "public"."users"`,
		nil)
}

func TestTypedScalarSubqueryComparison(t *testing.T) {
	// Correlated scalar subquery in a comparison, sharing placeholder numbering.
	maxID := sqlkit.Select(sql.Max(Posts.ID)).From(Posts).
		Where(Posts.UserID.EqExpr(Users.ID)).
		Where(Posts.Title.Ne("draft"))

	query := sqlkit.Select(Users.ID).From(Users).
		Where(Users.Email.Eq("a@example.com")).
		Where(Users.ID.GtExpr(sqlkit.SubExpr[int64](maxID)))

	assertToSQL(t, query,
		`SELECT "public"."users"."id" FROM "public"."users" WHERE (("public"."users"."email" = $1) AND ("public"."users"."id" > (SELECT MAX("public"."posts"."id") FROM "public"."posts" WHERE (("public"."posts"."user_id" = "public"."users"."id") AND ("public"."posts"."title" <> $2)))))`,
		[]any{"a@example.com", "draft"})
}

func TestTypedQuantifiedComparison(t *testing.T) {
	sub := sqlkit.Select(Posts.UserID).From(Posts).Where(Posts.Title.Eq("hot"))
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.GtAll(sub)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" > ALL (SELECT "public"."posts"."user_id" FROM "public"."posts" WHERE ("public"."posts"."title" = $1)))`,
		[]any{"hot"})
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.LeAny(sub)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" <= ANY (SELECT "public"."posts"."user_id" FROM "public"."posts" WHERE ("public"."posts"."title" = $1)))`,
		[]any{"hot"})
}

func TestTypedOuterAndCrossJoins(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Posts.ID).From(Posts).RightJoin(Users, Posts.UserID.EqExpr(Users.ID)),
		`SELECT "public"."posts"."id" FROM "public"."posts" RIGHT JOIN "public"."users" ON ("public"."posts"."user_id" = "public"."users"."id")`,
		nil)
	assertToSQL(t,
		sqlkit.Select(Posts.ID).From(Posts).FullJoin(Users, Posts.UserID.EqExpr(Users.ID)),
		`SELECT "public"."posts"."id" FROM "public"."posts" FULL JOIN "public"."users" ON ("public"."posts"."user_id" = "public"."users"."id")`,
		nil)
	assertToSQL(t,
		sqlkit.Select(Posts.ID).From(Posts).CrossJoin(Users),
		`SELECT "public"."posts"."id" FROM "public"."posts" CROSS JOIN "public"."users"`,
		nil)
}

func TestTypedOrderByNulls(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).OrderBy(Users.Name.Asc().NullsLast(), Users.ID.Desc()),
		`SELECT "public"."users"."id" FROM "public"."users" ORDER BY "public"."users"."name" NULLS LAST, "public"."users"."id" DESC`,
		nil)
}

func TestCount(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(3)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	n, err := db.Select().From(Users).Where(Users.Name.IsNotNull()).Count(context.Background())
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Count() = %d, want 3", n)
	}
	want := `SELECT COUNT(*) FROM (SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."name" IS NOT NULL)) AS "q"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("Count() SQL = %q,\nwant %q", got, want)
	}
}

func TestWindowProjectionScan(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "rn"},
				Rows:    [][]driver.Value{{int64(5), int64(1)}, {int64(6), int64(2)}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ranked []struct {
		ID int64
		RN int64 `sqlkit:"rn"`
	}
	err := db.Select(
		Posts.ID,
		sql.As(sql.RowNumber().Over().PartitionBy(Posts.UserID).OrderBy(Posts.ID.Desc()), "rn"),
	).From(Posts).All(context.Background()).Scan(&ranked)
	if err != nil {
		t.Fatalf("window Scan() error = %v", err)
	}
	if len(ranked) != 2 || ranked[1].RN != 2 {
		t.Fatalf("ranked = %#v", ranked)
	}
	want := `SELECT "public"."posts"."id", ROW_NUMBER() OVER (PARTITION BY "public"."posts"."user_id" ORDER BY "public"."posts"."id" DESC) AS "rn" FROM "public"."posts"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("window SQL = %q,\nwant %q", got, want)
	}
}

func TestSelectJoinUsingAndNatural(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).JoinUsing(Posts, "id"),
		`SELECT "public"."users"."id" FROM "public"."users" JOIN "public"."posts" USING ("id")`, nil)

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).NaturalLeftJoin(Posts),
		`SELECT "public"."users"."id" FROM "public"."users" NATURAL LEFT JOIN "public"."posts"`, nil)
}

func TestSelectDistinctOn(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Posts.UserID, Posts.Title).From(Posts).
			DistinctOn(Posts.UserID).
			OrderBy(Posts.UserID.Asc(), Posts.ID.Desc()),
		`SELECT DISTINCT ON ("public"."posts"."user_id") "public"."posts"."user_id", "public"."posts"."title" `+
			`FROM "public"."posts" ORDER BY "public"."posts"."user_id", "public"."posts"."id" DESC`, nil)
}

func TestSelectForUpdateSkipLocked(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Gt(0)).ForUpdate(sqlkit.SkipLocked()),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" > $1) FOR UPDATE SKIP LOCKED`,
		[]any{int64(0)})

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).ForShare(sqlkit.NoWait()),
		`SELECT "public"."users"."id" FROM "public"."users" FOR SHARE NOWAIT`, nil)
}

func TestSelectGet(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Users.Q(db).Where(Users.Email.Eq("x")),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"x"})
	// Get adds the PK predicate and LIMIT 1.
	rows := Users.Q(db).Get(context.Background(), 7)
	if err := rows.Scan(&User{}); err != nil && err != sqlkit.ErrNoRows {
		t.Fatalf("Get() scan error = %v", err)
	}
}

func TestSelectGetWrongArity(t *testing.T) {
	db := newDB()
	if err := Users.Q(db).Get(context.Background(), 1, 2).Scan(&User{}); err == nil {
		t.Fatal("Get with too many primary-key values should error")
	}
}

func TestTypedSelectFromSubquery(t *testing.T) {
	sub := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Gt(10)).As("sub")
	query := sqlkit.Select(sub.Col("id")).From(sub)

	assertToSQL(t, query,
		`SELECT "sub"."id" FROM (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" > $1)) AS "sub"`,
		[]any{int64(10)})
}
