package uow_test

// AfterFlush semantics: the hook runs only after a successful flush, outside the
// nested scope's guard savepoint, sees a snapshot of what was written, and an
// error from it never undoes the flush.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// failAfter fails AfterFlush.
type failAfter struct{ err error }

func (h failAfter) AfterFlush(context.Context, *uow.ChangeSet) error { return h.err }

// An AfterFlush error on a nested scope must not roll the flushed rows back:
// the staged work is already cleared, so rolling back to the guard savepoint
// would silently lose the rows (a retried Flush emits nothing and the session
// commits without them).
func TestAfterFlushErrorDoesNotRollBackNestedFlush(t *testing.T) {
	sentinel := errors.New("after failed")
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	err := db.WithSession(ctx(), func(s *sqlkit.Session) error {
		child := uow.NewScope(s, uow.WithFlushHook(failAfter{err: sentinel})).NewScope()
		if err := child.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
			return err
		}
		if err := child.Flush(ctx()); !errors.Is(err, sentinel) {
			t.Fatalf("Flush() error = %v, want the AfterFlush error", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("with session: %v", err)
	}
	joined := strings.Join(fake.Statements(), "\n")
	if strings.Contains(joined, "ROLLBACK TO SAVEPOINT") {
		t.Fatalf("AfterFlush error rolled the flush back:\n%s", joined)
	}
	if !strings.Contains(joined, "INSERT") || fake.Commits() != 1 {
		t.Fatalf("want the INSERT committed (commits=%d):\n%s", fake.Commits(), joined)
	}
}

// snapshotHook records what AfterFlush's ChangeSet reports.
type snapshotHook struct {
	inserted, dirty int
	removed         int
	empty           bool
}

func (h *snapshotHook) AfterFlush(_ context.Context, c *uow.ChangeSet) error {
	h.inserted = len(c.Inserted(newUsers()))
	h.dirty = len(c.Dirty(newUsers()))
	h.removed = len(c.Removed(newUsers()))
	h.empty = c.Empty()
	return nil
}

// AfterFlush receives a snapshot of the flushed work, not a view of the
// already-cleared scope.
func TestAfterFlushSeesWhatWasWritten(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "Alice")}
	h := &snapshotHook{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithFlushHook(h))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(1)); err != nil {
		t.Fatal(err)
	}
	u.Name = "Bob"
	if err := scope.Add(newUsers(), &userRow{Email: "c@x"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Remove(newUsers(), &userRow{ID: 9}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if h.inserted != 1 || h.dirty != 1 || h.removed != 1 || h.empty {
		t.Fatalf("AfterFlush saw inserted=%d dirty=%d removed=%d empty=%v, want 1/1/1/false", h.inserted, h.dirty, h.removed, h.empty)
	}

	// A second flush with nothing staged reports an empty change set.
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if !h.empty || h.inserted != 0 || h.dirty != 0 {
		t.Fatalf("idle flush: AfterFlush saw inserted=%d dirty=%d empty=%v, want 0/0/true", h.inserted, h.dirty, h.empty)
	}
}
