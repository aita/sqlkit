package sqlkit_test

// Fault-injection tests for sqlkit's execution paths. They drive the scriptable
// fake driver into failure modes — the driver returns an error, the context is
// cancelled, or a callback panics — and assert the invariants sqlkit promises:
//
//   - no panic escapes a normal call;
//   - errors propagate to the caller, wrapped so errors.Is reaches the package
//     sentinels and the underlying driver error;
//   - row cursors are always closed (happy path and scan failure);
//   - transactions always finish — committed on success, rolled back on error
//     or panic — and untouched sessions never begin one.

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// errDriver is a distinct error the fake driver returns, so errors.Is can prove
// the original value survives the round trip through database/sql and sqlkit.
var errDriver = errors.New("fault: driver failure")

// failingQueryFake returns errDriver from every query.
func failingQueryFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) { return testutil.Result{}, errDriver },
	}
}

// failingExecFake returns errDriver from every exec.
func failingExecFake() *testutil.Fake {
	return &testutil.Fake{
		ExecFunc: func(string, []any) error { return errDriver },
	}
}

// guardNoPanic runs fn and fails the test if it panics, reporting the value.
func guardNoPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("%s panicked: %v", what, p)
		}
	}()
	fn()
}

// --- Contract 1: WithSession rolls back on a callback error -----------------

// TestWithSessionRollbackOnError verifies a callback that returns an error
// rolls the transaction back (observed through the fake's rollback counter, not
// merely the absence of a commit), returns the error to the caller wrapped so
// errors.Is reaches it, and never panics.
func TestWithSessionRollbackOnError(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()
	sentinel := errors.New("callback boom")

	var err error
	guardNoPanic(t, "WithSession(error)", func() {
		err = db.WithSession(ctx, func(s *sqlkit.Session) error {
			if _, e := s.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); e != nil {
				return e
			}
			return sentinel
		})
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithSession() error = %v, want %v", err, sentinel)
	}
	if fake.Begins() != 1 || fake.Commits() != 0 || fake.Rollbacks() != 1 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/0/1",
			fake.Begins(), fake.Commits(), fake.Rollbacks())
	}
}

// --- Contract 2: WithSession rolls back on a callback panic -----------------

// TestWithSessionRollbackOnPanic verifies a callback that panics rolls the
// transaction back and re-panics (no swallowed panic, no leaked open
// transaction). The rollback is observed through the fake's counter.
func TestWithSessionRollbackOnPanic(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	defer func() {
		p := recover()
		if p == nil {
			t.Fatal("panic was swallowed, want re-panic")
		}
		if got, ok := p.(string); !ok || got != "callback panic" {
			t.Fatalf("recovered %v, want \"callback panic\"", p)
		}
		if fake.Begins() != 1 || fake.Commits() != 0 || fake.Rollbacks() != 1 {
			t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/0/1 after panic",
				fake.Begins(), fake.Commits(), fake.Rollbacks())
		}
	}()

	_ = db.WithSession(ctx, func(s *sqlkit.Session) error {
		_, _ = s.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx)
		panic("callback panic")
	})
	t.Fatal("WithSession returned, want it to re-panic")
}

// --- Contract 3: WithSession commits on success -----------------------------

// TestWithSessionCommitOnSuccess verifies a nil return commits exactly once and
// never rolls back.
func TestWithSessionCommitOnSuccess(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	var err error
	guardNoPanic(t, "WithSession(success)", func() {
		err = db.WithSession(ctx, func(s *sqlkit.Session) error {
			_, e := s.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1)).Exec(ctx)
			return e
		})
	})
	if err != nil {
		t.Fatalf("WithSession() error = %v", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 1 || fake.Rollbacks() != 0 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/1/0",
			fake.Begins(), fake.Commits(), fake.Rollbacks())
	}
}

// --- Contract 4: driver errors propagate from every terminal ----------------

