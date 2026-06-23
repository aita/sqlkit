package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// TestRetryReplaysTransaction drives WithRetry against a real engine. The
// callback writes a row, then fails with a retryable error on its first two
// tries before succeeding, so the test confirms the two guarantees that matter
// on a live server: the whole callback is replayed in a fresh transaction, and
// each failed attempt rolls back — so only the final, committed attempt's write
// survives (no duplicate rows from the replays).
func TestRetryReplaysTransaction(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		attempts := 0
		err := db.WithSession(ctx, func(s *sqlkit.Session) error {
			attempts++
			if _, err := s.Insert(blogdb.Users).
				Values(userRow{Email: "retry@example.com"}).
				Exec(ctx); err != nil {
				return err
			}
			if attempts < 3 {
				// Force a rollback-and-replay; the row written above must not persist.
				return sqlkit.Retryable(errors.New("simulated serialization failure"))
			}
			return nil
		},
			sqlkit.WithRetry(
				sqlkit.MaxAttempts(5),
				sqlkit.RetryBackoff(func(int) time.Duration { return time.Millisecond }),
			),
		)
		if err != nil {
			t.Fatalf("WithRetry: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("attempts = %d, want 3 (two retries then success)", attempts)
		}

		// Exactly one row: the two failed attempts rolled back their inserts.
		n, err := db.Select().From(blogdb.Users).
			Where(blogdb.Users.Email.Eq("retry@example.com")).
			Count(ctx)
		if err != nil {
			t.Fatalf("count after retry: %v", err)
		}
		if n != 1 {
			t.Fatalf("rows after retry = %d, want 1 (replays must roll back)", n)
		}
	})
}
