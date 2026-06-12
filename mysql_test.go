package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

func TestMySQLDialectEndToEnd(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(1), "a@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))
	ctx := context.Background()

	var users []User
	err := db.Select().From(Users).Where(Users.ID.Gt(0)).Limit(5).All(ctx).Scan(&users)
	if err != nil {
		t.Fatalf("All().Scan() error = %v", err)
	}
	want := "SELECT `public`.`users`.`id`, `public`.`users`.`email`, `public`.`users`.`name` FROM `public`.`users` WHERE (`public`.`users`.`id` > ?) LIMIT 5"
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("SQL = %q,\nwant %q", got, want)
	}

	if _, err := db.Insert(Users).Values(User{Email: "a@example.com"}).Exec(ctx); err != nil {
		t.Fatalf("Insert Exec() error = %v", err)
	}
	if got := fake.Execs()[0].SQL; got != "INSERT INTO `public`.`users` (`email`, `name`) VALUES (?, ?)" {
		t.Fatalf("INSERT SQL = %q", got)
	}
}

func TestMySQLReturningFailsAtScanWithoutQuerying(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))

	var inserted []User
	err := db.Insert(Users).Values(User{Email: "a@example.com"}).
		Returning().
		All(context.Background()).Scan(&inserted)
	if err == nil || !strings.Contains(err.Error(), "does not support RETURNING") {
		t.Fatalf("Scan() error = %v, want RETURNING rejection", err)
	}
	if n := len(fake.Queries()) + len(fake.Execs()); n != 0 {
		t.Fatalf("rejected statement still reached the database: %d statements", n)
	}
}

func TestDialectForDriver(t *testing.T) {
	if sqlkit.DialectForDriver("mysql").Name() != "mysql" {
		t.Fatal("mysql driver should select the MySQL dialect")
	}
	for _, name := range []string{"pgx", "postgres", "anything-else"} {
		if sqlkit.DialectForDriver(name).Name() != "postgres" {
			t.Fatalf("driver %q should default to postgres", name)
		}
	}
}
