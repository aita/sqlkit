package uow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// recordHook implements both BeforeFlushHook and AfterFlushHook.
type recordHook struct {
	before, after int
	dirtyNames    []string
}

func (h *recordHook) BeforeFlush(_ context.Context, c *uow.ChangeSet) error {
	h.before++
	for _, p := range c.Dirty(newUsers()) {
		h.dirtyNames = append(h.dirtyNames, p.(*userRow).Name)
	}
	return nil
}

func (h *recordHook) AfterFlush(_ context.Context, _ *uow.ChangeSet) error {
	h.after++
	return nil
}

func TestFlushHookFiresAroundFlush(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "Alice")}
	h := &recordHook{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithFlushHook(h))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(1)); err != nil {
		t.Fatal(err)
	}
	u.Name = "Bob"
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if h.before != 1 || h.after != 1 {
		t.Fatalf("before=%d after=%d, want 1/1", h.before, h.after)
	}
	if len(h.dirtyNames) != 1 || h.dirtyNames[0] != "Bob" {
		t.Fatalf("BeforeFlush saw dirty names %v, want [Bob]", h.dirtyNames)
	}
}

// abortHook fails BeforeFlush.
type abortHook struct{ err error }

func (h abortHook) BeforeFlush(_ context.Context, _ *uow.ChangeSet) error { return h.err }

func TestBeforeFlushErrorAbortsFlush(t *testing.T) {
	fake := &testutil.Fake{}
	sentinel := errors.New("nope")
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithFlushHook(abortHook{err: sentinel}))
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); !errors.Is(err, sentinel) {
		t.Fatalf("flush err = %v, want the BeforeFlush sentinel", err)
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0 (BeforeFlush aborted the flush)", got)
	}
}

// stampHook stamps a column on every dirty row, the audit-timestamp use case.
type stampHook struct{}

func (stampHook) BeforeFlush(_ context.Context, c *uow.ChangeSet) error {
	for _, p := range c.Dirty(newUsers()) {
		p.(*userRow).Email = "stamped@x"
	}
	return nil
}

func TestBeforeFlushStampReachesUpdate(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "Alice")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithFlushHook(stampHook{}))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(1)); err != nil {
		t.Fatal(err)
	}
	u.Name = "Bob" // makes the row dirty so the hook stamps it
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	upd := fake.Execs()[0]
	stamped := false
	for _, a := range upd.Args {
		if a == "stamped@x" {
			stamped = true
		}
	}
	if !stamped {
		t.Fatalf("update args = %v, want the hook's stamped email", upd.Args)
	}
}

// beforeOnly implements only BeforeFlushHook, to check each method is optional
// and discovered by type assertion.
type beforeOnly struct{ n *int }

func (h beforeOnly) BeforeFlush(_ context.Context, _ *uow.ChangeSet) error { *h.n++; return nil }

func TestFlushHookMethodsAreOptional(t *testing.T) {
	fake := &testutil.Fake{}
	var n int
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithFlushHook(beforeOnly{n: &n}))
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n != 1 {
		t.Fatalf("BeforeFlush count = %d, want 1 (a before-only hook still fires)", n)
	}
}
