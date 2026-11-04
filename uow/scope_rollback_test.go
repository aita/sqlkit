package uow_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// Rolling a nested scope back to its savepoint also undoes everything executed
// after it, so once the parent has flushed work after the child's savepoint,
// Rollback must refuse rather than silently discard the parent's rows.
func TestNestedRollbackRefusesToDiscardParentFlush(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	err := db.WithSession(ctx(), func(s *sqlkit.Session) error {
		parent := uow.NewScope(s)
		child := parent.NewScope()
		if err := child.Add(newUsers(), userRow{Email: "child"}); err != nil {
			return err
		}
		if err := child.Flush(ctx()); err != nil {
			return err
		}
		if err := parent.Add(newUsers(), userRow{Email: "parent"}); err != nil {
			return err
		}
		if err := parent.Flush(ctx()); err != nil {
			return err
		}
		if err := child.Rollback(ctx()); !errors.Is(err, uow.ErrRollbackDiscardsWork) {
			t.Fatalf("Rollback err = %v, want ErrRollbackDiscardsWork", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(fake.Statements(), "\n"); strings.Contains(joined, "ROLLBACK TO") {
		t.Fatalf("Rollback still rolled back to the savepoint:\n%s", joined)
	}
}

// A sibling that flushed after this scope's savepoint is protected the same way.
func TestNestedRollbackRefusesToDiscardSiblingFlush(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	_ = db.WithSession(ctx(), func(s *sqlkit.Session) error {
		parent := uow.NewScope(s)
		a, b := parent.NewScope(), parent.NewScope()
		_ = a.Add(newUsers(), userRow{Email: "a"})
		_ = a.Flush(ctx())
		_ = b.Add(newUsers(), userRow{Email: "b"})
		_ = b.Flush(ctx())
		if err := a.Rollback(ctx()); !errors.Is(err, uow.ErrRollbackDiscardsWork) {
			t.Fatalf("Rollback err = %v, want ErrRollbackDiscardsWork", err)
		}
		return nil
	})
}

// Rollback still works when nothing outside the scope flushed after its
// savepoint: its own (and its descendants') flushes, or other scopes' flushes
// from before the savepoint, or an empty parent flush.
func TestNestedRollbackAllowedWithoutLaterOuterWork(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	_ = db.WithSession(ctx(), func(s *sqlkit.Session) error {
		parent := uow.NewScope(s)
		_ = parent.Add(newUsers(), userRow{Email: "before"})
		_ = parent.Flush(ctx())
		child := parent.NewScope()
		grandchild := child.NewScope()
		_ = child.Add(newUsers(), userRow{Email: "child"})
		_ = child.Flush(ctx())
		_ = grandchild.Add(newUsers(), userRow{Email: "grandchild"})
		_ = grandchild.Flush(ctx())
		_ = child.Flush(ctx())
		_ = parent.Flush(ctx()) // nothing staged: writes nothing
		if err := child.Rollback(ctx()); err != nil {
			t.Fatalf("Rollback err = %v, want nil", err)
		}
		return nil
	})
	if joined := strings.Join(fake.Statements(), "\n"); !strings.Contains(joined, "ROLLBACK TO SAVEPOINT") {
		t.Fatalf("Rollback did not roll back:\n%s", joined)
	}
}
