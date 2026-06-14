package sqlkit

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"reflect"

	"github.com/aita/sqlkit/sql"
)

// compiledStmt is one already-compiled statement: SQL text plus bind arguments.
type compiledStmt struct {
	sql  string
	args []any
}

// statement wraps the compiled SQL as the Statement an interceptor sees for one
// split chunk. The AST is omitted (the chunk has no standalone builder), so an
// interceptor that needs to Recompile should rewrite SQL/Args directly.
func (c compiledStmt) statement(op Op, dialect sql.Dialect) Statement {
	return Statement{Op: op, SQL: c.sql, Args: c.args, Dialect: dialect}
}

// batchResult aggregates the per-batch results of a split statement: it sums
// RowsAffected across batches and reports LastInsertId from the final batch.
type batchResult struct {
	last         stdsql.Result
	rowsAffected int64
	rowsErr      error
}

func (r *batchResult) add(res stdsql.Result) {
	if res == nil {
		return
	}
	r.last = res
	if n, err := res.RowsAffected(); err != nil {
		r.rowsErr = err
	} else {
		r.rowsAffected += n
	}
}

func (r *batchResult) LastInsertId() (int64, error) {
	if r.last == nil {
		return 0, fmt.Errorf("sqlkit: LastInsertId is unavailable for an empty batched statement")
	}
	return r.last.LastInsertId()
}

func (r *batchResult) RowsAffected() (int64, error) {
	if r.rowsErr != nil {
		return 0, r.rowsErr
	}
	return r.rowsAffected, nil
}

// execStatements runs the compiled statements and aggregates their results. A
// single statement runs directly on the executor. When several statements must
// run and the executor auto-commits (a *Database), they run inside one
// transaction so a mid-batch failure rolls the whole set back; a *Session reuses
// its in-flight transaction, and any other executor runs them sequentially.
func execStatements(ctx context.Context, ex Executor, stmts []compiledStmt) (stdsql.Result, error) {
	if ex == nil {
		return nil, errNoExecutor
	}
	if len(stmts) == 1 {
		return runExecStmt(ctx, ex, stmts[0].statement(OpExec, ex.sqlDialect()))
	}
	if db, ok := ex.(*Database); ok {
		var out stdsql.Result
		err := db.WithSession(func(s *Session) error {
			res, err := execOn(ctx, s, stmts)
			out = res
			return err
		}, db.batchWriteSessionOptions()...)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	return execOn(ctx, ex, stmts)
}

func execOn(ctx context.Context, ex Executor, stmts []compiledStmt) (stdsql.Result, error) {
	result := &batchResult{}
	dialect := ex.sqlDialect()
	for _, st := range stmts {
		res, err := runExecStmt(ctx, ex, st.statement(OpExec, dialect))
		if err != nil {
			return nil, err
		}
		result.add(res)
	}
	return result, nil
}

// detectBindParamLimit confirms the connection is usable and returns the
// dialect's per-statement bind-parameter limit. The cap itself is a protocol
// constant (65535 for PostgreSQL and MySQL) exposed through BindParamLimiter;
// the round-trip validates that the database is reachable before a Database is
// configured to split statements at the limit.
func detectBindParamLimit(ctx context.Context, db *stdsql.DB, dialect sql.Dialect) (int, error) {
	if err := db.PingContext(ctx); err != nil {
		return 0, err
	}
	limiter, ok := dialect.(sql.BindParamLimiter)
	if !ok {
		return 0, fmt.Errorf("sqlkit: dialect %s does not report a bind-parameter limit", dialect.Name())
	}
	return limiter.MaxBindParams(), nil
}

// splitInStatements splits a deferred column IN whose value list would exceed
// ex's bind-parameter limit into one compiled statement per chunk, or reports
// ok=false when no split applies (no batch, no executor, no limit, or the values
// already fit). compile renders the statement carrying the given WHERE-IN
// expression; it is called once with nil to compile the IN-free base and size
// its other parameters against the limit.
func splitInStatements(ex Executor, batch *batchIn, compile func(in sql.Expression) (string, []any, error)) (stmts []compiledStmt, ok bool, err error) {
	if batch == nil || ex == nil {
		return nil, false, nil
	}
	limit := ex.maxBindParams()
	if limit <= 0 {
		return nil, false, nil
	}
	_, baseArgs, err := compile(nil)
	if err != nil {
		return nil, false, err
	}
	per := limit - len(baseArgs)
	if per < 1 {
		per = 1
	}
	if len(batch.values) <= per {
		return nil, false, nil
	}
	chunks := chunkValues(batch.values, per)
	stmts = make([]compiledStmt, 0, len(chunks))
	for _, chunk := range chunks {
		sqlText, args, err := compile(batch.exprFor(chunk))
		if err != nil {
			return nil, false, err
		}
		stmts = append(stmts, compiledStmt{sql: sqlText, args: args})
	}
	return stmts, true, nil
}

// InBatches splits values into chunks small enough that build's per-chunk query
// stays within ex's configured bind-parameter limit, runs each chunk, and
// concatenates the scanned rows into dest (a pointer to a slice). It is the
// manual counterpart to the automatic IN-list chunking the typed builders apply
// to a column's In: reach for it when the query the builder cannot split
// on its own — a hand-built IN, or one combined with ORDER BY/LIMIT — must still
// run a large value list. build receives each chunk and should return the Rows
// for it (typically db.Select()...Where(col.In(chunk...)).All(ctx)).
//
// chunkSize defaults to ex's bind-parameter limit; pass a positive override to
// cap it lower (e.g. to leave room for the query's other parameters). With no
// limit configured and no override, every value goes in a single chunk.
func InBatches[V any](ctx context.Context, ex Executor, values []V, build func(chunk []V) Rows, dest any, chunkSize ...int) error {
	size := 0
	if ex != nil {
		size = ex.maxBindParams()
	}
	if len(chunkSize) > 0 && chunkSize[0] > 0 {
		size = chunkSize[0]
	}

	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() || dv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("sqlkit: InBatches destination must be a non-nil pointer to a slice, got %T", dest)
	}
	sliceType := dv.Elem().Type()
	acc := reflect.MakeSlice(sliceType, 0, len(values))

	for _, chunk := range chunkValues(values, size) {
		tmp := reflect.New(sliceType)
		if err := build(chunk).Scan(tmp.Interface()); err != nil {
			return err
		}
		acc = reflect.AppendSlice(acc, tmp.Elem())
	}
	dv.Elem().Set(acc)
	return nil
}

// chunkValues splits values into consecutive chunks of at most size elements. A
// size of zero or fewer keeps the whole slice as a single chunk.
func chunkValues[V any](values []V, size int) [][]V {
	if size <= 0 || len(values) <= size {
		return [][]V{values}
	}
	out := make([][]V, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		out = append(out, values[start:min(start+size, len(values))])
	}
	return out
}
