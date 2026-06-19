package sqlkit

import (
	"context"
	stdsql "database/sql"
)

// Hooks observes — and may rewrite — statements around execution. It is the
// lifecycle counterpart to a value-level database/sql.Scanner: a Before hook
// runs just before a statement reaches the database, and an After hook runs
// once its result has been produced, at the boundary where the result is a
// concrete Go value rather than the opaque *sql.Rows cursor the execution path
// works with. That boundary is the point of the design: a read result cannot be
// transformed mid-execution (consuming *sql.Rows would starve the scanner), but
// after the scan dest is the caller's own materialized value, free to inspect
// or rewrite.
//
// Register Hooks with WithHooks; nil fields are skipped, so a hook fills in only
// the lifecycle points it needs. Multiple Hooks run in registration order, and a
// Session inherits its database's Hooks. Transaction-control statements (a
// Session's savepoints) bypass hooks.
//
// Hooks deliberately offer no retry or short-circuit: statement-level retry is
// unsafe across a transaction boundary (a failed statement may have aborted the
// surrounding transaction, so the whole unit of work must replay), and belongs
// outside the Database around the unit of work. For per-value transformation,
// implement database/sql.Scanner on the column type instead.
type Hooks struct {
	// Before runs just before a statement executes, for both reads and writes
	// (stmt.Op distinguishes them). It may rewrite *stmt — its SQL and Args, or
	// its Query followed by Recompile — or return an error to abort the
	// statement before it reaches the database. The returned context flows into
	// execution and the matching After hook, so a tracer can start a span here
	// and end it there. A nil Before is skipped.
	Before func(ctx context.Context, stmt *Statement) (context.Context, error)

	// AfterQuery runs after a read's result is scanned into dest, the caller's
	// own destination (a pointer to a slice for every row, or to a single struct
	// or scalar for one row). It runs on the execution or scan error too, with
	// err non-nil and dest not valid, so spans close and failures are observed;
	// observers that only care about success return early when err != nil. dest
	// is the materialized result, so a hook may inspect or transform it in place.
	// Returning an error fails the call; returning nil leaves any execution error
	// in place — a hook reports its own error, it does not swallow the query's. A
	// nil AfterQuery is skipped.
	AfterQuery func(ctx context.Context, stmt Statement, dest any, err error) error

	// AfterExec runs after a write, with the sql.Result it produced (nil on
	// error). Its error contract matches AfterQuery: it runs on failure too, and
	// returning an error fails the call without masking an execution error. A nil
	// AfterExec is skipped.
	AfterExec func(ctx context.Context, stmt Statement, result stdsql.Result, err error) error
}

// runBefore runs each registered Before hook over stmt in order, threading the
// context each returns into the next and out to the caller. It stops at the
// first hook error, leaving stmt as that hook last left it.
func runBefore(ctx context.Context, hooks []Hooks, stmt *Statement) (context.Context, error) {
	for _, h := range hooks {
		if h.Before == nil {
			continue
		}
		next, err := h.Before(ctx, stmt)
		if err != nil {
			return ctx, err
		}
		ctx = next
	}
	return ctx, nil
}

// afterQuery runs each AfterQuery hook in order. execErr is the execution or
// scan error (nil on success), passed to every hook so failures are observed.
// It returns the first hook error, or execErr when no hook overrides it, so a
// hook reports its own error without masking the query's.
func afterQuery(ctx context.Context, hooks []Hooks, stmt Statement, dest any, execErr error) error {
	for _, h := range hooks {
		if h.AfterQuery == nil {
			continue
		}
		if err := h.AfterQuery(ctx, stmt, dest, execErr); err != nil {
			return err
		}
	}
	return execErr
}

// afterExec runs each AfterExec hook in order, with the same error contract as
// afterQuery.
func afterExec(ctx context.Context, hooks []Hooks, stmt Statement, res stdsql.Result, execErr error) error {
	for _, h := range hooks {
		if h.AfterExec == nil {
			continue
		}
		if err := h.AfterExec(ctx, stmt, res, execErr); err != nil {
			return err
		}
	}
	return execErr
}

// runQuery runs a read statement's Before hooks, then executes it on ex's
// terminal Executor. It returns the context the Before hooks produced (carrying
// any tracing span) so the caller can pass it to the AfterQuery hooks once the
// result is scanned.
func runQuery(ctx context.Context, ex executorBinding, stmt Statement) (context.Context, *stdsql.Rows, error) {
	ctx, err := runBefore(ctx, ex.hooks(), &stmt)
	if err != nil {
		return ctx, nil, err
	}
	rows, err := ex.executor().Query(ctx, stmt)
	return ctx, rows, err
}

// runExecStmt runs a write statement's Before hooks, executes it on ex's
// terminal Executor, then runs its AfterExec hooks. The whole write lifecycle
// lives here because, unlike a read, a write has no later scan step to hang the
// After hook on.
func runExecStmt(ctx context.Context, ex executorBinding, stmt Statement) (stdsql.Result, error) {
	hooks := ex.hooks()
	ctx, err := runBefore(ctx, hooks, &stmt)
	if err != nil {
		return nil, err
	}
	res, execErr := ex.executor().Exec(ctx, stmt)
	return res, afterExec(ctx, hooks, stmt, res, execErr)
}
