package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

// These tests cover the driver-name -> dialect mapping only; they need no SQLite
// driver. The real driver-backed round-trip lives in internal/integration, which
// quarantines the driver dependency from the root module that consumers import.

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
