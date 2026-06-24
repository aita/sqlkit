package sqlkit

import (
	"context"
	"fmt"

	"github.com/aita/sqlkit/sql"
)

// columnSource is implemented by sources (generated tables) that can list
// their columns, so Select with no explicit columns can expand to them.
type columnSource interface {
	Columns() []AnyColumn
}

// SelectQuery is a typed SELECT builder. Columns and predicates are typed via
// Column[T], so type mismatches are compile errors. Results are materialized
// into a caller-defined destination with All(ctx).Scan(&dst).
//
// Build it from a Database or a Session — the chain is bound to that
// executor — then set the source with From before adding clauses:
//
//	db.Select().From(Users).Where(Users.ID.Gt(10)).All(ctx).Scan(&dst)
type SelectQuery struct {
	ex         executorBinding
	selections []sql.Selection
	hasCols    bool
	base       Table         // the From table, when the source is one; powers Filter/Get
	preloads   []preloadSpec // relations eager-loaded at Scan after the base rows
	folds      []foldSpec    // relations joined and folded into nested fields at Scan
	query      sql.SelectBuilder
	err        error // deferred build error (e.g. from Filter), surfaced at the terminal
	// batch holds a lone column IN back from q.query so All can split its
	// value list across statements when the bind-parameter limit would be
	// exceeded. finalQuery folds it back in for every other use.
	batch *batchIn
	// batchUnsafe records clauses (ORDER BY, GROUP BY, DISTINCT, LIMIT/OFFSET,
	// HAVING) whose semantics concatenating split results cannot preserve, so the
	// IN runs as a single statement instead.
	batchUnsafe bool
	// chunkSize/chunkOff are the per-query AutoChunk settings for splitting the
	// deferred IN at All: a positive chunkSize caps the values per statement
	// (also set internally for preload); chunkOff forces a single statement.
	chunkSize int
	chunkOff  bool
}

// Select starts an unbound SELECT builder for use as a subquery (FromSelect,
// Union) or with ToSQL. With no arguments it selects every column of the From
// source; otherwise it selects the given columns or expressions (use sql.As
// to alias projections). Executing an unbound chain returns an error; start
// from a Database or Session to run it.
func Select(selections ...sql.Selection) SelectQuery {
	return SelectQuery{selections: selections, hasCols: len(selections) > 0}
}

// From sets the table or subquery to select from. It must be called before
// the other clause methods. When Select was given no columns and the source
// can list its columns, they are selected explicitly; otherwise SELECT *.
func (q SelectQuery) From(source sql.Source) SelectQuery {
	selections := q.selections
	if !q.hasCols {
		selections = allColumnsOf(source)
	}
	q.query = sql.Select(selections...).From(source)
	if t, ok := source.(Table); ok {
		q.base = t // remembered so Filter and Get can reach its columns / primary key
	}
	return q
}

// fail records a deferred build error, keeping the first one. It surfaces at the
// terminal (All, Get, ToSQL, …), matching how the typed builders defer errors.
func (q SelectQuery) fail(err error) SelectQuery {
	if q.err == nil {
		q.err = err
	}
	return q
}

// Where adds predicates, combined with AND. A lone column IN whose value
// list is large enough to exceed the executor's bind-parameter limit is held
// back and split across statements by All; see WithAutoChunk.
func (q SelectQuery) Where(predicates ...Predicate) SelectQuery {
	flush, fold, deferred := resolveWhere(predicates, q.batch)
	if flush != nil {
		q.query = q.query.Where(flush)
	}
	if fold != nil {
		q.query = q.query.Where(fold)
	}
	q.batch = deferred
	return q
}

// AutoChunk configures how a lone WHERE column IN whose value list exceeds the
// executor's bind-parameter limit is split across statements by All: ChunkSize(n)
// caps each statement at n values, ChunkOff() runs it as one statement. The chunk
// rows are concatenated; ToSQL and the single-row terminals are unaffected.
func (q SelectQuery) AutoChunk(opts ...ChunkOption) SelectQuery {
	q.chunkSize, q.chunkOff = applyChunkOptions(q.chunkSize, q.chunkOff, opts)
	return q
}

// finalQuery folds any deferred IN back into the query, for every use other than
// All's splitting path (ToSQL, subqueries, single-row and counting reads).
func (q SelectQuery) finalQuery() sql.SelectBuilder {
	if q.batch == nil {
		return q.query
	}
	return q.query.Where(q.batch.exprFor(q.batch.values))
}

