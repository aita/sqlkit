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
	ex         Executor
	selections []sql.Selection
	hasCols    bool
	base       Table      // the From table, when the source is one; powers Filter/Get
	preloads   []Relation // relations eager-loaded at Scan after the base rows
	query      sql.SelectQuery
	err        error // deferred build error (e.g. from Filter), surfaced at the terminal
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

// Where adds predicates, combined with AND.
func (q SelectQuery) Where(predicates ...Predicate) SelectQuery {
	if expr := combineWhere(predicates); expr != nil {
		q.query = q.query.Where(expr)
	}
	return q
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

// Preload registers related tables to eager-load. Each target is resolved to
// the From table's relation reaching it, so callers pass the target binding
// rather than the relation: Preload(Posts), not Preload(Users.Rels.Posts). The
// load is not a join; when a terminal's Scan runs, each relation is fetched in
// one batched follow-up query and assigned onto the scanned destination by
// reflection (matched to a field by the relation name or a `preload:"…"` tag),
// avoiding the N+1:
//
//	db.Select().From(Users).Preload(Posts).All(ctx).Scan(&users)
//
// Resolution errors (no From table, the base exposes no relations, or no/ambiguous
// relation to a target) are deferred to the terminal. When more than one relation
// reaches the same table (e.g. a self-reference's Manager and Reports), pick one
// explicitly with PreloadRel.
func (q SelectQuery) Preload(targets ...Table) SelectQuery {
	preloads := append([]Relation(nil), q.preloads...)
	for _, target := range targets {
		rel, err := q.resolveRelation(target)
		if err != nil {
			return q.fail(err)
		}
		preloads = append(preloads, rel)
	}
	q.preloads = preloads
	return q
}

// PreloadRel registers relations to eager-load by value, for when Preload's
// target-table lookup is ambiguous (several relations reach the same table) or a
// hand-built relation is used. It behaves like Preload otherwise.
func (q SelectQuery) PreloadRel(rels ...Relation) SelectQuery {
	q.preloads = append(append([]Relation(nil), q.preloads...), rels...)
	return q
}

// resolveRelation finds the base table's relation whose target is t.
func (q SelectQuery) resolveRelation(t Table) (Relation, error) {
	if q.base == nil {
		return nil, fmt.Errorf("sqlkit: Preload requires From to be a table")
	}
	src, ok := q.base.(RelationSource)
	if !ok {
		return nil, fmt.Errorf("sqlkit: Preload: %s exposes no relations", q.base.Info().QualifiedName())
	}
	want := t.Info()
	var found Relation
	matches := 0
	for _, rel := range src.Relations() {
		if got := rel.Target().Info(); got.Schema == want.Schema && got.Name == want.Name {
			found, matches = rel, matches+1
		}
	}
	switch matches {
	case 0:
		return nil, fmt.Errorf("sqlkit: Preload: %s has no relation to %s",
			q.base.Info().QualifiedName(), want.QualifiedName())
	case 1:
		return found, nil
	default:
		return nil, fmt.Errorf("sqlkit: Preload: %s has %d relations to %s; pick one with PreloadRel",
			q.base.Info().QualifiedName(), matches, want.QualifiedName())
	}
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
	return q
}

// Having adds a HAVING predicate.
func (q SelectQuery) Having(predicate Predicate) SelectQuery {
	q.query = q.query.Having(predicate.Expression())
	return q
}

// OrderBy adds ORDER BY terms.
func (q SelectQuery) OrderBy(terms ...sql.OrderTerm) SelectQuery {
	q.query = q.query.OrderBy(terms...)
	return q
}

// Limit sets the LIMIT clause.
func (q SelectQuery) Limit(n int) SelectQuery {
	q.query = q.query.Limit(n)
	return q
}

// Offset sets the OFFSET clause.
func (q SelectQuery) Offset(n int) SelectQuery {
	q.query = q.query.Offset(n)
	return q
}

// Distinct marks the query as SELECT DISTINCT.
func (q SelectQuery) Distinct() SelectQuery {
	q.query = q.query.Distinct()
	return q
}

// CTE wraps this SELECT as a common table expression named name, for use as a
// FROM source and column handle, and attached to a statement with WithCTE. Pass
// sql.Recursive() / sql.Columns(...) options to configure it.
func (q SelectQuery) CTE(name string, opts ...sql.CTEOption) sql.CTE {
	return q.query.CTE(name, opts...)
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
	return q.query.As(alias)
}

// SQLSource marks the query as a subquery source.
func (q SelectQuery) SQLSource() SourceExpression { return q.query.SQLSource() }

// ToSQL compiles the query.
func (q SelectQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}
	return q.query.ToSQL(dialect)
}

// All prepares the query for execution against the executor the chain was
// built from. The query runs when Scan is called; build and execution errors
// surface there.
func (q SelectQuery) All(ctx context.Context) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	return q.withPreloads(newRows(ctx, q.ex, q.query))
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
func (q SelectQuery) First(ctx context.Context) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	return q.withPreloads(newRows(ctx, q.ex, q.query.Limit(1)))
}

// One is All restricted to exactly one row: Scan yields the row when the
// result has exactly one, ErrNoRows when it is empty, and ErrTooManyRows
// when there are more. LIMIT 2 is pushed into the query, so detecting the
// excess row never reads past the second.
func (q SelectQuery) One(ctx context.Context) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	rows := newRows(ctx, q.ex, q.query.Limit(2))
	rows.exactlyOne = true
	return q.withPreloads(rows)
}

// Page is All over a LIMIT/OFFSET window: it pushes LIMIT limit OFFSET
// offset into the query and prepares it for execution, for paginated
// fetches without spelling out the clauses:
//
//	db.Select().From(Users).OrderBy(Users.ID.Asc()).Page(ctx, 20, 40).Scan(&users)
func (q SelectQuery) Page(ctx context.Context, limit, offset int) Rows {
	if q.err != nil {
		return ErrRows(q.err)
	}
	return q.withPreloads(newRows(ctx, q.ex, q.query.Limit(limit).Offset(offset)))
}

// Count runs SELECT COUNT(*) over this query (as a subquery, so joins,
// grouping, and predicates apply) and returns the number of matching rows.
func (q SelectQuery) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	count := sql.Select(sql.CountAll()).From(sql.Alias(q.query, "q"))
	var n int64
	err := newRows(ctx, q.ex, count).Scan(&n)
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
	return UnionQuery{ex: q.ex, query: q.query.Union(other.query)}
}

// UnionAll combines this query with another using UNION ALL.
func (q SelectQuery) UnionAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.UnionAll(other.query)}
}

// Intersect combines this query with another using INTERSECT.
func (q SelectQuery) Intersect(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Intersect(other.query)}
}

// Except combines this query with another using EXCEPT.
func (q SelectQuery) Except(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Except(other.query)}
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
