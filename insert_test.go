package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

func TestInsertValuesMapsSnakeCaseFields(t *testing.T) {
	// Post.UserID maps to user_id without a tag.
	insert := sqlkit.Insert(Posts).Values(Post{UserID: 7, Title: "Hello"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."posts" ("user_id", "title") VALUES ($1, $2)`,
		[]any{int64(7), "Hello"})
}

func TestInsertValuesFlattensEmbeddedStructs(t *testing.T) {
	type postCore struct {
		Title string
	}
	type postInsert struct {
		postCore
		UserID int64
	}

	insert := sqlkit.Insert(Posts).Values(postInsert{postCore: postCore{Title: "Hi"}, UserID: 3})
	assertToSQL(t, insert,
		`INSERT INTO "public"."posts" ("user_id", "title") VALUES ($1, $2)`,
		[]any{int64(3), "Hi"})
}

func TestInsertValuesSkipsDashTaggedFields(t *testing.T) {
	type ignoreName struct {
		Email string
		Name  string `sqlkit:"-"`
	}

	insert := sqlkit.Insert(Users).Values(ignoreName{Email: "a@example.com", Name: "hidden"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") VALUES ($1)`,
		[]any{"a@example.com"})
}

func TestInsertValuesSliceOfPointers(t *testing.T) {
	insert := sqlkit.Insert(Users).Values([]*User{
		{Email: "a@example.com"},
		{Email: "b@example.com"},
	})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", (*string)(nil), "b@example.com", (*string)(nil)})
}

func TestInsertValuesMixedTypesWithSameColumns(t *testing.T) {
	// Two distinct struct types are fine as long as they map to the same
	// column set.
	type mailOnly struct {
		Email string
	}
	insert := sqlkit.Insert(Users).Values(
		insertDTO{Mail: "a@example.com"},
		mailOnly{Email: "b@example.com"},
	)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") VALUES ($1), ($2)`,
		[]any{"a@example.com", "b@example.com"})
}

func TestInsertValuesMixedColumnSetsFails(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(
		User{Email: "a@example.com"},     // id, email, name
		insertDTO{Mail: "b@example.com"}, // email only
	)
	_, _, err := insert.ToSQL(pg)
	if err == nil || !strings.Contains(err.Error(), "different column sets") {
		t.Fatalf("ToSQL() error = %v, want different-column-sets error", err)
	}
}

func TestInsertValuesDefaultedColumnSetInOneRow(t *testing.T) {
	// id is defaulted; explicitly set in one row, so it is kept for both.
	insert := sqlkit.Insert(Users).Values(
		User{Email: "a@example.com"},
		User{ID: 5, Email: "b@example.com"},
	)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3), ($4, $5, $6)`,
		[]any{int64(0), "a@example.com", (*string)(nil), int64(5), "b@example.com", (*string)(nil)})
}

func TestInsertValuesRejectsBadRows(t *testing.T) {
	cases := []struct {
		name    string
		insert  sqlkit.InsertQuery
		wantErr string
	}{
		{"nil pointer", sqlkit.Insert(Users).Values((*User)(nil)), "nil pointer"},
		{"non-struct", sqlkit.Insert(Users).Values(42), "must be structs"},
		{"no mapped fields", sqlkit.Insert(Users).Values(struct{ Unrelated int }{1}), "no fields"},
		{"columns with values", sqlkit.Insert(Users).Columns(Users.Email).Values(User{Email: "x"}), "Columns applies to FromSelect"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := c.insert.ToSQL(pg)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ToSQL() error = %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestInsertFromSelectUnwrapsUnionQuery(t *testing.T) {
	union := sqlkit.Select(Users.Email).From(Users).Where(Users.ID.Eq(1)).
		UnionAll(sqlkit.Select(Users.Email).From(Users).Where(Users.ID.Eq(2)))

	insert := sqlkit.Insert(Users).Columns(Users.Email).FromSelect(union)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") (SELECT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."id" = $1)) UNION ALL (SELECT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."id" = $2))`,
		[]any{int64(1), int64(2)})
}