// Join adds an INNER JOIN.
func (q SelectQuery) Join(source sql.Source, on Predicate) SelectQuery {
	q.query = q.query.Join(source, on.Expression())
	return q
}

// LeftJoin adds a LEFT JOIN.
func (q SelectQuery) LeftJoin(source sql.Source, on Predicate) SelectQuery {
	q.query = q.query.LeftJoin(source, on.Expression())
	return q
}

// RightJoin adds a RIGHT JOIN.
func (q SelectQuery) RightJoin(source sql.Source, on Predicate) SelectQuery {
	q.query = q.query.RightJoin(source, on.Expression())
	return q
}

// FullJoin adds a FULL OUTER JOIN.
func (q SelectQuery) FullJoin(source sql.Source, on Predicate) SelectQuery {
	q.query = q.query.FullJoin(source, on.Expression())
	return q
}

// CrossJoin adds a CROSS JOIN, the Cartesian product of the inputs. It takes no
// ON condition.
func (q SelectQuery) CrossJoin(source sql.Source) SelectQuery {
	q.query = q.query.CrossJoin(source)
	return q
}

// JoinUsing adds an INNER JOIN ... USING (columns) on the equally named columns.
func (q SelectQuery) JoinUsing(source sql.Source, columns ...string) SelectQuery {
	q.query = q.query.JoinUsing(source, columns...)
	return q
}

// LeftJoinUsing adds a LEFT JOIN ... USING (columns).
func (q SelectQuery) LeftJoinUsing(source sql.Source, columns ...string) SelectQuery {
	q.query = q.query.LeftJoinUsing(source, columns...)
	return q
}

// NaturalJoin adds a NATURAL JOIN, an inner join on every column the sources
// share by name. It takes no condition.
func (q SelectQuery) NaturalJoin(source sql.Source) SelectQuery {
	q.query = q.query.NaturalJoin(source)
	return q
}

// NaturalLeftJoin adds a NATURAL LEFT JOIN.
func (q SelectQuery) NaturalLeftJoin(source sql.Source) SelectQuery {
	q.query = q.query.NaturalLeftJoin(source)
	return q
}

// RightJoinRel adds a RIGHT JOIN to a relation's target on its foreign-key
// condition.
func (q SelectQuery) RightJoinRel(rel Relation) SelectQuery {
	return q.RightJoin(rel.Target(), rel.JoinOn())
}

// FullJoinRel adds a FULL OUTER JOIN to a relation's target on its foreign-key
// condition.
func (q SelectQuery) FullJoinRel(rel Relation) SelectQuery {
	return q.FullJoin(rel.Target(), rel.JoinOn())
}

// JoinRel adds an INNER JOIN to a relation's target on its foreign-key
// condition, so callers never spell out the ON clause:
//
//	db.Select().From(Users).JoinRel(Users.Rels.Posts)
func (q SelectQuery) JoinRel(rel Relation) SelectQuery {
	return q.Join(rel.Target(), rel.JoinOn())
}

// LeftJoinRel adds a LEFT JOIN to a relation's target on its foreign-key
// condition.
func (q SelectQuery) LeftJoinRel(rel Relation) SelectQuery {
	return q.LeftJoin(rel.Target(), rel.JoinOn())
}

// Preload registers a related table to eager-load, optionally narrowed by
// conditions. The target is resolved to the From table's relation reaching it, so
// callers pass the target binding rather than the relation: Preload(Posts), not
// Preload(Users.Rels.Posts). The load is not a join; when a terminal's Scan runs,
// the relation is fetched in one batched follow-up query and assigned onto the
// scanned destination by reflection (matched to a field by the relation name or a
// `preload:"…"` tag), avoiding the N+1:
//
//	db.Select().From(Users).Preload(Posts).All(ctx).Scan(&users)
//
// Conditions are ANDed into the follow-up SELECT, so only matching children load:
//
//	db.Select().From(Users).Preload(Posts, Posts.Published.Eq(true))
//
// For a nested eager-load, pass a RelPath as the target — Preload then builds
// each hop, creating any intermediate level not already registered, and the
// conditions apply to the final hop (Users→Posts→Comments):
//
//	db.Select().From(Users).
//	    Preload(sqlkit.RelPath(Posts, Comments), Comments.Approved.Eq(true)).
//	    All(ctx).Scan(&users)
//
// Resolution errors (no From table, a table on the path exposes no relations, or
// no/ambiguous relation between two hops) are deferred to the terminal. When more
// than one relation reaches the same table (e.g. a self-reference's Manager and
// Reports), pick one explicitly with PreloadRel.
func (q SelectQuery) Preload(target Table, conditions ...Predicate) SelectQuery {
	if q.base == nil {
		return q.fail(fmt.Errorf("sqlkit: Preload requires From to be a table"))
	}
	path := []Table{target}
	if rp, ok := target.(relPath); ok {
		if len(rp.path) == 0 {
			return q.fail(fmt.Errorf("sqlkit: Preload: RelPath requires at least one table"))
		}
		path = rp.path
	}
	specs, err := applyPreloadPath(q.base, path, conditions, q.preloads)
	if err != nil {
		return q.fail(err)
	}
	q.preloads = specs
	return q
}

