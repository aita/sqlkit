package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestRegexpPostgreSQL(t *testing.T) {
	cases := []struct {
		expr Expression
		want string
	}{
		{Regexp(Col("name"), Val("^a")), `("name" ~ $1)`},
		{NotRegexp(Col("name"), Val("^a")), `("name" !~ $1)`},
		{IRegexp(Col("name"), Val("^a")), `("name" ~* $1)`},
		{NotIRegexp(Col("name"), Val("^a")), `("name" !~* $1)`},
	}
	for _, tc := range cases {
		q := Select(Col("id")).From(Table{Name: "t"}).Where(tc.expr)
		got, args, err := q.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		want := `SELECT "id" FROM "t" WHERE ` + tc.want
		if got != want {
			t.Errorf("ToSQL() = %q, want %q", got, want)
		}
		// left then pattern: only the pattern binds a value.
		if len(args) != 1 || args[0] != "^a" {
			t.Errorf("ToSQL() args = %#v, want [\"^a\"]", args)
		}
	}
}

func TestRegexpMySQL(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "t"}).Where(Regexp(Col("name"), Val("^a")))
	got, _, err := q.ToSQL(MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := "SELECT `id` FROM `t` WHERE (`name` REGEXP ?)"; got != want {
		t.Errorf("ToSQL() = %q, want %q", got, want)
	}

	neg := Select(Col("id")).From(Table{Name: "t"}).Where(NotRegexp(Col("name"), Val("^a")))
	got, _, _ = neg.ToSQL(MySQLDialect{})
	if want := "SELECT `id` FROM `t` WHERE (`name` NOT REGEXP ?)"; got != want {
		t.Errorf("ToSQL() = %q, want %q", got, want)
	}

	// MySQL cannot express a case-insensitive match inline.
	ci := Select(Col("id")).From(Table{Name: "t"}).Where(IRegexp(Col("name"), Val("^a")))
	if _, _, err := ci.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("ToSQL() error = %v, want case-insensitive rejection", err)
	}
}

func TestRegexpSQLite(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "t"}).Where(Regexp(Col("name"), Val("^a")))
	got, _, err := q.ToSQL(SQLiteDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT "id" FROM "t" WHERE ("name" REGEXP ?)`; got != want {
		t.Errorf("ToSQL() = %q, want %q", got, want)
	}
}

func TestRegexpStandard(t *testing.T) {
	cases := []struct {
		expr Expression
		want string
	}{
		{Regexp(Col("name"), Val("^a")), `("name" LIKE_REGEX ?)`},
		{NotRegexp(Col("name"), Val("^a")), `(NOT ("name" LIKE_REGEX ?))`},
		{IRegexp(Col("name"), Val("^a")), `("name" LIKE_REGEX ? FLAG 'i')`},
		{NotIRegexp(Col("name"), Val("^a")), `(NOT ("name" LIKE_REGEX ? FLAG 'i'))`},
	}
	for _, tc := range cases {
		q := Select(Col("id")).From(Table{Name: "t"}).Where(tc.expr)
		got, _, err := q.ToSQL(StandardSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if want := `SELECT "id" FROM "t" WHERE ` + tc.want; got != want {
			t.Errorf("ToSQL() = %q, want %q", got, want)
		}
	}
}
