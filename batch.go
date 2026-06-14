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

// splitter is implemented by a query builder that can split itself into several
// compiled statements run in place of one — automatic statement splitting is the
// only implementation. The run path consults it once: runExec runs the statements
// atomically and merges their Result; newRows concatenates their rows at scan.
// ok is false when no split applies, running the statement unchanged. write tells
// newRows the chunks are writes (RETURNING — a writable atomic transaction) rather
// than reads (SELECT — an optional read snapshot).
type splitter interface {
	splitStatements(ex executorBinding) (stmts []compiledStmt, ok bool, write bool, err error)
}

// chunkConfig holds a query's per-call auto-chunking settings, set by AutoChunk.
type chunkConfig struct {
	size int  // max items per statement (rows for INSERT, IN values for SELECT/UPDATE/DELETE)
	off  bool // force a single statement even when auto-split is enabled
}

// ChunkOption configures a query's automatic statement splitting; pass it to a
// builder's AutoChunk.
type ChunkOption func(*chunkConfig)

// ChunkSize caps each split statement at n items — rows for a bulk INSERT, IN-list
// values for a SELECT/UPDATE/DELETE. It only lowers the chunk below the dialect's
// bind-parameter limit; it never raises it past the limit.
func ChunkSize(n int) ChunkOption { return func(c *chunkConfig) { c.size = n } }

// ChunkOff forces the statement to run as a single statement, disabling automatic
// splitting for this query even when it is enabled on the executor.
func ChunkOff() ChunkOption { return func(c *chunkConfig) { c.off = true } }

// applyChunkOptions folds opts onto the builder's current chunk settings.
func applyChunkOptions(size int, off bool, opts []ChunkOption) (int, bool) {
	c := chunkConfig{size: size, off: off}
	for _, opt := range opts {
		opt(&c)
	}
	return c.size, c.off
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
func execStatements(ctx context.Context, ex executorBinding, stmts []compiledStmt) (stdsql.Result, error) {
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

func execOn(ctx context.Context, ex executorBinding, stmts []compiledStmt) (stdsql.Result, error) {
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

// resolveBindParamLimit returns the dialect's per-statement bind-parameter limit
// (the BindParamLimiter capability — 65535 for PostgreSQL and MySQL), which drives
// automatic statement splitting. It is 0 when the dialect does not report one, in
// which case nothing splits automatically.
func resolveBindParamLimit(dialect sql.Dialect) int {
	if limiter, ok := dialect.(sql.BindParamLimiter); ok {
		return limiter.MaxBindParams()
	}
	return 0
}

// splitInStatements splits a deferred column IN whose value list would exceed
// ex's bind-parameter limit into one compiled statement per chunk, or reports
// ok=false when no split applies (no batch, no executor, no limit, or the values
// already fit). compile renders the statement carrying the given WHERE-IN
// expression; it is called once with nil to compile the IN-free base and size
// its other parameters against the limit.
//
// maxChunk, when positive, additionally caps the values per chunk (the per-query
// AutoChunk size or the preload default) — it can only make chunks smaller than
// the bind-parameter limit allows, never larger.
func splitInStatements(ex executorBinding, batch *batchIn, maxChunk int, compile func(in sql.Expression) (string, []any, error)) (stmts []compiledStmt, ok bool, err error) {
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
	if maxChunk > 0 && maxChunk < per {
		per = maxChunk
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
// stays within q's configured bind-parameter limit, runs each chunk, and
// concatenates the scanned rows into dest (a pointer to a slice). It is the
// manual counterpart to the automatic IN-list chunking the typed builders apply
// to a column's In: reach for it when the query the builder cannot split
// on its own — a hand-built IN, or one combined with ORDER BY/LIMIT — must still
// run a large value list. build receives each chunk and should return the Rows
// for it (typically q.Select()...Where(col.In(chunk...)).All(ctx)).
//
// q is the *Database or *Session the per-chunk queries run against. chunkSize
// defaults to its bind-parameter limit; pass a positive override to cap it lower
// (e.g. to leave room for the query's other parameters). With no limit available
// and no override, every value goes in a single chunk.
func InBatches[V any](ctx context.Context, q Querier, values []V, build func(chunk []V) Rows, dest any, chunkSize ...int) error {
	size := 0
	if b, ok := q.(executorBinding); ok {
		size = b.maxBindParams()
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