// PreloadRel registers a relation to eager-load by value, for when Preload's
// target-table lookup is ambiguous (several relations reach the same table) or a
// hand-built relation is used. It takes the same conditions, but its target is not
// resolved to a RelationSource, so it cannot be a hop on a RelPath.
func (q SelectQuery) PreloadRel(rel Relation, conditions ...Predicate) SelectQuery {
	q.preloads = append(append([]preloadSpec(nil), q.preloads...), preloadSpec{rel: rel, wheres: conditions})
	return q
}

// applyPreloadPath registers a preload for each hop of path under specs, resolving
// each table against the previous one's relations (path[0] against src). A hop
// that already has a spec is reused so a table is loaded once; conditions apply to
// the final hop. It returns a copy of specs with the path merged in.
func applyPreloadPath(src Table, path []Table, conditions []Predicate, specs []preloadSpec) ([]preloadSpec, error) {
	rs, ok := src.(RelationSource)
	if !ok {
		return nil, fmt.Errorf("sqlkit: Preload: %s exposes no relations", src.Info().QualifiedName())
	}
	target := path[0]
	rel, n := relationReaching(rs, target)
	switch {
	case n == 0:
		return nil, fmt.Errorf("sqlkit: Preload: %s has no relation to %s",
			src.Info().QualifiedName(), target.Info().QualifiedName())
	case n > 1:
		return nil, ambiguousRelation(src, target)
	}

	out := append([]preloadSpec(nil), specs...)
	idx := -1
	for i := range out {
		if out[i].target != nil && sameTable(out[i].target, target) {
			idx = i
			break
		}
	}
	if idx < 0 {
		out = append(out, preloadSpec{rel: rel, target: target})
		idx = len(out) - 1
	}
	s := out[idx]
	if len(path) == 1 {
		s.wheres = append(append([]Predicate(nil), s.wheres...), conditions...)
	} else {
		nested, err := applyPreloadPath(target, path[1:], conditions, s.nested)
		if err != nil {
			return nil, err
		}
		s.nested = nested
	}
	out[idx] = s
	return out, nil
}

// sameTable reports whether a and b name the same table (ignoring any alias).
func sameTable(a, b Table) bool {
	ai, bi := a.Info(), b.Info()
	return ai.Schema == bi.Schema && ai.Name == bi.Name
}

// Filter applies a reusable filter bundle against the From table's columns:
// either a Predicate, or a struct whose set fields map to columns by name and
// hold FilterTerm values (sqlkit.Le(20), …) or predicates. A mapping error is
// deferred to the terminal. It requires a From table (set Filter after From).
func (q SelectQuery) Filter(filter any) SelectQuery {
	if q.base == nil {
		return q.fail(fmt.Errorf("sqlkit: Filter requires From to be a table"))
	}
	predicates, err := buildFilter(q.base, filter)
	if err != nil {
		return q.fail(err)
	}
	return q.Where(predicates...)
}

// GroupBy adds GROUP BY expressions (columns or sql expressions).
func (q SelectQuery) GroupBy(exprs ...sql.Expression) SelectQuery {
	q.query = q.query.GroupBy(exprs...)
	q.batchUnsafe = true
	return q
}

// Having adds a HAVING predicate.
func (q SelectQuery) Having(predicate Predicate) SelectQuery {
	q.query = q.query.Having(predicate.Expression())
	q.batchUnsafe = true
	return q
}

// OrderBy adds ORDER BY terms.
func (q SelectQuery) OrderBy(terms ...sql.OrderTerm) SelectQuery {
	q.query = q.query.OrderBy(terms...)
	q.batchUnsafe = true
	return q
}

