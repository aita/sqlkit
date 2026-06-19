package sql

// Walk traverses the query tree rooted at q in depth-first, pre-order: it calls
// visit for q and then, unless visit returns false, for every query nested
// within it — FROM/JOIN subqueries, set-operation arms, CTE bodies, and the
// subqueries embedded in WHERE/HAVING/SELECT-list and other expressions
// (correlated ones included). Returning false from visit prunes the children of
// that query without stopping the rest of the walk.
//
// Walk reads structure only; it never compiles or mutates a query. With the
// Clauses accessors (which expose a node's own clauses) and WalkSubqueries (which
// finds the subqueries inside a single expression), it is the general-purpose
// traversal surface of the AST: analysis tools build on these to collect tables,
// detect patterns, or gather statistics across a whole statement.
func Walk(q Query, visit func(q Query) bool) {
	if q == nil || !visit(q) {
		return
	}
	queryChildren(q, func(string) {}, func(string) {}, func(child Query) { Walk(child, visit) })
}

// WalkSubqueries calls visit for every subquery embedded in e and its
// sub-expressions: the subquery predicates (IN (subquery), EXISTS, scalar
// subqueries, and quantified ANY/ALL comparisons) wherever they appear, even
// nested inside composite predicates, CASE arms, function arguments, or window
// clauses. It is the basis for finding correlated and uncorrelated subqueries in
// WHERE / HAVING / SELECT-list and other expression positions.
//
// visit is called with each embedded query but WalkSubqueries does not descend
// into that query's own structure — the caller decides whether to recurse (for
// example via Walk). e may be nil.
func WalkSubqueries(e Expression, visit func(Query)) {
	switch x := e.(type) {
	case nil:
		return

	// --- subquery-bearing predicates ---
	case InExpression:
		WalkSubqueries(x.Left, visit)
		for _, it := range x.Items {
			WalkSubqueries(it, visit)
		}
		if x.Subquery != nil {
			visit(x.Subquery)
		}
	case ExistsExpression:
		if x.Query != nil {
			visit(x.Query)
		}
	case ScalarSubqueryExpression:
		if x.Query != nil {
			visit(x.Query)
		}
	case QuantifiedExpression:
		WalkSubqueries(x.Left, visit)
		if x.Query != nil {
			visit(x.Query)
		}

	// --- composite expressions: recurse into operands ---
	case BinaryExpression:
		WalkSubqueries(x.Left, visit)
		WalkSubqueries(x.Right, visit)
	case UnaryExpression:
		WalkSubqueries(x.Operand, visit)
	case RowExpression:
		walkEach(x.Elements, visit)
	case DistinctPredicate:
		WalkSubqueries(x.Left, visit)
		WalkSubqueries(x.Right, visit)
	case ConcatExpression:
		walkEach(x.Operands, visit)
	case ILikeExpression:
		WalkSubqueries(x.Left, visit)
		WalkSubqueries(x.Pattern, visit)
	case RegexpExpression:
		WalkSubqueries(x.Left, visit)
		WalkSubqueries(x.Pattern, visit)
	case SimilarToExpression:
		WalkSubqueries(x.Left, visit)
		WalkSubqueries(x.Pattern, visit)
	case BetweenExpression:
		WalkSubqueries(x.Operand, visit)
		WalkSubqueries(x.Low, visit)
		WalkSubqueries(x.High, visit)
	case LogicalExpression:
		walkEach(x.Operands, visit)
	case NotExpression:
		WalkSubqueries(x.Operand, visit)
	case NullPredicate:
		WalkSubqueries(x.Operand, visit)
	case ArrayExpression:
		walkEach(x.Elements, visit)
	case QuantifiedArrayExpression:
		WalkSubqueries(x.Left, visit)
		WalkSubqueries(x.Array, visit)
	case CaseExpression:
		WalkSubqueries(x.Operand, visit)
		for _, w := range x.Whens {
			WalkSubqueries(w.Condition, visit)
			WalkSubqueries(w.Result, visit)
		}
		WalkSubqueries(x.ElseValue, visit)
	case WindowExpression:
		WalkSubqueries(x.Fn, visit)
		walkEach(x.Partitions, visit)
		walkOrderTerms(x.OrderByTerms, visit)

	// --- function / aggregate nodes ---
	case FuncExpression:
		walkEach(x.Args, visit)
	case CountExpression:
		WalkSelectionSubqueries(x.Target, visit)
	case StringAggExpression:
		WalkSubqueries(x.Arg, visit)
	case ExtractExpression:
		WalkSubqueries(x.Operand, visit)
	case PositionExpression:
		WalkSubqueries(x.Substring, visit)
		WalkSubqueries(x.String, visit)
	case CastExpression:
		WalkSubqueries(x.Operand, visit)
	case JSONObjectExpression:
		walkEach(x.Pairs, visit)
	case JSONAccessExpression:
		WalkSubqueries(x.Operand, visit)

	default:
		// Leaf nodes (ColumnExpression, ValueExpression, RawExpression,
		// ExcludedExpression, ...) hold no nested query.
	}
}