// TestSelectAllPropagatesDriverError verifies Select.All surfaces a query error
// wrapped so errors.Is reaches the driver's value, without panicking.
func TestSelectAllPropagatesDriverError(t *testing.T) {
	fake := failingQueryFake()
	db := sqlkit.WrapDB(fake.DB())

	var err error
	var users []User
	guardNoPanic(t, "Select.All", func() {
		err = db.Select().From(Users).All(context.Background()).Scan(&users)
	})
	if !errors.Is(err, errDriver) {
		t.Fatalf("Select.All error = %v, want errors.Is(errDriver)", err)
	}
}

// TestSelectOnePropagatesDriverError verifies Select.One surfaces a query error
// wrapped so errors.Is reaches the driver's value, without panicking.
func TestSelectOnePropagatesDriverError(t *testing.T) {
	fake := failingQueryFake()
	db := sqlkit.WrapDB(fake.DB())

	var err error
	var user User
	guardNoPanic(t, "Select.One", func() {
		err = db.Select().From(Users).Where(Users.ID.Eq(1)).One(context.Background()).Scan(&user)
	})
	if !errors.Is(err, errDriver) {
		t.Fatalf("Select.One error = %v, want errors.Is(errDriver)", err)
	}
}

// TestWriteExecsPropagateDriverError verifies Insert/Update/Delete.Exec each
// surface an exec error wrapped so errors.Is reaches the driver's value,
// without panicking.
func TestWriteExecsPropagateDriverError(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(db *sqlkit.Database) error{
		"Insert": func(db *sqlkit.Database) error {
			_, err := db.Insert(Users).Values(User{Email: "a@example.com"}).Exec(ctx)
			return err
		},
		"Update": func(db *sqlkit.Database) error {
			_, err := db.Update(Users).Set(Users.Email.Set("x")).Where(Users.ID.Eq(1)).Exec(ctx)
			return err
		},
		"Delete": func(db *sqlkit.Database) error {
			_, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx)
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			fake := failingExecFake()
			db := sqlkit.WrapDB(fake.DB())
			var err error
			guardNoPanic(t, name+".Exec", func() { err = run(db) })
			if !errors.Is(err, errDriver) {
				t.Fatalf("%s.Exec error = %v, want errors.Is(errDriver)", name, err)
			}
		})
	}
}

// TestErrorIsReachesDriverThroughErrorsAs additionally proves errors.As unwraps
// the chain to a concrete driver error type.
func TestErrorIsReachesDriverThroughErrorsAs(t *testing.T) {
	typed := &driverError{msg: "typed driver failure"}
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) { return testutil.Result{}, typed },
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []User
	err := db.Select().From(Users).All(context.Background()).Scan(&users)
	var got *driverError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As did not reach *driverError; err = %v", err)
	}
	if got.msg != typed.msg {
		t.Fatalf("errors.As got %q, want %q", got.msg, typed.msg)
	}
}

type driverError struct{ msg string }

func (e *driverError) Error() string { return e.msg }

// --- Contract 5: sentinel errors --------------------------------------------

// TestNoRowsIsErrNoRows verifies a single-row read over an empty result surfaces
// ErrNoRows via errors.Is.
func TestNoRowsIsErrNoRows(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var user User
	var err error
	guardNoPanic(t, "All(empty)", func() {
		err = db.Select().From(Users).All(context.Background()).Scan(&user)
	})
	if !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("Scan(empty) = %v, want ErrNoRows", err)
	}
}

// TestTooManyRowsIsErrTooManyRows verifies One over a multi-row result surfaces
// ErrTooManyRows via errors.Is.
func TestTooManyRowsIsErrTooManyRows(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "a@example.com", nil},
					{int64(2), "b@example.com", nil},
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var user User
	var err error
	guardNoPanic(t, "One(2 rows)", func() {
		err = db.Select().From(Users).One(context.Background()).Scan(&user)
	})
	if !errors.Is(err, sqlkit.ErrTooManyRows) {
		t.Fatalf("One(2 rows) = %v, want ErrTooManyRows", err)
	}
}

// --- Contract 6: context cancellation ---------------------------------------