// Limit sets the LIMIT clause.
func (q SelectQuery) Limit(n int) SelectQuery {
	q.query = q.query.Limit(n)
	q.batchUnsafe = true
	return q
}

// Offset sets the OFFSET clause.
func (q SelectQuery) Offset(n int) SelectQuery {
	q.query = q.query.Offset(n)
	q.batchUnsafe = true
	return q
}

// LockOption refines a SELECT row-locking clause (ForUpdate / ForShare / …); use
// SkipLocked, NoWait, and LockOf.
type LockOption = sql.LockOption

// SkipLocked adds SKIP LOCKED, skipping rows another transaction has already
// locked rather than waiting (the queue-worker pattern).
func SkipLocked() LockOption { return sql.SkipLocked() }

// NoWait adds NOWAIT, erroring immediately instead of waiting on a locked row.
func NoWait() LockOption { return sql.NoWait() }

// LockOf restricts the lock to rows from the named tables/aliases
// (FOR UPDATE OF t).
func LockOf(tables ...string) LockOption { return sql.LockOf(tables...) }

// ForUpdate adds a FOR UPDATE row-locking clause, refined with the SkipLocked /
// NoWait / LockOf options. Row locking is PostgreSQL/MySQL (8.0+) only.
func (q SelectQuery) ForUpdate(opts ...LockOption) SelectQuery {
	q.query = q.query.ForUpdate(opts...)
	return q
}

// ForShare adds a FOR SHARE row-locking clause (a shared lock).
func (q SelectQuery) ForShare(opts ...LockOption) SelectQuery {
	q.query = q.query.ForShare(opts...)
	return q
}

// ForNoKeyUpdate adds a FOR NO KEY UPDATE clause (PostgreSQL only).
func (q SelectQuery) ForNoKeyUpdate(opts ...LockOption) SelectQuery {
	q.query = q.query.ForNoKeyUpdate(opts...)
	return q
}

// ForKeyShare adds a FOR KEY SHARE clause (PostgreSQL only).
func (q SelectQuery) ForKeyShare(opts ...LockOption) SelectQuery {
	q.query = q.query.ForKeyShare(opts...)
	return q
}

// Distinct marks the query as SELECT DISTINCT.
func (q SelectQuery) Distinct() SelectQuery {
	q.query = q.query.Distinct()
	q.batchUnsafe = true
	return q
}

// DistinctOn marks the query as SELECT DISTINCT ON (exprs...), keeping the first
// row of each group equal on the given expressions (PostgreSQL only). Pair it
// with an ORDER BY beginning with the same expressions to make "first"
// deterministic.
func (q SelectQuery) DistinctOn(exprs ...sql.Expression) SelectQuery {
	q.query = q.query.DistinctOn(exprs...)
	q.batchUnsafe = true
	return q
}

// Window adds named window definitions to the SELECT's WINDOW clause, referenced
// from an OVER clause with sql.Over(fn).Named(name). Build a definition with
// sql.WindowDef(name).PartitionBy(...).OrderBy(...).
func (q SelectQuery) Window(defs ...sql.NamedWindow) SelectQuery {
	q.query = q.query.Window(defs...)
	// A window spans the whole result set; concatenating per-chunk results would
	// recompute it wrong, so a deferred IN must not be split (an inline OVER is
	// caught separately by queryHasWindow at split time).
	q.batchUnsafe = true
	return q
}

// queryHasWindow reports whether q's AST carries a window function — an inline
// OVER in a projection or a named WINDOW definition. A window aggregates/ranks
// across the result set, so a query carrying one cannot be split into per-chunk
// statements: each chunk would compute the window over only its own rows.
func queryHasWindow(q sql.Query) bool {
	node, ok := sql.Unwrap(q).(sql.Node)
	if !ok {
		return false
	}
	found := false
	sql.Inspect(node, func(n sql.Node) bool {
		if found {
			return false
		}
		switch n.(type) {
		case sql.WindowExpression, sql.NamedWindow:
			found = true
			return false
		}
		return true
	})
	return found
}

// CTE wraps this SELECT as a common table expression named name, for use as a
// FROM source and column handle, and attached to a statement with WithCTE. Pass
// sql.Recursive() / sql.Columns(...) options to configure it.
func (q SelectQuery) CTE(name string, opts ...sql.CTEOption) sql.CTE {
	return q.finalQuery().CTE(name, opts...)
}

