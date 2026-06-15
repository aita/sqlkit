package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func assertStandard(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(StandardSQLDialect{})
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

func TestStandardSelect(t *testing.T) {
	query := Select(Col("users.id"), Col("users.email")).
		From(Table{Name: "users"}).
		Where(Eq(Col("users.id"), Int(7))).
		Limit(10)

	assertStandard(t, query,
		`SELECT "users"."id", "users"."email" FROM "users" WHERE ("users"."id" = ?) LIMIT 10`,
		[]any{7})
}

func TestStandardConcat(t *testing.T) {
	// The standard || operator concatenates.
	assertStandard(t,
		Select(As(Concat(Col("first"), Val(" "), Col("last")), "name")).From(Table{Name: "users"}),
		`SELECT ("first" || ? || "last") AS "name" FROM "users"`, []any{" "})
}

func TestStandardJSONAccess(t *testing.T) {
	// SQL/JSON standard JSON_QUERY (JSON) and JSON_VALUE (text).
	assertStandard(t,
		Select(As(JSONGet(Col("data"), "address", "city"), "c")).From(Table{Name: "events"}),
		`SELECT JSON_QUERY("data", '$.address.city') AS "c" FROM "events"`, nil)

	assertStandard(t,
		Select(As(JSONGetText(Col("data"), "items", 0, "id"), "id")).From(Table{Name: "events"}),
		`SELECT JSON_VALUE("data", '$.items[0].id') AS "id" FROM "events"`, nil)
}

func TestStandardJSONObject(t *testing.T) {
	// The SQL/JSON standard uses the "key VALUE value" separator.
	assertStandard(t,
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: "users"}),
		`SELECT JSON_OBJECT(? VALUE "id", ? VALUE "name") AS "j" FROM "users"`, []any{"id", "name"})
}

func TestStandardRejectsReturning(t *testing.T) {
	insert := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com").Returning(Col("id"))
	_, _, err := insert.ToSQL(StandardSQLDialect{})
	if err == nil || !strings.Contains(err.Error(), "does not support RETURNING") {
		t.Fatalf("ToSQL() error = %v, want RETURNING rejection", err)
	}
}

func TestStandardRejectsUpsert(t *testing.T) {
	insert := InsertInto(Table{Name: "users"}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict("email").DoUpdate(Set("name", Excluded("name")))
	_, _, err := insert.ToSQL(StandardSQLDialect{})
	if err == nil || !strings.Contains(err.Error(), "upsert") {
		t.Fatalf("ToSQL() error = %v, want upsert rejection", err)
	}
}

func TestStandardCreateTableIdentity(t *testing.T) {
	stmt := CreateTable(Table{Name: "items"}).
		Column(ColumnDef{Name: "id", Type: EngineType{Name: "bigint"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: "name", Type: EngineType{Name: "varchar", Length: 255}, NotNull: true}).
		PrimaryKey("id")
	got := mustDDL(t, stmt, "standard")
	want := "CREATE TABLE \"items\" (\n" +
		"    \"id\" bigint GENERATED ALWAYS AS IDENTITY,\n" +
		"    \"name\" varchar(255) NOT NULL,\n" +
		"    PRIMARY KEY (\"id\")\n" +
		");"
	if got != want {
		t.Errorf("CreateTable(standard) =\n%s\nwant\n%s", got, want)
	}
}
