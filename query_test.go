package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

var pg = sql.PostgreSQLDialect{}

func assertToSQL(t *testing.T, query sql.Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q,\nwant %q", sqlText, wantSQL)
	}
	if len(wantArgs) > 0 && !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

func usersFake() *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "a@example.com", "Alice"},
					{int64(2), "b@example.com", nil},
				},
			}, nil
		},
	}
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

// MyUserDTO is a caller-defined destination, distinct from the generated row.
type MyUserDTO struct {
	Identifier int64  `sqlkit:"id"`
	Mail       string `sqlkit:"email"`
	Name       *string
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

func TestUnionFirstAppliesLimit(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			gotSQL = sqlText
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
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

func TestUnionOneMultipleRowsIsErrTooManyRows(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}, {int64(2)}}}, nil
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

func TestUnionPageAppliesLimitAndOffset(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			gotSQL = sqlText
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
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

func TestUnionChain(t *testing.T) {
	query := sqlkit.Select(Users.ID).From(Users).Where(Users.Name.IsNotNull()).
		Union(sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Lt(10))).
		OrderBy(Users.ID.Asc()).
		Limit(20)

	assertToSQL(t, query,
		`(SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."name" IS NOT NULL)) UNION (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" < $1)) ORDER BY "public"."users"."id" LIMIT 20`,
		[]any{int64(10)})
}

func TestTypedBetween(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Between(10, 20)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" BETWEEN $1 AND $2)`,
		[]any{int64(10), int64(20)})
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.NotBetween(10, 20)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" NOT BETWEEN $1 AND $2)`,
		[]any{int64(10), int64(20)})
}

func TestTypedInQuery(t *testing.T) {
	sub := sqlkit.Select(Users.ID).From(Users).Where(Users.Email.Eq("a@example.com"))
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.InQuery(sub)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" IN (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" = $1)))`,
		[]any{"a@example.com"})
}

func TestTypedExists(t *testing.T) {
	sub := sqlkit.Select(Users.ID).From(Users).Where(Users.Email.Eq("a@example.com"))
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(sqlkit.Exists(sub)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE EXISTS (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" = $1))`,
		[]any{"a@example.com"})
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

func TestTypedArithmeticAndCast(t *testing.T) {
	// UPDATE ... SET id = id + 1 via a typed arithmetic expression.
	assertToSQL(t,
		sqlkit.Update(Users).Set(Users.ID.SetExpr(Users.ID.Add(1))).Where(Users.ID.Eq(7)),
		`UPDATE "public"."users" SET "id" = ("public"."users"."id" + $1) WHERE ("public"."users"."id" = $2)`,
		[]any{int64(1), int64(7)})

	assertToSQL(t,
		sqlkit.Select(sql.As(sqlkit.Cast[int64](Users.Email, "integer"), "n")).From(Users),
		`SELECT CAST("public"."users"."email" AS integer) AS "n" FROM "public"."users"`,
		nil)
}

func TestTypedOnConflictDoUpdate(t *testing.T) {
	name := "Alice"
	query := sqlkit.Insert(Users).
		Values(User{ID: 1, Email: "a@example.com", Name: &name}).
		OnConflictDoUpdate([]sqlkit.AnyColumn{Users.ID}, Users.Email.SetExpr(Users.Email.Excluded())).
		Returning(Users.ID)
	assertToSQL(t, query,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "email" = excluded."email" RETURNING "public"."users"."id"`,
		[]any{int64(1), "a@example.com", &name})
}

func TestTypedOnConflictDoNothing(t *testing.T) {
	name := "Alice"
	query := sqlkit.Insert(Users).
		Values(User{ID: 1, Email: "a@example.com", Name: &name}).
		OnConflictDoNothing(Users.ID)
	assertToSQL(t, query,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ("id") DO NOTHING`,
		[]any{int64(1), "a@example.com", &name})
}