func TestInsertFromSelectAcceptsRawSQLQuery(t *testing.T) {
	source := sql.Select(sql.Col("email")).From(sql.Table{Name: "old_users"})

	insert := sqlkit.Insert(Users).Columns(Users.Email).FromSelect(source)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old_users"`,
		nil)
}

func TestInsertFromSelectReturningScan(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}, {int64(2)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ids []int64
	err := db.Insert(Users).Columns(Users.Email).
		FromSelect(db.Select(Users.Email).From(Users)).
		Returning(Users.ID).
		All(context.Background()).Scan(&ids)
	if err != nil {
		t.Fatalf("FromSelect Returning Scan error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %#v", ids)
	}
	want := `INSERT INTO "public"."users" ("email") SELECT "public"."users"."email" FROM "public"."users" RETURNING "public"."users"."id"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("SQL = %q,\nwant %q", got, want)
	}
}

func TestInsertExecThroughDatabase(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Insert(Users).Values(User{Email: "a@example.com"}).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 || !strings.HasPrefix(execs[0].SQL, "INSERT INTO") {
		t.Fatalf("execs = %#v, want one INSERT", execs)
	}
	if strings.Contains(execs[0].SQL, "RETURNING") {
		t.Fatalf("Exec without Returning produced RETURNING: %q", execs[0].SQL)
	}
}

func TestInsertBatchSizeSplitsExec(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	users := []User{
		{Email: "a@example.com"},
		{Email: "b@example.com"},
		{Email: "c@example.com"},
		{Email: "d@example.com"},
		{Email: "e@example.com"},
	}
	res, err := db.Insert(Users).Values(users).BatchSize(2).Exec(context.Background())
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	// 5 rows in batches of 2 → 3 statements (2, 2, 1).
	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("execs = %d, want 3", len(execs))
	}
	if n := strings.Count(execs[0].SQL, "VALUES"); n != 1 {
		t.Fatalf("execs[0] = %q", execs[0].SQL)
	}
	// Users maps to two columns (email, name), so a 2-row batch carries 4 args
	// and the trailing 1-row batch carries 2.
	if len(execs[0].Args) != 4 || len(execs[2].Args) != 2 {
		t.Fatalf("batch arg counts = %d, %d; want 4 and 2", len(execs[0].Args), len(execs[2].Args))
	}

	// RowsAffected sums the per-batch results (the fake reports 1 per Exec).
	if n, err := res.RowsAffected(); err != nil || n != 3 {
		t.Fatalf("RowsAffected() = %d, %v; want 3, nil", n, err)
	}
}

func TestInsertBatchSizeSingleStatementWhenNotExceeded(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	users := []User{{Email: "a@example.com"}, {Email: "b@example.com"}}
	// batchSize >= rows, and batchSize <= 0, both run as a single statement.
	for _, size := range []int{2, 5, 0, -1} {
		fake = &sqltest.Fake{}
		db = sqlkit.WrapDB(fake.DB())
		if _, err := db.Insert(Users).Values(users).BatchSize(size).Exec(context.Background()); err != nil {
			t.Fatalf("BatchSize(%d) Exec() error = %v", size, err)
		}
		if n := len(fake.Execs()); n != 1 {
			t.Fatalf("BatchSize(%d) execs = %d, want 1", size, n)
		}
	}
}

func TestInsertBatchSizeKeepsColumnSetAcrossBatches(t *testing.T) {
	// id is defaulted. Only a later batch carries a non-zero id, so the whole
	// insert must include id in every batch (binding explicit zeroes) — the
	// column set is derived from all rows, not per chunk.
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	_, err := db.Insert(Users).Values(
		User{Email: "a@example.com"}, // id zero
		User{Email: "b@example.com"}, // id zero
		User{ID: 9, Email: "c@example.com"},
	).BatchSize(2).Exec(context.Background())
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	execs := fake.Execs()
	if len(execs) != 2 {
		t.Fatalf("execs = %d, want 2", len(execs))
	}
	// Every batch includes the id column, so the first 2-row batch binds 6 args
	// (id, email, name x2) and the trailing 1-row batch binds 3.
	for i, want := range []int{6, 3} {
		if got := len(execs[i].Args); got != want {
			t.Fatalf("execs[%d] args = %d, want %d; SQL = %q", i, got, want, execs[i].SQL)
		}
		if !strings.Contains(execs[i].SQL, `"id"`) {
			t.Fatalf("execs[%d] omitted id: %q", i, execs[i].SQL)
		}
	}
}

func TestInsertBatchSizeIgnoredByToSQL(t *testing.T) {
	// ToSQL always compiles the full statement regardless of BatchSize.
	insert := sqlkit.Insert(Users).Values(
		User{Email: "a@example.com"},
		User{Email: "b@example.com"},
	).BatchSize(1)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", (*string)(nil), "b@example.com", (*string)(nil)})
}

func TestBuildErrorsSurfaceAtScanWithoutQuerying(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	var out []User
	err := db.Insert(Users).Returning().All(context.Background()).Scan(&out)
	if err == nil || !strings.Contains(err.Error(), "requires Values or FromSelect") {
		t.Fatalf("Scan() error = %v, want build error", err)
	}
	if n := len(fake.Queries()) + len(fake.Execs()); n != 0 {
		t.Fatalf("build error still reached the database: %d statements", n)
	}
}

func TestBuildErrorsSurfaceAtExecWithoutQuerying(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Insert(Users).Exec(context.Background()); err == nil {
		t.Fatal("Exec() on empty INSERT succeeded, want error")
	}
	if n := len(fake.Queries()) + len(fake.Execs()); n != 0 {
		t.Fatalf("build error still reached the database: %d statements", n)
	}
}

func TestTypedOnConflictDoUpdate(t *testing.T) {
	name := "Alice"
	query := sqlkit.Insert(Users).
		Values(User{ID: 1, Email: "a@example.com", Name: &name}).
		OnConflict(sqlkit.OnColumns(Users.ID), sqlkit.DoUpdate(Users.Email.SetExpr(Users.Email.Excluded()))).
		Returning(Users.ID)
	assertToSQL(t, query,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "email" = excluded."email" RETURNING "public"."users"."id"`,
		[]any{int64(1), "a@example.com", &name})
}

