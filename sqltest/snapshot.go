package sqltest

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
)

// Snapshot records the session's current state as a reusable restore point and
// returns a function that rolls back to it. Seed your fixtures into the session
// once, snapshot, then run each case against the same session and call the
// returned restore between cases to discard that case's writes and return to the
// seeded state — far cheaper than reloading the fixtures every time:
//
//	tdb := sqltest.New(db, md)
//	tdb.SetupSchema(t)
//	session := tdb.Tx(t)             // one session, rolled back at the end
//	blog.Load(t, session)            // seed once
//	restore := sqltest.Snapshot(t, session)
//
//	t.Run("case1", func(t *testing.T) {
//		defer restore(t)         // undo case1's writes
//		// ... mutate and assert against session ...
//	})
//	t.Run("case2", func(t *testing.T) {
//		defer restore(t)         // case2 sees the seeded state again
//		// ...
//	})
//
// It uses a SAVEPOINT (Session.Savepoint / RollbackTo), so it works on
// PostgreSQL and MySQL alike and the restore point survives repeated restores.
// The catch is that every case must run its statements through the same session
// and the session must stay open and uncommitted: committing or closing it ends
// the transaction and invalidates the restore point. Cases therefore cannot run
// in parallel against the snapshot.
func Snapshot(t *testing.T, s *sqlkit.Session) (restore func(t *testing.T)) {
	t.Helper()
	name, err := s.Savepoint(context.Background())
	if err != nil {
		t.Fatalf("sqltest: snapshot: %v", err)
	}
	return func(t *testing.T) {
		t.Helper()
		if err := s.RollbackTo(context.Background(), name); err != nil {
			t.Fatalf("sqltest: restore snapshot: %v", err)
		}
	}
}

// LoadSnapshot loads the fixture into the session and snapshots immediately,
// returning the restore function. It is Fixture.Load followed by Snapshot, for
// the common case of seeding a fixture and reusing it across cases.
func (f *Fixture) LoadSnapshot(t *testing.T, s *sqlkit.Session) (restore func(t *testing.T)) {
	t.Helper()
	f.Load(t, s)
	return Snapshot(t, s)
}
