package sql

import (
	"strings"
	"testing"
)

func TestPostgreSQLDialectQuoteIdent(t *testing.T) {
	dialect := PostgreSQLDialect{}
	if got, want := dialect.QuoteIdent(`public.user"profiles`), `"public"."user""profiles"`; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
}

func TestPostgreSQLDialectPlaceholder(t *testing.T) {
	dialect := PostgreSQLDialect{}
	for position, want := range []string{"$1", "$2", "$3"} {
		if got := dialect.Placeholder(position + 1); got != want {
			t.Fatalf("Placeholder(%d) = %q, want %q", position+1, got, want)
		}
	}
}

func TestMySQLDialectQuoteIdent(t *testing.T) {
	dialect := MySQLDialect{}
	if got, want := dialect.QuoteIdent("blog.user`profiles"), "`blog`.`user``profiles`"; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
}

func TestMySQLDialectPlaceholder(t *testing.T) {
	dialect := MySQLDialect{}
	for position := 1; position <= 3; position++ {
		if got := dialect.Placeholder(position); got != "?" {
			t.Fatalf("Placeholder(%d) = %q, want ?", position, got)
		}
	}
}

func TestDialectNamesAndCapabilities(t *testing.T) {
	if (PostgreSQLDialect{}).Name() != "postgres" || !(PostgreSQLDialect{}).SupportsReturning() {
		t.Fatal("postgres dialect metadata wrong")
	}
	if (MySQLDialect{}).Name() != "mysql" || (MySQLDialect{}).SupportsReturning() {
		t.Fatal("mysql dialect metadata wrong")
	}
	if (StandardSQLDialect{}).Name() != "standard" || (StandardSQLDialect{}).SupportsReturning() {
		t.Fatal("standard dialect metadata wrong")
	}
	for _, d := range []BindParamLimiter{PostgreSQLDialect{}, MySQLDialect{}} {
		if got := d.MaxBindParams(); got != 65535 {
			t.Fatalf("MaxBindParams() = %d, want 65535", got)
		}
	}
}

func TestStandardDialectQuoteAndPlaceholder(t *testing.T) {
	d := StandardSQLDialect{}
	if got, want := d.QuoteIdent(`s.t`), `"s"."t"`; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
	if got := d.Placeholder(2); got != "?" {
		t.Fatalf("Placeholder(2) = %q, want ?", got)
	}
}

func TestDialectByNameStandard(t *testing.T) {
	for _, name := range []string{"standard", "ansi", "sql"} {
		d, err := DialectByName(name)
		if err != nil || d.Name() != "standard" {
			t.Fatalf("DialectByName(%q) = %v, %v", name, d, err)
		}
	}
}

func funcSQL(t *testing.T, expr Selection, d Dialect) string {
	t.Helper()
	got, _, err := Select(expr).ToSQL(d)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	return got
}

func TestFuncRemapNow(t *testing.T) {
	// PostgreSQL/MySQL have no FuncRenderer: NOW() passes through unchanged.
	if got, want := funcSQL(t, Now(), PostgreSQLDialect{}), "SELECT NOW()"; got != want {
		t.Errorf("postgres NOW = %q, want %q", got, want)
	}
	if got, want := funcSQL(t, Now(), MySQLDialect{}), "SELECT NOW()"; got != want {
		t.Errorf("mysql NOW = %q, want %q", got, want)
	}
	// Standard remaps NOW to CURRENT_TIMESTAMP.
	if got, want := funcSQL(t, Now(), StandardSQLDialect{}), "SELECT CURRENT_TIMESTAMP"; got != want {
		t.Errorf("standard NOW = %q, want %q", got, want)
	}
}

func TestFuncRemapLength(t *testing.T) {
	// CHAR_LENGTH passes through on postgres; standard keeps the standard
	// CHAR_LENGTH spelling.
	if got, want := funcSQL(t, CharLength(Col("name")), PostgreSQLDialect{}), `SELECT CHAR_LENGTH("name")`; got != want {
		t.Errorf("postgres CHAR_LENGTH = %q, want %q", got, want)
	}
	if got, want := funcSQL(t, CharLength(Col("name")), StandardSQLDialect{}), `SELECT CHAR_LENGTH("name")`; got != want {
		t.Errorf("standard CHAR_LENGTH = %q, want %q", got, want)
	}
}

