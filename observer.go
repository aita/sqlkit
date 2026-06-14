package sqlkit

import "context"

// ResultObserver inspects a query's result after sqlkit has scanned it from the
// database into Go values. It is the read-side counterpart to Interceptor: where
// an interceptor wraps statement execution and sees only the compiled Statement
// and the opaque *sql.Rows cursor (which cannot be read without consuming it), a
// ResultObserver receives the result already materialized into the caller's
// destination, so it can examine column values and collect statistics without
// disturbing the result. It mirrors how Ent (read interceptors), GORM (a query
// callback over Statement.Dest), and SQLAlchemy (do_orm_execute over a Result)
// place whole-result handling above the scan layer rather than in the raw
// execution chain.
//
// ObserveResult is called once per successful Scan, with dest the same value
// Scan was given: a pointer to a slice (every row), or a pointer to a single
// struct or scalar (one row). It runs only when the scan succeeded, never on a
// scan error or a write. sqlkit materializes nothing extra for it — dest is the
// caller's own destination — and skips the call entirely when no observer is
// registered, so an unobserved query holds no result snapshot. Observing is
// read-only: an observer must not retain or mutate dest. Observers registered
// with WithResultObserver run in registration order, and a Session inherits its
// database's observers.
//
// For value-level transformation (as opposed to observation), implement
// database/sql.Scanner on the column type instead; that is the per-value hook
// the standard library already provides.
type ResultObserver interface {
	ObserveResult(ctx context.Context, stmt Statement, dest any)
}

// ObserveResultFunc adapts a plain function to a ResultObserver, for the common
// case of an observer with no state beyond a closure:
//
//	db, _ := sqlkit.Open(driver, dsn, sqlkit.WithResultObserver(
//		sqlkit.ObserveResultFunc(func(ctx context.Context, stmt sqlkit.Statement, dest any) {
//			rows, _ := dest.(*[]User)
//			rowsSeen.Add(int64(len(*rows)))
//		}),
//	))
type ObserveResultFunc func(ctx context.Context, stmt Statement, dest any)

// ObserveResult calls f.
func (f ObserveResultFunc) ObserveResult(ctx context.Context, stmt Statement, dest any) {
	f(ctx, stmt, dest)
}

// observeResult hands dest to every observer in turn. It is a no-op when obs is
// empty, so callers can invoke it unconditionally without materializing dest for
// an observer that is not there.
func observeResult(ctx context.Context, obs []ResultObserver, stmt Statement, dest any) {
	for _, o := range obs {
		o.ObserveResult(ctx, stmt, dest)
	}
}
