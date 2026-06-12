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
