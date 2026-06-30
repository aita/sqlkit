package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// These tests run the row-locking clauses against the real engines. The unit
// tests assert the rendered FOR UPDATE / SKIP LOCKED text; here the engine has to
// actually take the locks, so a second transaction observes the effect. Both
// PostgreSQL and MySQL 8 support FOR UPDATE and SKIP LOCKED.

func TestRowLockForUpdate(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// FOR UPDATE inside a transaction returns the row and holds its lock until
		// commit. The smoke test is that it executes and reads back the row.
		s := db.NewSession()
		defer func() { _ = s.Rollback() }()
		var locked userRow
		if err := s.Select().From(blogdb.Users).
			Where(blogdb.Users.ID.Eq(aliceID)).
			ForUpdate().
			One(ctx).Scan(&locked); err != nil {
			t.Fatalf("SELECT FOR UPDATE: %v", err)
		}
		if locked.ID != aliceID {
			t.Fatalf("locked row = %#v, want alice (%d)", locked, aliceID)
		}
		if err := s.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	})
}

func TestRowLockSkipLocked(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)

		// Transaction one locks the lowest-id user (alice) and keeps the lock open.
		locker := db.NewSession()
		defer func() { _ = locker.Rollback() }()
		var held []int64
		if err := locker.Select(blogdb.Users.ID).From(blogdb.Users).
			OrderBy(blogdb.Users.ID.Asc()).
			Limit(1).
			ForUpdate().
			All(ctx).Scan(&held); err != nil {
			t.Fatalf("lock alice: %v", err)
		}
		if len(held) != 1 || held[0] != aliceID {
			t.Fatalf("held rows = %#v, want alice (%d)", held, aliceID)
		}

		// A second transaction asking for the next free row with SKIP LOCKED must
		// step over the locked alice and claim bob instead.
		worker := db.NewSession()
		defer func() { _ = worker.Rollback() }()
		var claimed []int64
		if err := worker.Select(blogdb.Users.ID).From(blogdb.Users).
			OrderBy(blogdb.Users.ID.Asc()).
			Limit(1).
			ForUpdate(sqlkit.SkipLocked()).
			All(ctx).Scan(&claimed); err != nil {
			t.Fatalf("skip-locked claim: %v", err)
		}
		if len(claimed) != 1 || claimed[0] != bobID {
			t.Fatalf("skip-locked claimed = %#v, want bob (%d) with alice locked", claimed, bobID)
		}
	})
}