// TestCancelledContextReadReturnsContextError verifies a read on an
// already-cancelled context returns a context error and does not panic.
func TestCancelledContextReadReturnsContextError(t *testing.T) {
	fake := usersFake()
	db := sqlkit.WrapDB(fake.DB())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var users []User
	var err error
	guardNoPanic(t, "All(cancelled)", func() {
		err = db.Select().From(Users).All(ctx).Scan(&users)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("All(cancelled) = %v, want context.Canceled", err)
	}
}

// TestCancelledContextWriteReturnsContextError verifies a write on an
// already-cancelled context returns a context error and does not panic.
func TestCancelledContextWriteReturnsContextError(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var err error
	guardNoPanic(t, "Delete.Exec(cancelled)", func() {
		_, err = db.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete.Exec(cancelled) = %v, want context.Canceled", err)
	}
}

// TestDeadlineExceededReturnsContextError verifies a read whose deadline has
// already passed returns a context deadline error and does not panic.
func TestDeadlineExceededReturnsContextError(t *testing.T) {
	fake := usersFake()
	db := sqlkit.WrapDB(fake.DB())
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	var users []User
	var err error
	guardNoPanic(t, "All(deadline)", func() {
		err = db.Select().From(Users).All(ctx).Scan(&users)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("All(deadline) = %v, want context.DeadlineExceeded", err)
	}
}

// --- Contract 7: rows are always closed -------------------------------------

// TestRowsClosedOnNormalRead verifies a successful read closes its cursor.
func TestRowsClosedOnNormalRead(t *testing.T) {
	fake := usersFake()
	db := sqlkit.WrapDB(fake.DB())

	var users []User
	if err := db.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("All().Scan() error = %v", err)
	}
	if fake.RowCloses() != 1 {
		t.Fatalf("RowCloses = %d, want 1 (cursor released on normal read)", fake.RowCloses())
	}
}

// TestRowsClosedOnScanFailure verifies a read whose scan fails — here a column
// value of the wrong type for the destination field — still closes its cursor.
func TestRowsClosedOnScanFailure(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			// id is declared int64 on User, but a string cannot scan into it,
			// so structScan fails after the cursor is opened.
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{"not-an-int", "a@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []User
	var err error
	guardNoPanic(t, "All(scan failure)", func() {
		err = db.Select().From(Users).All(context.Background()).Scan(&users)
	})
	if err == nil {
		t.Fatal("All().Scan() succeeded, want a scan type error")
	}
	if fake.RowCloses() != 1 {
		t.Fatalf("RowCloses = %d, want 1 (cursor released despite scan failure)", fake.RowCloses())
	}
}

// --- Contract 8: NewSession Close / Commit invariants -----------------------

// TestNewSessionCloseRollsBackUncommitted verifies Close on a session that ran a
// statement but was never committed rolls the transaction back (observed through
// the fake's rollback counter), and does not panic.
func TestNewSessionCloseRollsBackUncommitted(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	session := db.NewSession()
	ctx := context.Background()

	if _, err := session.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	var err error
	guardNoPanic(t, "Session.Close", func() { err = session.Close() })
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 0 || fake.Rollbacks() != 1 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/0/1 (Close rolled back)",
			fake.Begins(), fake.Commits(), fake.Rollbacks())
	}
}

// TestUntouchedSessionCloseAndCommitDoNotBegin verifies Close and Commit on a
// session that never ran a statement begin no transaction and neither commit nor
// roll back anything.
func TestUntouchedSessionCloseAndCommitDoNotBegin(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	closeSession := db.NewSession()
	guardNoPanic(t, "untouched Close", func() {
		if err := closeSession.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	commitSession := db.NewSession()
	guardNoPanic(t, "untouched Commit", func() {
		if err := commitSession.Commit(); err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
	})

	if fake.Begins() != 0 || fake.Commits() != 0 || fake.Rollbacks() != 0 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 0/0/0 (untouched sessions are no-ops)",
			fake.Begins(), fake.Commits(), fake.Rollbacks())
	}
}
