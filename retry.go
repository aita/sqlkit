package sqlkit

import (
	"context"
	"errors"
	"time"
)

// retryConfig holds the WithRetry settings consumed by Database.WithSession.
type retryConfig struct {
	maxAttempts int
	retryable   func(error) bool
	backoff     func(attempt int) time.Duration
}

// run invokes attempt until it succeeds, the attempt budget is exhausted, or a
// non-retryable error is returned, waiting the configured backoff between tries.
// It returns the last error. A cancelled ctx cuts the backoff short and stops
// the loop, returning that last error rather than ctx.Err so the underlying
// failure (and its errors.Is matches) survives.
func (c *retryConfig) run(ctx context.Context, attempt func() error) error {
	for n := 1; ; n++ {
		err := attempt()
		if err == nil {
			return nil
		}
		if n >= c.maxAttempts || !c.retryable(err) {
			return err
		}
		if c.backoff != nil {
			if d := c.backoff(n); d > 0 {
				timer := time.NewTimer(d)
				select {
				case <-ctx.Done():
					timer.Stop()
					return err
				case <-timer.C:
				}
			}
		}
	}
}

// RetryOption configures WithRetry.
type RetryOption func(*retryConfig)

// WithRetry makes WithSession replay its whole callback in a fresh transaction
// when it fails with a retryable error — the safe granularity for a retry,
// since a failed statement may have aborted the surrounding transaction, so the
// entire unit of work must run again. It applies only to WithSession (which
// owns the callback); a manually managed NewSession is unaffected.
//
// By default it makes up to 3 attempts and retries an error that is a
// serialization failure or deadlock, or one wrapped with Retryable
// (IsRetryable), waiting an exponential backoff between tries. Tune it with
// MaxAttempts, RetryIf, and RetryBackoff:
//
//	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
//		// ... read, decide, write — all replayed on a serialization failure
//		return nil
//	}, sqlkit.WithRetry(sqlkit.MaxAttempts(5)))
//
// The callback must be idempotent across attempts: it runs again from the top,
// against a new transaction, each time it is retried.
func WithRetry(opts ...RetryOption) SessionOption {
	cfg := &retryConfig{
		maxAttempts: 3,
		retryable:   IsRetryable,
		backoff:     ExponentialBackoff(5*time.Millisecond, time.Second),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	if cfg.retryable == nil {
		cfg.retryable = func(error) bool { return false }
	}
	return retryOption{cfg: cfg}
}

type retryOption struct{ cfg *retryConfig }

func (o retryOption) applySession(s *sessionOptions) { s.retry = o.cfg }

// MaxAttempts caps the total number of tries (the first attempt included). A
// value below 1 is treated as 1 (no retry).
func MaxAttempts(n int) RetryOption {
	return func(c *retryConfig) { c.maxAttempts = n }
}

// RetryIf replaces the predicate that decides whether a failed attempt is worth
// retrying. Use it to retry application-level conflicts (for an idempotent
// callback that reloads its inputs), e.g. RetryIf(func(err error) bool {
// return errors.Is(err, sqlkit.ErrVersionConflict) }), or to recognize a
// driver's deadlock errors that IsSerializationFailure does not.
func RetryIf(pred func(error) bool) RetryOption {
	return func(c *retryConfig) { c.retryable = pred }
}

// RetryBackoff sets the wait before the attempt+1'th try, given the just-failed
// attempt number (1-based). Return 0 (or pass nil) for no wait.
func RetryBackoff(fn func(attempt int) time.Duration) RetryOption {
	return func(c *retryConfig) { c.backoff = fn }
}

// ExponentialBackoff returns a backoff that doubles from base each attempt and
// is capped at max.
func ExponentialBackoff(base, max time.Duration) func(attempt int) time.Duration {
	return func(attempt int) time.Duration {
		d := base << (attempt - 1)
		if d <= 0 || d > max {
			return max
		}
		return d
	}
}

// sqlStater is implemented by SQLSTATE-carrying driver errors, notably pgx's
// *pgconn.PgError. It is matched structurally so sqlkit's core takes on no
// driver dependency.
type sqlStater interface{ SQLState() string }

// IsSerializationFailure reports whether err is a transaction serialization
// failure or a detected deadlock — the transient conflicts that are resolved by
// replaying the transaction. It recognizes the SQLSTATE codes 40001
// (serialization_failure) and 40P01 (deadlock_detected) on any error exposing
// SQLState() string (pgx). MySQL's driver does not expose a SQLSTATE method, so
// detect its deadlock (1213) / lock-wait-timeout (1205) errors with a custom
// RetryIf predicate.
func IsSerializationFailure(err error) bool {
	var s sqlStater
	if errors.As(err, &s) {
		switch s.SQLState() {
		case "40001", "40P01":
			return true
		}
	}
	return false
}

// retryableError marks an arbitrary error as worth retrying. It is transparent
// to errors.Is / errors.As (it unwraps to the wrapped error), so only the retry
// classification changes, not how callers match the error.
type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

// Retryable wraps err so the default retry predicate (IsRetryable) treats it as
// retryable, even when it is not a recognized serialization failure. Return it
// from a WithSession callback — directly or wrapped further with %w — to signal
// that a failure is safe to replay:
//
//	if conflicted {
//		return sqlkit.Retryable(err)
//	}
//
// Retryable(nil) returns nil. The mark survives additional wrapping, since
// IsRetryable finds it anywhere in the error chain.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return retryableError{err: err}
}

// IsRetryable is WithRetry's default predicate: it reports whether err is a
// serialization failure or deadlock (IsSerializationFailure) or was explicitly
// marked with Retryable anywhere in its wrap chain.
func IsRetryable(err error) bool {
	if IsSerializationFailure(err) {
		return true
	}
	var re retryableError
	return errors.As(err, &re)
}
