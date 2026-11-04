package uow_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// readingHook reads through the scope from BeforeFlush, e.g. to load a related
// row while validating an invariant.
type readingHook struct{ scope **uow.Scope }

func (h readingHook) BeforeFlush(ctx context.Context, _ *uow.ChangeSet) error {
	var u userRow
	_, err := (*h.scope).Get(ctx, newUsers(), &u, int64(1))
	return err
}

// With autoflush on, a read inside BeforeFlush must not start another flush of
// the same pending work: that recursed until the process died with a stack
// overflow.
func TestAutoflushDoesNotReenterFromFlushHook(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "A")}
	var scope *uow.Scope
	scope = uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithAutoflush(true), uow.WithFlushHook(readingHook{&scope}))
	if err := scope.Add(newUsers(), userRow{Email: "new@x"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("execs = %d (%v), want the one INSERT", n, fake.Statements())
	}
}