// WalkSelectionSubqueries is WalkSubqueries for a SELECT-list item, unwrapping
// the selection-only nodes (an AS alias, COUNT's target) that are not themselves
// Expressions before delegating to WalkSubqueries.
func WalkSelectionSubqueries(s Selection, visit func(Query)) {
	switch x := s.(type) {
	case nil:
		return
	case AliasedSelection:
		WalkSelectionSubqueries(x.Selection, visit)
	case Expression:
		WalkSubqueries(x, visit)
	default:
		// StarSelection and any other non-expression selection hold no subquery.
	}
}

// walkEach walks every expression in es.
func walkEach(es []Expression, visit func(Query)) {
	for _, e := range es {
		WalkSubqueries(e, visit)
	}
}

// walkOrderTerms walks the expression of every ORDER BY term.
func walkOrderTerms(terms []OrderTerm, visit func(Query)) {
	for _, t := range terms {
		WalkSubqueries(t.Expr, visit)
	}
}

// Decompose reports q's immediate structural pieces for read-only analysis,
// without copying a snapshot of its clauses:
//
//   - onCTEName: each CTE name q declares in its own WITH clause, the scope a
//     FROM/JOIN reference resolves against (a name in scope denotes that CTE, not
//     a base table).
//   - onName: each base-table (or CTE-reference) name q references directly
//     through a FROM/JOIN/target source.
//   - onChild: each query nested one level inside q — CTE bodies, subquery
//     sources, set-operation arms, and the subqueries embedded in its
//     expressions.
//
// It does not recurse into nested queries; callers drive the recursion (see
// Walk). Any callback may be nil. It is the public, zero-copy structural seam
// analysis tools build on instead of the clause-copying Parts accessors.
func Decompose(q Query, onCTEName func(string), onName func(string), onChild func(Query)) {
	queryChildren(q, orNoopString(onCTEName), orNoopString(onName), orNoopQuery(onChild))
}

func orNoopString(f func(string)) func(string) {
	if f == nil {
		return func(string) {}
	}
	return f
}

func orNoopQuery(f func(Query)) func(Query) {
	if f == nil {
		return func(Query) {}
	}
	return f
}

// queryChildren is the in-package engine behind Decompose and Walk: it reports
// the CTE names q declares (onCTEName), the base-table names it references
// directly (onName), and the queries nested one level inside it (onQuery),
// reading q's unexported fields directly so nothing is copied. CTE bodies are
// reported first so a derived ordering follows source order.
func queryChildren(q Query, onCTEName func(string), onName func(string), onQuery func(Query)) {
	src := func(s Source) {
		if s == nil {
			return
		}
		e := s.SQLSource()
		onName(e.Name)
		if e.Query != nil {
			onQuery(e.Query)
		}
	}
	expr := func(e Expression) { WalkSubqueries(e, onQuery) }
	sel := func(s Selection) { WalkSelectionSubqueries(s, onQuery) }
	ctes := func(cs []CTE) {
		for _, c := range cs {
			onCTEName(c.name)
			if c.query != nil {
				onQuery(c.query)
			}
		}
	}
	orderTerms := func(terms []OrderTerm) {
		for _, term := range terms {
			expr(term.Expr)
		}
	}

	switch v := astOf(q).(type) {
	case SelectStmt:
		ctes(v.With)
		src(v.From)
		for _, j := range v.Joins {
			src(j.Source)
			expr(j.On)
		}
		for _, s := range v.Columns {
			sel(s)
		}
		expr(v.Where)
		expr(v.Having)
		for _, e := range v.GroupBy {
			expr(e)
		}
		for _, e := range v.DistinctOn {
			expr(e)
		}
		orderTerms(v.OrderBy)
		for _, w := range v.Windows {
			for _, e := range w.Partitions {
				expr(e)
			}
			orderTerms(w.OrderByTerms)
		}
	case CompoundStmt:
		if v.Left != nil {
			onQuery(v.Left)
		}
		if v.Right != nil {
			onQuery(v.Right)
		}
		orderTerms(v.OrderBy)
	case InsertQuery:
		onName(v.table.QualifiedName())
		if v.selectSrc != nil {
			onQuery(v.selectSrc)
		}
		for _, row := range v.rows {
			for _, e := range row {
				expr(e)
			}
		}
		if v.conflict != nil {
			for _, a := range v.conflict.sets {
				expr(a.Value)
			}
			expr(v.conflict.where)
		}
		for _, s := range v.returning {
			sel(s)
		}
	case UpdateQuery:
		ctes(v.with)
		onName(v.table.QualifiedName())
		for _, s := range v.from {
			src(s)
		}
		for _, j := range v.joins {
			src(j.Source)
			expr(j.On)
		}
		for _, a := range v.sets {
			expr(a.Value)
		}
		expr(v.where)
		for _, s := range v.returning {
			sel(s)
		}
	case DeleteQuery:
		ctes(v.with)
		onName(v.table.QualifiedName())
		for _, s := range v.using {
			src(s)
		}
		for _, j := range v.joins {
			src(j.Source)
			expr(j.On)
		}
		expr(v.where)
		for _, s := range v.returning {
			sel(s)
		}
	case MergeQuery:
		ctes(v.with)
		onName(v.target.QualifiedName())
		src(v.using)
		expr(v.on)
		for _, w := range v.whens {
			expr(w.condition)
			for _, a := range w.sets {
				expr(a.Value)
			}
			for _, e := range w.insertValues {
				expr(e)
			}
		}
		for _, s := range v.returning {
			sel(s)
		}
	default:
		// ValuesQuery and DDL statements have no nested queries.
	}
}
