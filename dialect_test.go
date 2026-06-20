package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestRegisterDriverDialect(t *testing.T) {
	// A custom driver name can be pointed at an existing dialect.
	sqlkit.RegisterDriverDialect("mysql-custom", sqlkit.MySQLDialect{})
	if got := sqlkit.DialectForDriver("mysql-custom").Name(); got != "mysql" {
		t.Fatalf("DialectForDriver(mysql-custom) = %q, want mysql", got)
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
