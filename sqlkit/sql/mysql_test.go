package sql_test

import (
	"strings"
	"testing"

	. "github.com/sqlkit-dev/sqlkit/sqlkit/sql"
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

func TestMySQLInsertUpdateDelete(t *testing.T) {
	insert := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x.com")
	assertMySQL(t, insert, "INSERT INTO `users` (`email`) VALUES (?)", []any{"a@x.com"})

	update := Update(Table{Name: "users"}).Set(Set("email", "b@x.com")).Where(Eq(Col("id"), Int(1)))
	assertMySQL(t, update, "UPDATE `users` SET `email` = ? WHERE (`id` = ?)", []any{"b@x.com", 1})

	del := DeleteFrom(Table{Name: "users"}).Where(Eq(Col("id"), Int(1)))
	assertMySQL(t, del, "DELETE FROM `users` WHERE (`id` = ?)", []any{1})
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
