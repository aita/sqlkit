package sql_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestTruncate(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}

	if got, want := mustDDL(t, sql.Truncate(users), "postgres"), `TRUNCATE TABLE "public"."users";`; got != want {
		t.Fatalf("TRUNCATE = %q, want %q", got, want)
	}

	if got, want := mustDDL(t, sql.Truncate(users), "mysql"), "TRUNCATE TABLE `public`.`users`;"; got != want {
		t.Fatalf("TRUNCATE(mysql) = %q, want %q", got, want)
	}
}

func TestTruncateOptions(t *testing.T) {
	users := sql.Table{Name: "users"}
	logs := sql.Table{Name: "logs"}

	got := mustDDL(t, sql.Truncate(users, logs).RestartIdentity().Cascade(), "postgres")
	want := `TRUNCATE TABLE "users", "logs" RESTART IDENTITY CASCADE;`
	if got != want {
		t.Fatalf("TRUNCATE options = %q, want %q", got, want)
	}

	// On MySQL, RESTART IDENTITY is implicit and renders nothing.
	if got, want := mustDDL(t, sql.Truncate(users).RestartIdentity(), "mysql"), "TRUNCATE TABLE `users`;"; got != want {
		t.Fatalf("TRUNCATE(mysql, restart) = %q, want %q", got, want)
	}
}

func TestTruncateRejections(t *testing.T) {
	users := sql.Table{Name: "users"}
	logs := sql.Table{Name: "logs"}

	cases := []struct {
		name    string
		stmt    sql.Statement
		dialect string
		want    string
	}{
		{"sqlite", sql.Truncate(users), "sqlite", "TRUNCATE"},
		{"mysql multiple", sql.Truncate(users, logs), "mysql", "multiple tables"},
		{"mysql cascade", sql.Truncate(users).Cascade(), "mysql", "CASCADE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := mustDialect(t, tc.dialect)
			if _, _, err := tc.stmt.ToSQL(d); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ToSQL(%s) error = %v, want containing %q", tc.dialect, err, tc.want)
			}
		})
	}
}
