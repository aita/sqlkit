package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func assertSQLite(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(SQLiteDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q,\nwant %q", sqlText, wantSQL)
	}
	if len(wantArgs) > 0 && len(args) != len(wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

func TestSQLiteSelect(t *testing.T) {
	query := Select(Col("users.id"), Col("users.email")).
		From(Table{Name: "users"}).
		Where(And(Eq(Col("users.id"), Int(7)), Like(Col("users.email"), Val("%@x.com")))).
		OrderBy(Desc(Col("users.id"))).
		Limit(10)

	assertSQLite(t, query,
		`SELECT "users"."id", "users"."email" FROM "users" WHERE (("users"."id" = ?) AND ("users"."email" LIKE ?)) ORDER BY "users"."id" DESC LIMIT 10`,
		[]any{7, "%@x.com"})
}

func TestSQLiteJSONAccess(t *testing.T) {
	// SQLite uses the PostgreSQL-style -> / ->> operators (>= 3.38).
	assertSQLite(t,
		Select(As(JSONGet(Col("data"), "name"), "n")).From(Table{Name: "events"}),
		`SELECT ("data" -> 'name') AS "n" FROM "events"`, nil)

	assertSQLite(t,
		Select(As(JSONGetText(Col("data"), "items", 0, "id"), "id")).From(Table{Name: "events"}),
		`SELECT ((("data" -> 'items') -> 0) ->> 'id') AS "id" FROM "events"`, nil)
}

func TestSQLiteJSONObject(t *testing.T) {
	// SQLite uses a flat json_object(key, value, ...) list (lowercase, no VALUE).
	assertSQLite(t,
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: "users"}),
		`SELECT json_object(?, "id", ?, "name") AS "j" FROM "users"`, []any{"id", "name"})

	// The aggregate is json_group_object, not the standard JSON_OBJECTAGG.
	assertSQLite(t,
		Select(As(JSONObjectAgg(Col("k"), Col("v")), "obj")).From(Table{Name: "kv"}),
		`SELECT json_group_object("k", "v") AS "obj" FROM "kv"`, nil)
}

func TestSQLiteILike(t *testing.T) {
	// SQLite has no ILIKE operator: it falls back to the portable LOWER form.
	assertSQLite(t,
		Select(Col("id")).From(Table{Name: "users"}).Where(ILike(Col("email"), Val("%@EXAMPLE.com"))),
		`SELECT "id" FROM "users" WHERE (LOWER("email") LIKE LOWER(?))`, []any{"%@EXAMPLE.com"})
}

func TestSQLiteUpsert(t *testing.T) {
	// SQLite uses the PostgreSQL-style ON CONFLICT ... excluded.<col> syntax.
	insert := InsertInto(Table{Name: "users"}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict("email").DoUpdate(Set("name", Excluded("name")))
	assertSQLite(t, insert,
		`INSERT INTO "users" ("email", "name") VALUES (?, ?) ON CONFLICT ("email") DO UPDATE SET "name" = excluded."name"`,
		[]any{"a@x.com", "Alice"})

	doNothing := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com").
		OnConflict("email").DoNothing()
	assertSQLite(t, doNothing,
		`INSERT INTO "users" ("email") VALUES (?) ON CONFLICT ("email") DO NOTHING`,
		[]any{"a@x.com"})
}

func TestSQLiteReturning(t *testing.T) {
	// SQLite >= 3.35 supports RETURNING.
	insert := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com").Returning(Col("id"))
	assertSQLite(t, insert,
		`INSERT INTO "users" ("email") VALUES (?) RETURNING "id"`, []any{"a@x.com"})
}

func TestSQLiteCreateTableAutoIncrement(t *testing.T) {
	// INTEGER PRIMARY KEY AUTOINCREMENT declares the primary key inline, so no
	// separate PRIMARY KEY constraint is emitted.
	stmt := CreateTable(Table{Name: "items"}).
		Column(ColumnDef{Name: "id", Type: EngineType{Name: "integer"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: "name", Type: EngineType{Name: "text"}}).
		PrimaryKey("id")
	got := mustDDL(t, stmt, "sqlite")
	want := "CREATE TABLE \"items\" (\n" +
		"    \"id\" INTEGER PRIMARY KEY AUTOINCREMENT,\n" +
		"    \"name\" text\n" +
		");"
	if got != want {
		t.Errorf("CreateTable(sqlite) =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "PRIMARY KEY (") {
		t.Errorf("CreateTable(sqlite) emitted a duplicate standalone PRIMARY KEY:\n%s", got)
	}
}