// WithCTE prepends common table expressions to the SELECT, emitting a leading
// WITH clause. Like the other clause methods it must be called after From.
func (q SelectQuery) WithCTE(ctes ...sql.CTE) SelectQuery {
	q.query = q.query.WithCTE(ctes...)
	return q
}

// As wraps this SELECT as an aliased subquery (derived table), for use as a
// FROM/JOIN source and column handle. Reference its output columns with the
// returned handle's Col, which qualifies them by the alias:
//
//	sub := sqlkit.Select(Users.ID).From(Users).Where(Users.Active.Eq(true)).As("sub")
//	db.Select(sub.Col("id")).From(sub).All(ctx).Scan(&ids)
func (q SelectQuery) As(alias string) sql.DerivedTable {
	return q.finalQuery().As(alias)
}

// SQLSource marks the query as a subquery source.
func (q SelectQuery) SQLSource() SourceExpression { return q.finalQuery().SQLSource() }

// ToSQL compiles the query. A deferred IN is folded into a single statement, so
// the compiled SQL always carries the full value list regardless of any
// configured bind-parameter limit (which only affects All's execution).
func (q SelectQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}
	return q.finalQuery().ToSQL(dialect)
}

// All prepares the query for execution against the executor the chain was
// built from. The query runs when Scan is called; build and execution errors
// surface there.
//
// When a lone column IN carries more values than the executor's
// bind-parameter limit allows in one statement, the value
// list is split across several statements and Scan concatenates their rows. The
// split is skipped, running a single statement, when the query also carries
// ORDER BY, GROUP BY, DISTINCT, HAVING, or LIMIT/OFFSET, whose semantics
// concatenation cannot preserve.
func (q SelectQuery) All(ctx context.Context, opts ...ExecOption) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	// Pass the builder (not finalQuery) so newRows can consult splitStatements; the
	// non-split path compiles SelectQuery.ToSQL, which folds the IN identically.
	return q.rows(ctx, q, opts)
}

// rows builds a Rows for a SELECT terminal: it resolves any WithRoute options
// into the execution binding and the Preload binding, runs the query through the
// former, and attaches the registered preloads to run through the latter.
func (q SelectQuery) rows(ctx context.Context, query sql.Query, opts []ExecOption) Rows {
	ex, preloadEx := applyExecOptions(q.ex, opts)
	rows := newRows(ctx, ex, query)
	rows.preloadEx = preloadEx
	return q.withFolds(q.withPreloads(rows))
}

// splitStatements implements splitter: it splits a deferred lone column IN across
// statements when its value list would exceed the executor's bind-parameter limit,
// concatenated at scan. It returns ok=false — running one statement — when no split
// applies (unsafe clauses present, ChunkOff, no deferred IN/limit, or the values
// fit). AutoChunk's ChunkSize additionally caps the values per statement. Reads are
// never writes.
func (q SelectQuery) splitStatements(ex executorBinding) ([]compiledStmt, bool, bool, error) {
	// A window function spans the whole result set, so splitting the IN across
	// statements would recompute it over each chunk's rows alone (ROW_NUMBER
	// restarting per chunk, etc.). queryHasWindow catches an inline OVER in the
	// projection, which the per-clause batchUnsafe flags cannot see.
	if q.batchUnsafe || q.chunkOff || queryHasWindow(q.query) {
		return nil, false, false, nil
	}
	dialect := ex.sqlDialect()
	stmts, ok, err := splitInStatements(ex, q.batch, q.chunkSize, func(in sql.Expression) (string, []any, error) {
		query := q.query
		if in != nil {
			query = query.Where(in)
		}
		return query.ToSQL(dialect)
	})
	return stmts, ok, false, err
}

// withPreloads attaches the registered preloads to a Rows so Scan eager-loads
// them after materializing the base rows.
func (q SelectQuery) withPreloads(rows Rows) Rows {
	rows.preloads = q.preloads
	return rows
}

// First is All limited to the first row: LIMIT 1 is pushed into the query,
// and Scan into a struct or scalar yields that row, or ErrNoRows when the
// result is empty.
func (q SelectQuery) First(ctx context.Context, opts ...ExecOption) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	return q.rows(ctx, q.finalQuery().Limit(1), opts)
}

