package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/migrate"
)

// TestRegistryAddAndMigrations checks a Registry collects migrations and returns
// a defensive copy, and that NewMigrator builds a Migrator from the registry's
// set (the package-level-var registration pattern).
func TestRegistryAddAndMigrations(t *testing.T) {
	reg := migrate.NewRegistry()
	reg.Add(mk("0001", "", "a", "t1"))
	reg.Add(mk("0002", "0001", "b", "t2"), mk("0003", "0002", "c", "t3"))

	migs := reg.Migrations()
	if len(migs) != 3 {
		t.Fatalf("Migrations() len = %d, want 3", len(migs))
	}
	// The returned slice is a copy: mutating it must not change the registry.
	migs[0].Name = "mutated"
	if reg.Migrations()[0].Name != "a" {
		t.Error("Migrations() did not return a defensive copy")
	}

	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := reg.NewMigrator(sqlkit.WrapDB(fake.DB()))
	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("NewMigrator Up: %v", err)
	}
	if got := creates(fake.Execs()); len(got) != 3 {
		t.Errorf("NewMigrator applied %v, want 3 tables", got)
	}
}

// TestNewRevisionUnique checks NewRevision returns a 12-hex-char id and that
// repeated calls differ (collision-resistant).
func TestNewRevisionUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		r := migrate.NewRevision()
		if len(r) != 12 {
			t.Fatalf("NewRevision() = %q, want 12 hex chars", r)
		}
		if strings.TrimLeft(r, "0123456789abcdef") != "" {
			t.Fatalf("NewRevision() = %q, want only hex chars", r)
		}
		if seen[r] {
			t.Fatalf("NewRevision() returned a duplicate: %q", r)
		}
		seen[r] = true
	}
}