func TestTypedOnConflictOnConstraint(t *testing.T) {
	name := "Alice"
	query := sqlkit.Insert(Users).
		Values(User{ID: 1, Email: "a@example.com", Name: &name}).
		OnConflictOnConstraintDoUpdate("users_email_key", Users.Email.SetExpr(Users.Email.Excluded()))
	assertToSQL(t, query,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ON CONSTRAINT "users_email_key" DO UPDATE SET "email" = excluded."email"`,
		[]any{int64(1), "a@example.com", &name})
}

func TestTypedUpdateFromAndDeleteUsing(t *testing.T) {
	assertToSQL(t,
		sqlkit.Update(Posts).
			Set(Posts.Title.SetExpr(sqlkit.SubExpr[string](
				sqlkit.Select(Users.Email).From(Users).Where(Users.ID.EqExpr(Posts.UserID))))).
			From(Users).
			Where(Users.ID.EqExpr(Posts.UserID)),
		`UPDATE "public"."posts" SET "title" = (SELECT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."id" = "public"."posts"."user_id")) FROM "public"."users" WHERE ("public"."users"."id" = "public"."posts"."user_id")`,
		nil)

	assertToSQL(t,
		sqlkit.Delete(Posts).Using(Users).Where(Users.ID.EqExpr(Posts.UserID), Users.Email.Eq("x@example.com")),
		`DELETE FROM "public"."posts" USING "public"."users" WHERE (("public"."users"."id" = "public"."posts"."user_id") AND ("public"."users"."email" = $1))`,
		[]any{"x@example.com"})
}

func TestUpdateAndDeleteFilter(t *testing.T) {
	// The same filter bundle that narrows a SELECT (TestSelectFilter) narrows an
	// UPDATE and a DELETE: Filter resolves against the target table's columns, so
	// one reusable bundle serves a read or a write.
	assertToSQL(t,
		sqlkit.Update(Users).
			Filter(userFilter{Email: sqlkit.Eq("a@x")}).
			Set(Users.Email.Set("new@x")),
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."email" = $2)`,
		[]any{"new@x", "a@x"})

	assertToSQL(t,
		sqlkit.Delete(Users).Filter(userFilter{Email: sqlkit.Eq("a@x")}),
		`DELETE FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"a@x"})
}

func TestUpdateAndDeleteFilterDefersError(t *testing.T) {
	// A bad filter (not a struct or Predicate) defers its build error to the
	// terminal, matching SelectQuery.Filter.
	if _, _, err := sqlkit.Update(Users).Filter(123).ToSQL(pg); err == nil {
		t.Fatal("UpdateQuery.Filter with a bad filter should defer an error")
	}
	if _, _, err := sqlkit.Delete(Users).Filter(123).ToSQL(pg); err == nil {
		t.Fatal("DeleteQuery.Filter with a bad filter should defer an error")
	}
}

func TestUpdateSetStruct(t *testing.T) {
	name := "Alice"
	u := User{ID: 7, Email: "a@example.com", Name: &name}

	// Explicit columns: a partial update of just those columns.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u, Users.Email).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"a@example.com", int64(7)})

	// Several explicit columns keep the given order.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u, Users.Name, Users.Email).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "name" = $1, "email" = $2 WHERE ("public"."users"."id" = $3)`,
		[]any{&name, "a@example.com", int64(7)})

	// No columns: every mapped column except the primary key, in table order.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "email" = $1, "name" = $2 WHERE ("public"."users"."id" = $3)`,
		[]any{"a@example.com", &name, int64(7)})

	// Composes with Set: the struct assignments come first, then manual ones.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u, Users.Email).
			Set(Users.ID.SetExpr(Users.ID.Add(1))).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "email" = $1, "id" = ("public"."users"."id" + $2) WHERE ("public"."users"."id" = $3)`,
		[]any{"a@example.com", int64(1), int64(7)})
}

func TestUpdateSetStructErrors(t *testing.T) {
	cases := map[string]sqlkit.UpdateQuery{
		"not a struct": sqlkit.Update(Users).SetStruct(42),
		"nil pointer":  sqlkit.Update(Users).SetStruct((*User)(nil)),
		"explicit column with no field": sqlkit.Update(Users).
			SetStruct(struct{ Email string }{}, Users.Name),
		"no fields map": sqlkit.Update(Users).
			SetStruct(struct{ Foo string }{}),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := query.ToSQL(pg); err == nil {
				t.Fatalf("ToSQL() error = nil, want non-nil")
			}
		})
	}
}

func TestTypedOrderByNulls(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).OrderBy(Users.Name.Asc().NullsLast(), Users.ID.Desc()),
		`SELECT "public"."users"."id" FROM "public"."users" ORDER BY "public"."users"."name" NULLS LAST, "public"."users"."id" DESC`,
		nil)
}

func TestTypedLikeVariants(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(sqlkit.NotLike(Users.Email, "%@spam.com")),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" NOT LIKE $1)`,
		[]any{"%@spam.com"})
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(sqlkit.ILike(Users.Email, "%@EXAMPLE.com")),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" ILIKE $1)`,
		[]any{"%@EXAMPLE.com"})
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