func TestFuncRemapUnknownPassThrough(t *testing.T) {
	// A function the dialect does not remap passes through verbatim everywhere.
	for _, d := range []Dialect{PostgreSQLDialect{}, MySQLDialect{}, StandardSQLDialect{}} {
		got, _, err := Select(Func("my_custom_fn", Col("x"))).ToSQL(d)
		if err != nil {
			t.Fatalf("ToSQL(%s) error = %v", d.Name(), err)
		}
		quoted := `"x"`
		if d.Name() == "mysql" {
			quoted = "`x`"
		}
		if want := "SELECT my_custom_fn(" + quoted + ")"; got != want {
			t.Errorf("%s custom fn = %q, want %q", d.Name(), got, want)
		}
	}
}

func TestFuncRemapCaseInsensitiveName(t *testing.T) {
	// Canonical names are matched case-insensitively.
	if got, want := funcSQL(t, Func("now"), StandardSQLDialect{}), "SELECT CURRENT_TIMESTAMP"; got != want {
		t.Errorf("standard now = %q, want %q", got, want)
	}
}

func TestMySQLSelect(t *testing.T) {
	query := Select(Col("users.id"), Col("users.email")).
		From(Table{Name: NewIdent("users")}).
		Where(And(Eq(Col("users.id"), Int(7)), Like(Col("users.email"), Val("%@x.com")))).
		OrderBy(Desc(Col("users.id"))).
		Limit(10)

	assertMySQL(t, query,
		"SELECT `users`.`id`, `users`.`email` FROM `users` WHERE ((`users`.`id` = ?) AND (`users`.`email` LIKE ?)) ORDER BY `users`.`id` DESC LIMIT 10",
		[]any{7, "%@x.com"})
}

func TestMySQLJSONObject(t *testing.T) {
	// MySQL uses a flat key, value list (no VALUE separator).
	assertMySQL(t,
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: NewIdent("users")}),
		"SELECT JSON_OBJECT(?, `id`, ?, `name`) AS `j` FROM `users`", []any{"id", "name"})

	assertMySQL(t,
		Select(As(JSONObjectAgg(Col("k"), Col("v")), "obj")).From(Table{Name: NewIdent("kv")}),
		"SELECT JSON_OBJECTAGG(`k`, `v`) AS `obj` FROM `kv`", nil)
}

func TestMySQLConcat(t *testing.T) {
	// MySQL's || is logical OR, so concatenation compiles to CONCAT(...).
	assertMySQL(t,
		Select(As(Concat(Col("first"), Val(" "), Col("last")), "name")).From(Table{Name: NewIdent("users")}),
		"SELECT CONCAT(`first`, ?, `last`) AS `name` FROM `users`", []any{" "})
}

func TestMySQLILike(t *testing.T) {
	// MySQL has no ILIKE: it compiles to the portable LOWER ... LIKE LOWER form.
	assertMySQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(ILike(Col("email"), Val("%@EXAMPLE.com"))),
		"SELECT `id` FROM `users` WHERE (LOWER(`email`) LIKE LOWER(?))", []any{"%@EXAMPLE.com"})
}

func TestMySQLStringAgg(t *testing.T) {
	// MySQL spells STRING_AGG as GROUP_CONCAT with a SEPARATOR keyword.
	assertMySQL(t,
		Select(As(StringAgg(Col("name"), ", "), "names")).From(Table{Name: NewIdent("users")}).GroupBy(Col("org_id")),
		"SELECT GROUP_CONCAT(`name` SEPARATOR ', ') AS `names` FROM `users` GROUP BY `org_id`", nil)
}

func TestMySQLScalarFunctions(t *testing.T) {
	// The scalar string/numeric/date functions and the EXTRACT and POSITION
	// special forms share one rendering across dialects (only the identifier
	// quoting and placeholders differ).
	assertMySQL(t,
		Select(
			Upper(Col("name")),
			Round(Col("rate"), 2),
			As(CurrentDate(), "today"),
			As(Extract("year", Col("created_at")), "yr"),
			As(Position("@", Col("email")), "at"),
		).From(Table{Name: NewIdent("t")}),
		"SELECT UPPER(`name`), ROUND(`rate`, ?), CURRENT_DATE AS `today`, EXTRACT(year FROM `created_at`) AS `yr`, POSITION(? IN `email`) AS `at` FROM `t`",
		[]any{2, "@"})
}

