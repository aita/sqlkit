package sqlkit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// serializationError is a driver error carrying a SQLSTATE, like pgx's
// *pgconn.PgError, so IsSerializationFailure can recognize it structurally.
type serializationError struct{ code string }

func (e serializationError) Error() string    { return "serialization failure " + e.code }
func (e serializationError) SQLState() string { return e.code }

// noBackoff disables the sleep between retries so tests stay fast.
var noBackoff = sqlkit.RetryBackoff(func(int) time.Duration { return 0 })

func retryFakeDB() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
}

func TestWithRetrySucceedsAfterTransientFailure(t *testing.T) {
	ctx := context.Background()
	fake := retryFakeDB()
	db := sqlkit.WrapDB(fake.DB())

	attempts := 0
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		if err := s.Select().From(Users).All(ctx).Scan(&users); err != nil {
			return err
		}
		if attempts < 2 {
			return serializationError{code: "40001"}
		}
		return nil
	}, sqlkit.WithRetry(sqlkit.MaxAttempts(3), noBackoff))

	if err != nil {
		t.Fatalf("WithSession() error = %v, want nil", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	// Each attempt that ran a statement began its own transaction; only the last
	// committed.
	if fake.Begins() != 2 {
		t.Fatalf("Begins() = %d, want 2", fake.Begins())
	}
	if fake.Commits() != 1 {
		t.Fatalf("Commits() = %d, want 1", fake.Commits())
	}
}

func TestWithRetryExhaustsAttempts(t *testing.T) {
	ctx := context.Background()
	fake := retryFakeDB()
	db := sqlkit.WrapDB(fake.DB())

	attempts := 0
	want := serializationError{code: "40P01"} // deadlock_detected
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		return want
	}, sqlkit.WithRetry(sqlkit.MaxAttempts(3), noBackoff))

	if !errors.Is(err, want) {
		t.Fatalf("WithSession() error = %v, want %v", err, want)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (the cap)", attempts)
	}
}

func TestWithRetryStopsOnNonRetryableError(t *testing.T) {
	ctx := context.Background()
	db := sqlkit.WrapDB(retryFakeDB().DB())

	attempts := 0
	boom := errors.New("application error")
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		return boom
	}, sqlkit.WithRetry(sqlkit.MaxAttempts(5), noBackoff))

	if !errors.Is(err, boom) {
		t.Fatalf("WithSession() error = %v, want %v", err, boom)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on a non-retryable error)", attempts)
	}
}

func TestWithRetryStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := sqlkit.WrapDB(retryFakeDB().DB())

	// A long backoff would block the retry forever; cancelling ctx before it
	// elapses must cut it short and return the last error after a single attempt.
	attempts := 0
	want := serializationError{code: "40001"}
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		cancel()
		return want
	}, sqlkit.WithRetry(
		sqlkit.MaxAttempts(5),
		sqlkit.RetryBackoff(func(int) time.Duration { return time.Hour }),
	))

	if !errors.Is(err, want) {
		t.Fatalf("WithSession() error = %v, want %v", err, want)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (a cancelled ctx cuts the backoff short)", attempts)
	}
}

func TestWithRetryDefaultIgnoresVersionConflict(t *testing.T) {
	ctx := context.Background()
	db := sqlkit.WrapDB(retryFakeDB().DB())

	// ErrVersionConflict is not retried by default — the callback would have to
	// reload its inputs — so opt in explicitly with RetryIf.
	attempts := 0
	_ = db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		return sqlkit.ErrVersionConflict
	}, sqlkit.WithRetry(sqlkit.MaxAttempts(4), noBackoff))
	if attempts != 1 {
		t.Fatalf("default attempts = %d, want 1 (version conflict not retried)", attempts)
	}

	attempts = 0
	_ = db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		return sqlkit.ErrVersionConflict
	}, sqlkit.WithRetry(
		sqlkit.MaxAttempts(4), noBackoff,
		sqlkit.RetryIf(func(err error) bool { return errors.Is(err, sqlkit.ErrVersionConflict) }),
	))
	if attempts != 4 {
		t.Fatalf("opt-in attempts = %d, want 4", attempts)
	}
}

func TestWithSessionWithoutRetryRunsOnce(t *testing.T) {
	ctx := context.Background()
	db := sqlkit.WrapDB(retryFakeDB().DB())

	attempts := 0
	boom := serializationError{code: "40001"}
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		return boom
	})
	if !errors.Is(err, boom) || attempts != 1 {
		t.Fatalf("attempts = %d err = %v, want 1 attempt and the error returned", attempts, err)
	}
}

func TestRetryableMarksErrorRetryable(t *testing.T) {
	base := errors.New("conflict")

	if !sqlkit.IsRetryable(sqlkit.Retryable(base)) {
		t.Fatal("Retryable(err) should be IsRetryable")
	}
	// The mark survives further wrapping with %w, and stays transparent to
	// errors.Is.
	wrapped := fmt.Errorf("save user: %w", sqlkit.Retryable(base))
	if !sqlkit.IsRetryable(wrapped) {
		t.Fatal("a %w-wrapped Retryable should still be IsRetryable")
	}
	if !errors.Is(wrapped, base) {
		t.Fatal("Retryable must unwrap to the original error")
	}
	if sqlkit.Retryable(nil) != nil {
		t.Fatal("Retryable(nil) should be nil")
	}
	if sqlkit.IsRetryable(base) {
		t.Fatal("a plain error should not be IsRetryable")
	}
}

func TestWithRetryReplaysRetryableWrappedError(t *testing.T) {
	ctx := context.Background()
	db := sqlkit.WrapDB(retryFakeDB().DB())

	attempts := 0
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		attempts++
		var users []User
		_ = s.Select().From(Users).All(ctx).Scan(&users)
		if attempts < 2 {
			return fmt.Errorf("transient: %w", sqlkit.Retryable(errors.New("boom")))
		}
		return nil
	}, sqlkit.WithRetry(sqlkit.MaxAttempts(3), noBackoff))

	if err != nil {
		t.Fatalf("WithSession() error = %v, want nil", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestIsSerializationFailure(t *testing.T) {
	cases := map[error]bool{
		serializationError{code: "40001"}: true,  // serialization_failure
		serializationError{code: "40P01"}: true,  // deadlock_detected
		serializationError{code: "23505"}: false, // unique_violation
		errors.New("plain"):               false,
	}
	for err, want := range cases {
		if got := sqlkit.IsSerializationFailure(err); got != want {
			t.Errorf("IsSerializationFailure(%v) = %v, want %v", err, got, want)
		}
	}
}

func TestExponentialBackoff(t *testing.T) {
	b := sqlkit.ExponentialBackoff(5*time.Millisecond, 40*time.Millisecond)
	want := []time.Duration{5, 10, 20, 40, 40}
	for i, w := range want {
		if got := b(i + 1); got != w*time.Millisecond {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w*time.Millisecond)
		}
	}
}
