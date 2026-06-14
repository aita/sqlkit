package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func assertMySQL(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(MySQLDialect{})
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

func TestMySQLSelect(t *testing.T) {
	query := Select(Col("users.id"), Col("users.email")).
		From(Table{Name: "users"}).
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
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: "users"}),
		"SELECT JSON_OBJECT(?, `id`, ?, `name`) AS `j` FROM `users`", []any{"id", "name"})

	assertMySQL(t,
		Select(As(JSONObjectAgg(Col("k"), Col("v")), "obj")).From(Table{Name: "kv"}),
		"SELECT JSON_OBJECTAGG(`k`, `v`) AS `obj` FROM `kv`", nil)
}

func TestMySQLConcat(t *testing.T) {
	// MySQL's || is logical OR, so concatenation compiles to CONCAT(...).
	assertMySQL(t,
		Select(As(Concat(Col("first"), Val(" "), Col("last")), "name")).From(Table{Name: "users"}),
		"SELECT CONCAT(`first`, ?, `last`) AS `name` FROM `users`", []any{" "})
}

func TestMySQLILike(t *testing.T) {
	// MySQL has no ILIKE: it compiles to the portable LOWER ... LIKE LOWER form.
	assertMySQL(t,
		Select(Col("id")).From(Table{Name: "users"}).Where(ILike(Col("email"), Val("%@EXAMPLE.com"))),
		"SELECT `id` FROM `users` WHERE (LOWER(`email`) LIKE LOWER(?))", []any{"%@EXAMPLE.com"})
}

func TestMySQLStringAgg(t *testing.T) {
	// MySQL spells STRING_AGG as GROUP_CONCAT with a SEPARATOR keyword.
	assertMySQL(t,
		Select(As(StringAgg(Col("name"), ", "), "names")).From(Table{Name: "users"}).GroupBy(Col("org_id")),
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
		).From(Table{Name: "t"}),
		"SELECT UPPER(`name`), ROUND(`rate`, ?), CURRENT_DATE AS `today`, EXTRACT(year FROM `created_at`) AS `yr`, POSITION(? IN `email`) AS `at` FROM `t`",
		[]any{2, "@"})
}

func TestMySQLJSONAccess(t *testing.T) {
	assertMySQL(t,
		Select(As(JSONGet(Col("data"), "name"), "n")).From(Table{Name: "events"}),
		"SELECT JSON_EXTRACT(`data`, '$.name') AS `n` FROM `events`", nil)

	assertMySQL(t,
		Select(As(JSONGetText(Col("data"), "items", 0, "id"), "id")).From(Table{Name: "events"}),
		"SELECT JSON_UNQUOTE(JSON_EXTRACT(`data`, '$.items[0].id')) AS `id` FROM `events`", nil)

	// A key needing quoting in the path.
	assertMySQL(t,
		Select(JSONGet(Col("data"), "first name")).From(Table{Name: "events"}),
		"SELECT JSON_EXTRACT(`data`, '$.\"first name\"') FROM `events`", nil)
}

func TestMySQLInsertUpdateDelete(t *testing.T) {
	insert := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com")
	assertMySQL(t, insert, "INSERT INTO `users` (`email`) VALUES (?)", []any{"a@x.com"})

	update := Update(Table{Name: "users"}).Set(Set("email", "b@x.com")).Where(Eq(Col("id"), Int(1)))
	assertMySQL(t, update, "UPDATE `users` SET `email` = ? WHERE (`id` = ?)", []any{"b@x.com", 1})

	del := DeleteFrom(Table{Name: "users"}).Where(Eq(Col("id"), Int(1)))
	assertMySQL(t, del, "DELETE FROM `users` WHERE (`id` = ?)", []any{1})
}

func TestMySQLOnDuplicateKeyUpdate(t *testing.T) {
	// MySQL ignores the conflict target and uses VALUES(col) for the new row.
	insert := InsertInto(Table{Name: "users"}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict(OnColumns("email"), DoUpdate(Set("name", Excluded("name"))))
	assertMySQL(t, insert,
		"INSERT INTO `users` (`email`, `name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)",
		[]any{"a@x.com", "Alice"})
}

func TestMySQLRejectsDoNothingAndConflictWhere(t *testing.T) {
	doNothing := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com").
		OnConflict(OnColumns("email"), DoNothing())
	if _, _, err := doNothing.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "DO NOTHING") {
		t.Fatalf("ToSQL() error = %v, want DO NOTHING rejection", err)
	}

	withWhere := InsertInto(Table{Name: "users"}).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict(OnColumns("email"), DoUpdate(Set("name", Excluded("name"))),
			ConflictWhere(Ne(Col("name"), Val("locked"))))
	if _, _, err := withWhere.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "WHERE") {
		t.Fatalf("ToSQL() error = %v, want WHERE rejection", err)
	}
}

func TestMySQLRejectsReturning(t *testing.T) {
	insert := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com").Returning(Col("id"))
	_, _, err := insert.ToSQL(MySQLDialect{})
	if err == nil || !strings.Contains(err.Error(), "does not support RETURNING") {
		t.Fatalf("ToSQL() error = %v, want RETURNING rejection", err)
	}
	if !strings.Contains(err.Error(), "LastInsertId") {
		t.Fatalf("error should point at LastInsertId: %v", err)
	}
}