func TestMySQLJSONAccess(t *testing.T) {
	assertMySQL(t,
		Select(As(JSONGet(Col("data"), "name"), "n")).From(Table{Name: NewIdent("events")}),
		"SELECT JSON_EXTRACT(`data`, '$.name') AS `n` FROM `events`", nil)

	assertMySQL(t,
		Select(As(JSONGetText(Col("data"), "items", 0, "id"), "id")).From(Table{Name: NewIdent("events")}),
		"SELECT JSON_UNQUOTE(JSON_EXTRACT(`data`, '$.items[0].id')) AS `id` FROM `events`", nil)

	// A key needing quoting in the path.
	assertMySQL(t,
		Select(JSONGet(Col("data"), "first name")).From(Table{Name: NewIdent("events")}),
		"SELECT JSON_EXTRACT(`data`, '$.\"first name\"') FROM `events`", nil)
}

func TestMySQLInsertUpdateDelete(t *testing.T) {
	insert := InsertInto(Table{Name: NewIdent("users")}).Columns("email").Values("a@x.com")
	assertMySQL(t, insert, "INSERT INTO `users` (`email`) VALUES (?)", []any{"a@x.com"})

	update := Update(Table{Name: NewIdent("users")}).Set(Set("email", "b@x.com")).Where(Eq(Col("id"), Int(1)))
	assertMySQL(t, update, "UPDATE `users` SET `email` = ? WHERE (`id` = ?)", []any{"b@x.com", 1})

	del := DeleteFrom(Table{Name: NewIdent("users")}).Where(Eq(Col("id"), Int(1)))
	assertMySQL(t, del, "DELETE FROM `users` WHERE (`id` = ?)", []any{1})
}

func TestMySQLOnDuplicateKeyUpdate(t *testing.T) {
	// MySQL ignores the conflict target and uses VALUES(col) for the new row.
	insert := InsertInto(Table{Name: NewIdent("users")}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict("email").DoUpdate(Set("name", Excluded("name")))
	assertMySQL(t, insert,
		"INSERT INTO `users` (`email`, `name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)",
		[]any{"a@x.com", "Alice"})
}

func TestMySQLRejectsDoNothingAndConflictWhere(t *testing.T) {
	doNothing := InsertInto(Table{Name: NewIdent("users")}).Columns("email").Values("a@x.com").
		OnConflict("email").DoNothing()
	if _, _, err := doNothing.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "DO NOTHING") {
		t.Fatalf("ToSQL() error = %v, want DO NOTHING rejection", err)
	}

	withWhere := InsertInto(Table{Name: NewIdent("users")}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict("email").DoUpdate(Set("name", Excluded("name"))).
		Where(Ne(Col("name"), Val("locked")))
	if _, _, err := withWhere.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "WHERE") {
		t.Fatalf("ToSQL() error = %v, want WHERE rejection", err)
	}
}

func TestMySQLRejectsReturning(t *testing.T) {
	insert := InsertInto(Table{Name: NewIdent("users")}).Columns("email").Values("a@x.com").Returning(Col("id"))
	_, _, err := insert.ToSQL(MySQLDialect{})
	if err == nil || !strings.Contains(err.Error(), "does not support RETURNING") {
		t.Fatalf("ToSQL() error = %v, want RETURNING rejection", err)
	}
	if !strings.Contains(err.Error(), "LastInsertId") {
		t.Fatalf("error should point at LastInsertId: %v", err)
	}
}

func TestStandardSelect(t *testing.T) {
	query := Select(Col("users.id"), Col("users.email")).
		From(Table{Name: NewIdent("users")}).
		Where(Eq(Col("users.id"), Int(7))).
		Limit(10)

	assertStandard(t, query,
		`SELECT "users"."id", "users"."email" FROM "users" WHERE ("users"."id" = ?) LIMIT 10`,
		[]any{7})
}

func TestStandardConcat(t *testing.T) {
	// The standard || operator concatenates.
	assertStandard(t,
		Select(As(Concat(Col("first"), Val(" "), Col("last")), "name")).From(Table{Name: NewIdent("users")}),
		`SELECT ("first" || ? || "last") AS "name" FROM "users"`, []any{" "})
}