// One is All restricted to exactly one row: Scan yields the row when the
// result has exactly one, ErrNoRows when it is empty, and ErrTooManyRows
// when there are more. LIMIT 2 is pushed into the query, so detecting the
// excess row never reads past the second.
func (q SelectQuery) One(ctx context.Context, opts ...ExecOption) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	ex, preloadEx := applyExecOptions(q.ex, opts)
	rows := newRows(ctx, ex, q.finalQuery().Limit(2))
	rows.exactlyOne = true
	rows.preloadEx = preloadEx
	return q.withPreloads(rows)
}

// Page is All over a LIMIT/OFFSET window: it pushes LIMIT limit OFFSET
// offset into the query and prepares it for execution, for paginated
// fetches without spelling out the clauses:
//
//	db.Select().From(Users).OrderBy(Users.ID.Asc()).Page(ctx, 20, 40).Scan(&users)
func (q SelectQuery) Page(ctx context.Context, limit, offset int, opts ...ExecOption) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	return q.rows(ctx, q.finalQuery().Limit(limit).Offset(offset), opts)
}

// Count runs SELECT COUNT(*) over this query (as a subquery, so joins,
// grouping, and predicates apply) and returns the number of matching rows.
func (q SelectQuery) Count(ctx context.Context, opts ...ExecOption) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	ex, _ := applyExecOptions(q.ex, opts)
	count := sql.Select(sql.CountAll()).From(sql.Alias(q.finalQuery(), "q"))
	var n int64
	err := newRows(ctx, ex, count).Scan(&n)
	return n, err
}

// Get fetches the row matching the From table's primary key and returns the
// first result. Pass one value per primary-key column, in declaration order. It
// requires a From table (set it before Get).
func (q SelectQuery) Get(ctx context.Context, pk ...any) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	if q.base == nil {
		return ErrRows(fmt.Errorf("sqlkit: Get requires From to be a table"))
	}
	key := q.base.PrimaryKey()
	if len(key) == 0 {
		return ErrRows(fmt.Errorf("sqlkit: Get on %s, which has no primary key", q.base.Info().QualifiedName()))
	}
	if len(pk) != len(key) {
		return ErrRows(fmt.Errorf("sqlkit: Get on %s expects %d primary-key value(s), got %d",
			q.base.Info().QualifiedName(), len(key), len(pk)))
	}
	predicates := make([]Predicate, len(key))
	for i, col := range key {
		predicates[i] = Cond(sql.Eq(col, sql.Val(pk[i])))
	}
	return q.Where(predicates...).First(ctx)
}

// Union combines this query with another using UNION. The result stays bound
// to this chain's executor.
func (q SelectQuery) Union(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.finalQuery().Union(other.finalQuery())}
}

// UnionAll combines this query with another using UNION ALL.
func (q SelectQuery) UnionAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.finalQuery().UnionAll(other.finalQuery())}
}

// Intersect combines this query with another using INTERSECT.
func (q SelectQuery) Intersect(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.finalQuery().Intersect(other.finalQuery())}
}

// IntersectAll combines this query with another using INTERSECT ALL, keeping
// duplicate rows.
func (q SelectQuery) IntersectAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.finalQuery().IntersectAll(other.finalQuery())}
}

// Except combines this query with another using EXCEPT.
func (q SelectQuery) Except(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.finalQuery().Except(other.finalQuery())}
}

// ExceptAll combines this query with another using EXCEPT ALL, keeping duplicate
// rows.
func (q SelectQuery) ExceptAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.finalQuery().ExceptAll(other.finalQuery())}
}

// Star is the "table.*" select-list item — every column of table, qualified by
// its name (or alias when the binding is aliased), like Column.Reference. Use it
// to project one table whole alongside individual columns of others, which a
// plain Select cannot express on its own:
//
//	db.Select(sqlkit.Star(Users), Profiles.Bio).
//		From(Users).JoinRel(Users.Rels.Profile).
//		All(ctx).Scan(&dst)
//
// compiles to SELECT "users".*, "profiles"."bio" FROM ... . For an unqualified
// "*" use sql.Star; selecting from a single table with no columns already emits
// every column without it (db.Select().From(Users)).
func Star(table Table) sql.Selection {
	return sql.StarOf(table.Info().refName())
}

func allColumnsOf(source sql.Source) []sql.Selection {
	if cs, ok := source.(columnSource); ok {
		return selectionsOf(cs.Columns())
	}
	return nil
}

func selectionsOf(columns []AnyColumn) []sql.Selection {
	selections := make([]sql.Selection, len(columns))
	for i, column := range columns {
		selections[i] = column
	}
	return selections
}