func TestTypedOnConflictDoNothing(t *testing.T) {
	name := "Alice"
	query := sqlkit.Insert(Users).
		Values(User{ID: 1, Email: "a@example.com", Name: &name}).
		OnConflict(sqlkit.OnColumns(Users.ID), sqlkit.DoNothing())
	assertToSQL(t, query,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ("id") DO NOTHING`,
		[]any{int64(1), "a@example.com", &name})
}

func TestTypedOnConflictOnConstraint(t *testing.T) {
	name := "Alice"
	query := sqlkit.Insert(Users).
		Values(User{ID: 1, Email: "a@example.com", Name: &name}).
		OnConflict(sqlkit.OnConstraint("users_email_key"), sqlkit.DoUpdate(Users.Email.SetExpr(Users.Email.Excluded())))
	assertToSQL(t, query,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ON CONSTRAINT "users_email_key" DO UPDATE SET "email" = excluded."email"`,
		[]any{int64(1), "a@example.com", &name})
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

func TestInsertValuesPassesValuerFields(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(struct {
		Email upperString
		Name  stdsql.NullString
	}{
		Email: upperString{value: "X@EXAMPLE.COM"},
		Name:  stdsql.NullString{}, // invalid -> NULL via Valuer
	})

	sqlText, args, err := insert.ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2)`
	if sqlText != want {
		t.Fatalf("SQL = %q, want %q", sqlText, want)
	}
	// database/sql converts the args via driver.Valuer at execution time;
	// verify what the driver actually receives.
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	q := db.Insert(Users).Values(struct {
		Email upperString
		Name  stdsql.NullString
	}{Email: upperString{value: "X@EXAMPLE.COM"}})
	if _, err := q.Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	got := fake.Execs()[0].Args
	if got[0] != "x@example.com" || got[1] != nil {
		t.Fatalf("driver args = %#v, want lowered email and NULL name", got)
	}
	_ = args
}