func TestStandardJSONAccess(t *testing.T) {
	// SQL/JSON standard JSON_QUERY (JSON) and JSON_VALUE (text).
	assertStandard(t,
		Select(As(JSONGet(Col("data"), "address", "city"), "c")).From(Table{Name: NewIdent("events")}),
		`SELECT JSON_QUERY("data", '$.address.city') AS "c" FROM "events"`, nil)

	assertStandard(t,
		Select(As(JSONGetText(Col("data"), "items", 0, "id"), "id")).From(Table{Name: NewIdent("events")}),
		`SELECT JSON_VALUE("data", '$.items[0].id') AS "id" FROM "events"`, nil)
}

func TestStandardJSONObject(t *testing.T) {
	// The SQL/JSON standard uses the "key VALUE value" separator.
	assertStandard(t,
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: NewIdent("users")}),
		`SELECT JSON_OBJECT(? VALUE "id", ? VALUE "name") AS "j" FROM "users"`, []any{"id", "name"})
}

func TestStandardRejectsReturning(t *testing.T) {
	insert := InsertInto(Table{Name: NewIdent("users")}).Columns("email").Values("a@x.com").Returning(Col("id"))
	_, _, err := insert.ToSQL(StandardSQLDialect{})
	if err == nil || !strings.Contains(err.Error(), "does not support RETURNING") {
		t.Fatalf("ToSQL() error = %v, want RETURNING rejection", err)
	}
}

func TestStandardRejectsUpsert(t *testing.T) {
	insert := InsertInto(Table{Name: NewIdent("users")}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict("email").DoUpdate(Set("name", Excluded("name")))
	_, _, err := insert.ToSQL(StandardSQLDialect{})
	if err == nil || !strings.Contains(err.Error(), "upsert") {
		t.Fatalf("ToSQL() error = %v, want upsert rejection", err)
	}
}

func TestStandardCreateTableIdentity(t *testing.T) {
	stmt := CreateTable(Table{Name: NewIdent("items")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "bigint"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: NewIdent("name"), Type: EngineType{Name: "varchar", Length: 255}, NotNull: true}).
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

func TestDialectHelpersCoverage(t *testing.T) {
	if _, err := DialectByName("postgres"); err != nil {
		t.Fatalf("DialectByName(postgres): %v", err)
	}
	if _, err := DialectByName("nope-dialect"); err == nil {
		t.Fatal("DialectByName(unknown) = nil error, want error")
	}

	pg := PostgreSQLDialect{}
	if pg.TableCommentStatement("\"t\"", "") != "" {
		t.Fatal("empty table comment should render nothing")
	}
	if pg.TableCommentStatement("\"t\"", "hi") == "" {
		t.Fatal("non-empty table comment should render")
	}
	if pg.ColumnCommentStatement("\"t\"", "\"c\"", "") != "" {
		t.Fatal("empty column comment should render nothing")
	}
	if pg.ColumnCommentStatement("\"t\"", "\"c\"", "hi") == "" {
		t.Fatal("non-empty column comment should render")
	}

	// AutoIncrementColumn: integer types auto-increment, others do not.
	if _, ok := pg.AutoIncrementColumn("int8", 0, 0, 0); !ok {
		t.Fatal("pg int8 should auto-increment")
	}
	if _, ok := pg.AutoIncrementColumn("text", 0, 0, 0); ok {
		t.Fatal("pg text should not auto-increment")
	}
	my := MySQLDialect{}
	if _, ok := my.AutoIncrementColumn("int", 0, 0, 0); !ok {
		t.Fatal("mysql int should auto-increment")
	}
	if _, ok := my.AutoIncrementColumn("text", 0, 0, 0); ok {
		t.Fatal("mysql text should not auto-increment")
	}

	if !(StandardSQLDialect{}).SupportsMerge() {
		t.Fatal("standard dialect should support MERGE")
	}

	// ValuesCastType over a range of value kinds.
	for _, v := range []any{int(1), int64(2), float64(1.5), "s", true, []byte("b"), nil} {
		_, _ = pg.ValuesCastType(v)
	}
}
