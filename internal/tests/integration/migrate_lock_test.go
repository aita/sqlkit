package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
)

// lockRaceTable and lockRaceMigrations are dedicated to this test: a single
// revision, isolated from testMigrations/testMigrationTables so it does not
// collide with the shared migrator test's history rows or tables.
const lockRaceTable = "lock_race_widgets"

func lockRaceMigrations() []migrate.Migration {
	return []migrate.Migration{
		{
			Revision: "lockrace0001",
			Name:     "create_lock_race_widgets",
			Up: func(op *migrate.Operator) {
				op.CreateTable(lockRaceTable, decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
			},
			Down: func(op *migrate.Operator) { op.DropTable(lockRaceTable) },
		},
	}
}

// TestConcurrentMigratorsSerializeOnAdvisoryLock reproduces, against a real
// engine, the race a prior audit found: two Migrator instances backed by
// separate connection pools (standing in for two pods mid-rollout) both call
// Up() against the same pending revision at the same time. Without the
// session-level advisory lock acquireLock takes before reading
// appliedRevisions, both processes can observe "not yet applied" and both run
// the migration's DDL and history insert, which — for real, non-idempotent
// DDL/DML — either double-runs work or fails one runner outright (e.g. a
// duplicate-table error) instead of the second cleanly skipping it. With the
// lock in place exactly one Migrator performs the CREATE TABLE and exactly
// one history row is recorded; the other's Up() still succeeds (it observes
// the revision already applied once it gets the lock) and does no work.
func TestConcurrentMigratorsSerializeOnAdvisoryLock(t *testing.T) {
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			db1 := connect(t, eng)
			db2 := connectSecond(t, eng)
			ctx := context.Background()

			m1 := migrate.New(db1, lockRaceMigrations(), migrate.WithSchema(testSchema))
			m2 := migrate.New(db2, lockRaceMigrations(), migrate.WithSchema(testSchema))

			// Isolate from any earlier run of this test on the same database.
			t.Cleanup(func() {
				_ = m1.DownTo(ctx, "")
			})
			if err := m1.DownTo(ctx, ""); err != nil {
				t.Fatalf("pre-clean DownTo: %v", err)
			}

			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			go func() { defer wg.Done(); errs[0] = m1.Up(ctx) }()
			go func() { defer wg.Done(); errs[1] = m2.Up(ctx) }()
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Fatalf("Up() [migrator %d] error = %v (the advisory lock should make the loser observe the revision already applied, not fail)", i, err)
				}
			}

			history, err := m1.History(ctx)
			if err != nil {
				t.Fatalf("History: %v", err)
			}
			var ups int
			for _, e := range history {
				if e.Revision == "lockrace0001" && e.Direction == migrate.DirectionUp {
					ups++
				}
			}
			if ups != 1 {
				t.Errorf("history has %d up-events for lockrace0001, want exactly 1 (double-apply race not prevented)", ups)
			}
		})
	}
}

// TestMigratorSingleConnectionPool runs a full Up/Down cycle on a pool limited
// to one connection. The migration lock pins a connection for the whole run, so
// unless the run itself executes on that same connection it would wait forever
// for a second one; the context deadline turns such a self-deadlock into a
// failure instead of a hung test.
func TestMigratorSingleConnectionPool(t *testing.T) {
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			db := connectSecond(t, eng)
			db.SQL().SetMaxOpenConns(1)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			m := migrate.New(db, lockRaceMigrations(), migrate.WithSchema(testSchema))
			t.Cleanup(func() { _ = m.DownTo(context.Background(), "") })
			if err := m.Up(ctx); err != nil {
				t.Fatalf("Up() on a single-connection pool: %v", err)
			}
			if err := m.Stamp(ctx, "head"); err != nil {
				t.Fatalf("Stamp() on a single-connection pool: %v", err)
			}
			if err := m.DownTo(ctx, ""); err != nil {
				t.Fatalf("DownTo() on a single-connection pool: %v", err)
			}
			// The lock is released: a fresh run can take it again.
			if err := m.Up(ctx); err != nil {
				t.Fatalf("second Up(): %v", err)
			}
		})
	}
}
