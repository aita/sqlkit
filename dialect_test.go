package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestDialectForDriverSQLite(t *testing.T) {
	for _, name := range []string{"sqlite", "sqlite3"} {
		if got := sqlkit.DialectForDriver(name).Name(); got != "sqlite" {
			t.Fatalf("DialectForDriver(%q) = %q, want sqlite", name, got)
		}
	}
}

func TestRegisterDriverDialect(t *testing.T) {
	// A custom driver name can be pointed at an existing dialect.
	sqlkit.RegisterDriverDialect("sqlite-custom", sqlkit.SQLiteDialect{})
	if got := sqlkit.DialectForDriver("sqlite-custom").Name(); got != "sqlite" {
		t.Fatalf("DialectForDriver(sqlite-custom) = %q, want sqlite", got)
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