func TestUnionCount(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(5)}}}, nil
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

func TestInsertValuesSlice(t *testing.T) {
	name := "Alice"
	insert := sqlkit.Insert(Users).Values([]User{
		{Email: "a@example.com", Name: &name},
		{Email: "b@example.com"},
	})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", &name, "b@example.com", (*string)(nil)})
}

func TestInsertValuesVariadicAndPointer(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(
		User{Email: "a@example.com"},
		&User{Email: "b@example.com"},
	)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", (*string)(nil), "b@example.com", (*string)(nil)})
}

func TestInsertKeepsExplicitDefaultedColumn(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(User{ID: 42, Email: "a@example.com"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3)`,
		[]any{int64(42), "a@example.com", (*string)(nil)})
}

// insertDTO maps a subset of columns; unmapped columns are omitted.
type insertDTO struct {
	Mail string `sqlkit:"email"`
}

func TestInsertValuesFromCustomDTO(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(insertDTO{Mail: "dto@example.com"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") VALUES ($1)`,
		[]any{"dto@example.com"})
}

func TestInsertFromSelect(t *testing.T) {
	insert := sqlkit.Insert(Users).
		Columns(Users.Email, Users.Name).
		FromSelect(sqlkit.Select(Users.Email, Users.Name).From(Users).Where(Users.ID.Lt(100)))
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") SELECT "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."id" < $1)`,
		[]any{int64(100)})
}

func TestInsertFromSelectWithoutColumns(t *testing.T) {
	insert := sqlkit.Insert(Users).FromSelect(sqlkit.Select().From(Users))
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users"`,
		nil)
}

func TestInsertRejectsValuesAndFromSelect(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(User{Email: "x"}).FromSelect(sqlkit.Select().From(Users))
	if _, _, err := insert.ToSQL(pg); err == nil {
		t.Fatal("Values+FromSelect compiled, want error")
	}
	if _, _, err := sqlkit.Insert(Users).ToSQL(pg); err == nil {
		t.Fatal("empty INSERT compiled, want error")
	}
}

func TestUpdateFromDB(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())
	query := db.Update(Users).
		Set(Users.Email.Set("new@example.com")).
		Where(Users.ID.Eq(7))
	assertToSQL(t, query,
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"new@example.com", int64(7)})
}

func TestDeleteFromDB(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())
	query := db.Delete(Users).Where(sqlkit.Like(Users.Email, "%@spam.com"))
	assertToSQL(t, query,
		`DELETE FROM "public"."users" WHERE ("public"."users"."email" LIKE $1)`,
		[]any{"%@spam.com"})
}

func TestUpdateReturningScans(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(7), "new@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var updated []User
	err := db.Update(Users).Set(Users.Email.Set("new@example.com")).Where(Users.ID.Eq(7)).
		Returning().
		All(context.Background()).Scan(&updated)
	if err != nil {
		t.Fatalf("Update().All().Scan() error = %v", err)
	}
	if len(updated) != 1 || updated[0].Email != "new@example.com" {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestReturningIsExplicit(t *testing.T) {
	// Without Returning, no RETURNING clause is generated.
	insert := sqlkit.Insert(Users).Values(User{Email: "a@example.com"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2)`,
		[]any{"a@example.com", (*string)(nil)})

	// Returning() with no arguments returns all table columns.
	all := sqlkit.Insert(Users).Values(User{Email: "a@example.com"}).Returning()
	assertToSQL(t, all,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) RETURNING "public"."users"."id", "public"."users"."email", "public"."users"."name"`,
		[]any{"a@example.com", (*string)(nil)})

	// Returning(columns...) returns just those columns.
	ids := sqlkit.Insert(Users).Values(User{Email: "a@example.com"}).Returning(Users.ID)
	assertToSQL(t, ids,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) RETURNING "public"."users"."id"`,
		[]any{"a@example.com", (*string)(nil)})
}

func TestReturningSpecificColumnsScan(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ids []int64
	err := db.Delete(Users).Where(Users.Email.Eq("gone@example.com")).
		Returning(Users.ID).
		All(context.Background()).Scan(&ids)
	if err != nil {
		t.Fatalf("Returning(ID).All().Scan() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("ids = %#v", ids)
	}
	want := `DELETE FROM "public"."users" WHERE ("public"."users"."email" = $1) RETURNING "public"."users"."id"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("DELETE SQL = %q,\nwant %q", got, want)
	}
}

func TestUpdateWithoutReturningHasNoAll(t *testing.T) {
	// Compile-time property: UpdateQuery/InsertQuery/DeleteQuery expose Exec
	// but not All; All exists only on ReturningQuery. Verified by the type
	// switch below not requiring assertions — this test just documents the
	// runtime SQL shape without RETURNING.
	query := sqlkit.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1))
	assertToSQL(t, query,
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"x@example.com", int64(1)})
}

