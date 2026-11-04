package sqlkit_test

import (
	"fmt"
	"sync"
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

// TestRegisterDriverDialectConcurrent drives RegisterDriverDialect and
// DialectForDriver from many goroutines at once against the shared
// driverDialects map. RegisterDriverDialect and DialectForDriver used to write
// and read that map with no synchronization, which -race flags as a data race
// (and an unlucky interleaving of two concurrent RegisterDriverDialect calls
// can fatally crash the process with "concurrent map writes" even without
// -race). Run with -race to catch a regression.
func TestRegisterDriverDialectConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			sqlkit.RegisterDriverDialect(fmt.Sprintf("driver-%d", i), sqlkit.MySQLDialect{})
		}()
		go func() {
			defer wg.Done()
			sqlkit.DialectForDriver(fmt.Sprintf("driver-%d", i))
		}()
	}
	wg.Wait()
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
