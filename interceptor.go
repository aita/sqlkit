package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"

	"github.com/aita/sqlkit/sql"
)

// errNoStatementQuery is returned by Statement.Recompile when there is no AST
// to recompile (e.g. a raw-string statement).
var errNoStatementQuery = errors.New("sqlkit: statement has no Query to recompile")

// Op is the kind of statement an interceptor sees: a row-returning read or a
// result-returning write.
type Op int

const (
	// OpQuery is a read that yields rows (SELECT, a RETURNING read, RawSQL).
	OpQuery Op = iota
	// OpExec is a write that yields a sql.Result (INSERT, UPDATE, DELETE, and
	// the DDL statements run through Exec).
	OpExec
)

// String renders the Op for logs and errors.
func (o Op) String() string {
	switch o {
	case OpQuery:
		return "query"
	case OpExec:
		return "exec"
	default:
		return "unknown"
	}
}

// Statement is the query an interceptor inspects and may rewrite before it runs.
//
// SQL and Args are the source of truth for execution: the terminal handler runs
// exactly what they hold, so rewriting them changes what reaches the database.
// Query carries the SQL DSL (the sql.Query AST) the statement was compiled from,
// for interceptors that want to branch on its structure — the target table, the
// kind of statement, and so on. It is nil when there is no AST, as with a
// RawSQL string. To rewrite at the AST level, build a new query, assign it to
// Query, and call Recompile to refresh SQL and Args from it.
type Statement struct {
	// Op is OpQuery for reads and OpExec for writes.
	Op Op
	// Query is the SQL DSL the statement was compiled from, or nil for a
	// raw-string statement (RawSQL with a string).
	Query sql.Query
	// SQL is the compiled SQL text that will run.
	SQL string
	// Args are the bind arguments that will run.
	Args []any
	// Dialect is the dialect SQL was compiled for; Recompile uses it.
	Dialect sql.Dialect
}

// Recompile recompiles Query for the statement's Dialect and replaces SQL and
// Args with the result. It is the AST-level rewrite path: set Query to a new
// builder result, then call Recompile. It returns an error if Query is nil or
// compilation fails.
func (s *Statement) Recompile() error {
	if s.Query == nil {
		return errNoStatementQuery
	}
	sqlText, args, err := s.Query.ToSQL(s.Dialect)
	if err != nil {
		return err
	}
	s.SQL = sqlText
	s.Args = args
	return nil
}

// QueryHandler runs a read statement and returns its rows. It is the next link
// an InterceptQuery implementation calls to proceed down the chain.
type QueryHandler func(ctx context.Context, stmt Statement) (*stdsql.Rows, error)

// ExecHandler runs a write statement and returns its result. It is the next
// link an InterceptExec implementation calls to proceed down the chain.
type ExecHandler func(ctx context.Context, stmt Statement) (stdsql.Result, error)

// Interceptor wraps statement execution, gRPC-style: each method receives the
// Statement about to run and the next handler in the chain, and decides whether
// (and with what) to call it. An interceptor can observe and time execution,
// rewrite stmt before calling next, short-circuit by returning without calling
// next, or retry by calling next more than once. Interceptors registered with
// WithInterceptor run in registration order, the first wrapping the rest.
//
// Reads (OpQuery) and writes (OpExec) keep their native return types — *sql.Rows
// and sql.Result — so they are separate methods rather than one over a union.
// Embed NoopInterceptor to implement only the method you care about, or use
// Rewrite for a statement transform that applies to both.
//
// Transaction-control statements (a Session's savepoints) bypass interceptors.
type Interceptor interface {
	InterceptQuery(ctx context.Context, stmt Statement, next QueryHandler) (*stdsql.Rows, error)
	InterceptExec(ctx context.Context, stmt Statement, next ExecHandler) (stdsql.Result, error)
}

// NoopInterceptor is a no-op Interceptor that passes straight through to next.
// Embed it to implement only one of the two methods:
//
//	type queryLogger struct{ sqlkit.NoopInterceptor }
//
//	func (queryLogger) InterceptQuery(ctx context.Context, stmt sqlkit.Statement, next sqlkit.QueryHandler) (*sql.Rows, error) {
//		log.Println(stmt.SQL)
//		return next(ctx, stmt)
//	}
type NoopInterceptor struct{}

// InterceptQuery passes through to next unchanged.
func (NoopInterceptor) InterceptQuery(ctx context.Context, stmt Statement, next QueryHandler) (*stdsql.Rows, error) {
	return next(ctx, stmt)
}

// InterceptExec passes through to next unchanged.
func (NoopInterceptor) InterceptExec(ctx context.Context, stmt Statement, next ExecHandler) (stdsql.Result, error) {
	return next(ctx, stmt)
}

// Rewrite adapts a statement transform into an Interceptor that applies fn to
// both reads and writes just before execution. fn receives the Statement and
// returns the one to run, or an error to abort it. It is the shortcut for the
// common case — rewriting SQL or Args — without the next plumbing of the full
// interface:
//
//	db, _ := sqlkit.Open(driver, dsn, sqlkit.WithInterceptor(
//		sqlkit.Rewrite(func(ctx context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
//			stmt.SQL = "/* tenant=" + tenantOf(ctx) + " */ " + stmt.SQL
//			return stmt, nil
//		}),
//	))
func Rewrite(fn func(ctx context.Context, stmt Statement) (Statement, error)) Interceptor {
	return rewriteInterceptor{fn: fn}
}

type rewriteInterceptor struct {
	fn func(ctx context.Context, stmt Statement) (Statement, error)
}

func (r rewriteInterceptor) InterceptQuery(ctx context.Context, stmt Statement, next QueryHandler) (*stdsql.Rows, error) {
	stmt, err := r.fn(ctx, stmt)
	if err != nil {
		return nil, err
	}
	return next(ctx, stmt)
}

func (r rewriteInterceptor) InterceptExec(ctx context.Context, stmt Statement, next ExecHandler) (stdsql.Result, error) {
	stmt, err := r.fn(ctx, stmt)
	if err != nil {
		return nil, err
	}
	return next(ctx, stmt)
}

// runQuery executes a read statement through ex's interceptor chain, with the
// raw driver query as the terminal handler.
func runQuery(ctx context.Context, ex Executor, stmt Statement) (*stdsql.Rows, error) {
	handler := func(ctx context.Context, s Statement) (*stdsql.Rows, error) {
		return ex.queryContext(ctx, s.SQL, s.Args...)
	}
	ics := ex.interceptors()
	for i := len(ics) - 1; i >= 0; i-- {
		ic, next := ics[i], handler
		handler = func(ctx context.Context, s Statement) (*stdsql.Rows, error) {
			return ic.InterceptQuery(ctx, s, next)
		}
	}
	return handler(ctx, stmt)
}

// runExecStmt executes a write statement through ex's interceptor chain, with
// the raw driver exec as the terminal handler.
func runExecStmt(ctx context.Context, ex Executor, stmt Statement) (stdsql.Result, error) {
	handler := func(ctx context.Context, s Statement) (stdsql.Result, error) {
		return ex.execContext(ctx, s.SQL, s.Args...)
	}
	ics := ex.interceptors()
	for i := len(ics) - 1; i >= 0; i-- {
		ic, next := ics[i], handler
		handler = func(ctx context.Context, s Statement) (stdsql.Result, error) {
			return ic.InterceptExec(ctx, s, next)
		}
	}
	return handler(ctx, stmt)
}