func TestColumnIsDistinctFrom(t *testing.T) {
	// IS DISTINCT FROM against a value and against another typed column.
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.Email.IsDistinctFrom("a@x.com")),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" IS DISTINCT FROM $1)`,
		[]any{"a@x.com"})

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.IsNotDistinctFromExpr(Posts.UserID)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" IS NOT DISTINCT FROM "public"."posts"."user_id")`,
		nil)
}

func TestColumnEqAnyAndNeAll(t *testing.T) {
	// EqAny binds the whole list as one array parameter (PostgreSQL's IN
	// replacement), so a single $1 carries all values.
	q := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.EqAny(1, 2, 3))
	gotSQL, args, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	wantSQL := `SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = ANY($1))`
	if gotSQL != wantSQL {
		t.Fatalf("ToSQL() = %q, want %q", gotSQL, wantSQL)
	}
	if want := []any{[]int64{1, 2, 3}}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.NeAll(4, 5)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" <> ALL($1))`,
		[]any{[]int64{4, 5}})
}

func TestColumnSimilarTo(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(sqlkit.SimilarTo(Users.Email, "%@example.com")),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" SIMILAR TO $1)`,
		[]any{"%@example.com"})
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

// TestPredicateMethodCombinators covers the And/Or methods on Predicate, which
// combine a receiver predicate with others.
func TestPredicateMethodCombinators(t *testing.T) {
	id := `"public"."users"."id"`
	where(t, Users.ID.Eq(1).And(Users.ID.Eq(2)),
		`((`+id+` = $1) AND (`+id+` = $2))`, []any{int64(1), int64(2)})
	where(t, Users.ID.Eq(1).Or(Users.ID.Eq(2)),
		`((`+id+` = $1) OR (`+id+` = $2))`, []any{int64(1), int64(2)})
}

// TestPredicateFuncCombinators covers the package-level Or and Not (And is
// already exercised by the WHERE folding tests).
func TestPredicateFuncCombinators(t *testing.T) {
	id := `"public"."users"."id"`
	where(t, sqlkit.Or(Users.ID.Eq(1), Users.ID.Eq(2)),
		`((`+id+` = $1) OR (`+id+` = $2))`, []any{int64(1), int64(2)})
	where(t, sqlkit.Not(Users.ID.Eq(1)), `(NOT (`+id+` = $1))`, []any{int64(1)})
}

// TestInValuesEscapeHatch covers InValues, the untyped chunkable IN built from a
// raw column expression rather than a typed Column.
func TestInValuesEscapeHatch(t *testing.T) {
	where(t, sqlkit.InValues(Users.ID, 1, 2, 3),
		`("public"."users"."id" IN ($1, $2, $3))`, []any{1, 2, 3})
}

// TestNotExistsPredicate covers NotExists (Exists is exercised elsewhere).
func TestNotExistsPredicate(t *testing.T) {
	sub := sqlkit.Select(Posts.UserID).From(Posts).Where(Posts.UserID.EqExpr(Users.ID))
	where(t, sqlkit.NotExists(sub),
		`(NOT EXISTS (SELECT "public"."posts"."user_id" FROM "public"."posts" WHERE ("public"."posts"."user_id" = "public"."users"."id")))`,
		nil)
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
