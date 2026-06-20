package sql

// This file is the AST traversal surface of the package, modeled on go/ast.
//
// Walk is the general one: a Visitor and a single depth-first descent through
// every kind of node — statements, expressions, selections, the FROM/JOIN
// sources and clause structs that knit them together, and the queries embedded
// in expressions. Layered on top of it are the specialized, single-scope
// traversals callers reach for when they want less than the whole tree:
// WalkSubqueries / WalkSelectionSubqueries surface the subqueries embedded in one
// expression, WalkExpr / WalkSelectionExpr the expressions within one scope, and
// Decompose a query's immediate structural pieces. Each is a thin Walk visitor
// rather than its own recursion, so the AST shape lives in exactly one place
// (walkChildren).

// Visitor is invoked by Walk for each node it encounters. Visit returns the
// visitor w used to walk node's children: a non-nil w descends into the
// children and, once they are exhausted, Walk calls w.Visit(nil); a nil w prunes
// the subtree rooted at node. It mirrors the contract of go/ast.Visitor.
type Visitor interface {
	Visit(node Node) (w Visitor)
}

// Walk traverses the AST rooted at node in depth-first, pre-order, mirroring
// go/ast.Walk. It calls v.Visit(node); if the returned visitor w is non-nil it
// walks each child of node with w and then calls w.Visit(nil).
//
// It descends through the whole tree — a statement's clauses, the expressions
// and subqueries inside them, the FROM/JOIN sources, and on into nested queries
// — so a single visitor observes every node. A node kind with no modeled
// structure (a leaf such as a column or value, an unmodeled shape) is simply a
// stopping point with no children. node must satisfy Node; normalize a builder
// to its statement with Unwrap before walking.
func Walk(v Visitor, node Node) {
	if node == nil {
		return
	}
	if v = v.Visit(node); v == nil {
		return
	}
	walkChildren(v, node)
	v.Visit(nil)
}

// Inspect walks node in pre-order, calling f for each node. When f returns false
// Walk does not recurse into that node's children. It is the function-adapter
// form of Walk, the counterpart of go/ast.Inspect; f is also called once with a
// nil node after a node's children have been walked, so f must tolerate nil.
func Inspect(node Node, f func(Node) bool) {
	Walk(inspector(f), node)
}

type inspector func(Node) bool

func (f inspector) Visit(node Node) Visitor {
	if f(node) {
		return f
	}
	return nil
}

// walkExpr walks the child expression e, skipping a nil or position-less one.
func walkExpr(v Visitor, e Expression) {
	if n, ok := e.(Node); ok {
		Walk(v, n)
	}
}

// walkSelection walks the child selection s, skipping a nil or position-less one.
func walkSelection(v Visitor, s Selection) {
	if n, ok := s.(Node); ok {
		Walk(v, n)
	}
}

// walkQuery walks the child query q, normalizing a builder to its statement so
// the traversal handles only the statement node kinds.
func walkQuery(v Visitor, q Query) {
	if q == nil {
		return
	}
	if n, ok := astOf(q).(Node); ok {
		Walk(v, n)
	}
}

// walkSource walks the child source s through its compiled SourceExpression, the
// uniform structural view of a table, derived table, CTE reference, or subquery
// source.
func walkSource(v Visitor, s Source) {
	if s == nil {
		return
	}
	Walk(v, s.SQLSource())
}

func walkExprs(v Visitor, es []Expression) {
	for _, e := range es {
		walkExpr(v, e)
	}
}

func walkSelections(v Visitor, ss []Selection) {
	for _, s := range ss {
		walkSelection(v, s)
	}
}

func walkOrderBy(v Visitor, terms []OrderTerm) {
	for _, t := range terms {
		Walk(v, t)
	}
}

func walkRows(v Visitor, rows [][]Expression) {
	for _, row := range rows {
		walkExprs(v, row)
	}
}

// walkCTEs walks every CTE in the WITH list.
func walkCTEs(v Visitor, ctes []CTE) {
	for _, c := range ctes {
		Walk(v, c)
	}
}

// walkChildren walks every child of node, dispatching on its concrete type. A
// type not listed here is a leaf: it has no nested node to descend into.
func walkChildren(v Visitor, node Node) {
	switch n := node.(type) {

	// --- statements ---
	case SelectStmt:
		walkCTEs(v, n.With)
		walkSource(v, n.From)
		for _, j := range n.Joins {
			Walk(v, j)
		}
		walkSelections(v, n.Columns)
		walkExpr(v, n.Where)
		walkExprs(v, n.GroupBy)
		walkExpr(v, n.Having)
		walkExprs(v, n.DistinctOn)
		walkOrderBy(v, n.OrderBy)
		for _, w := range n.Windows {
			Walk(v, w)
		}
	case CompoundStmt:
		walkQuery(v, n.Left)
		walkQuery(v, n.Right)
		walkOrderBy(v, n.OrderBy)
	case InsertStmt:
		Walk(v, n.Table)
		walkRows(v, n.Rows)
		walkQuery(v, n.Select)
		if n.Conflict != nil {
			Walk(v, *n.Conflict)
		}
		walkSelections(v, n.Returning)
	case UpdateStmt:
		walkCTEs(v, n.With)
		Walk(v, n.Table)
		for _, a := range n.Sets {
			Walk(v, a)
		}
		for _, s := range n.From {
			walkSource(v, s)
		}
		for _, j := range n.Joins {
			Walk(v, j)
		}
		walkExpr(v, n.Where)
		walkOrderBy(v, n.OrderBy)
		walkSelections(v, n.Returning)
	case DeleteStmt:
		walkCTEs(v, n.With)
		Walk(v, n.Table)
		for _, s := range n.Using {
			walkSource(v, s)
		}
		for _, j := range n.Joins {
			Walk(v, j)
		}
		walkExpr(v, n.Where)
		walkOrderBy(v, n.OrderBy)
		walkSelections(v, n.Returning)
	case MergeStmt:
		walkCTEs(v, n.With)
		Walk(v, n.Target)
		walkSource(v, n.Using)
		walkExpr(v, n.On)
		for _, w := range n.When {
			Walk(v, w)
		}
		walkSelections(v, n.Returning)
	case ValuesQuery:
		walkRows(v, n.rows)

	// --- clause structs ---
	case CTE:
		walkQuery(v, n.query)
	case JoinClause:
		walkSource(v, n.Source)
		walkExpr(v, n.On)
	case OrderTerm:
		walkExpr(v, n.Expr)
	case Assignment:
		walkExpr(v, n.Target)
		walkExpr(v, n.Value)
	case WhenClause:
		walkExpr(v, n.Condition)
		walkExpr(v, n.Result)
	case NamedWindow:
		walkExprs(v, n.Partitions)
		walkOrderBy(v, n.OrderByTerms)
	case ConflictClause:
		walkExprs(v, n.TargetExprs)
		for _, a := range n.Sets {
			Walk(v, a)
		}
		walkExpr(v, n.Where)
	case MergeWhenClause:
		walkExpr(v, n.Condition)
		for _, a := range n.Sets {
			Walk(v, a)
		}
		walkExprs(v, n.InsertValues)
	case SourceExpression:
		walkQuery(v, n.Query)
		if n.Values != nil {
			walkRows(v, n.Values.Rows)
		}

	// --- expressions: composite operands ---
	case BinaryExpression:
		walkExpr(v, n.Left)
		walkExpr(v, n.Right)
	case UnaryExpression:
		walkExpr(v, n.Operand)
	case LogicalExpression:
		walkExprs(v, n.Operands)
	case NotExpression:
		walkExpr(v, n.Operand)
	case NullPredicate:
		walkExpr(v, n.Operand)
	case DistinctPredicate:
		walkExpr(v, n.Left)
		walkExpr(v, n.Right)
	case BetweenExpression:
		walkExpr(v, n.Operand)
		walkExpr(v, n.Low)
		walkExpr(v, n.High)
	case ConcatExpression:
		walkExprs(v, n.Operands)
	case ILikeExpression:
		walkExpr(v, n.Left)
		walkExpr(v, n.Pattern)
	case RegexpExpression:
		walkExpr(v, n.Left)
		walkExpr(v, n.Pattern)
	case SimilarToExpression:
		walkExpr(v, n.Left)
		walkExpr(v, n.Pattern)
	case RowExpression:
		walkExprs(v, n.Elements)
	case ArrayExpression:
		walkExprs(v, n.Elements)
	case QuantifiedArrayExpression:
		walkExpr(v, n.Left)
		walkExpr(v, n.Array)
	case GroupingExpression:
		walkExprs(v, n.Elements)
	case GroupingSetsExpression:
		for _, set := range n.Sets {
			walkExprs(v, set)
		}
	case CaseExpression:
		walkExpr(v, n.Operand)
		for _, w := range n.Whens {
			Walk(v, w)
		}
		walkExpr(v, n.ElseValue)
	case WindowExpression:
		walkExpr(v, n.Fn)
		walkExprs(v, n.Partitions)
		walkOrderBy(v, n.OrderByTerms)
	case FuncExpression:
		walkExprs(v, n.Args)
	case CountExpression:
		walkSelection(v, n.Target)
	case StringAggExpression:
		walkExpr(v, n.Arg)
	case ExtractExpression:
		walkExpr(v, n.Operand)
	case PositionExpression:
		walkExpr(v, n.Substring)
		walkExpr(v, n.String)
	case CastExpression:
		walkExpr(v, n.Operand)
	case CollateExpression:
		walkExpr(v, n.Operand)
	case FieldExpression:
		walkExpr(v, n.Operand)
	case SubscriptExpression:
		walkExpr(v, n.Operand)
		for _, idx := range n.Indices {
			walkExpr(v, idx.Lower)
			walkExpr(v, idx.Upper)
		}
	case JSONObjectExpression:
		walkExprs(v, n.Pairs)
	case JSONAccessExpression:
		walkExpr(v, n.Operand)
		for _, p := range n.Path {
			if e, ok := p.(Expression); ok {
				walkExpr(v, e)
			}
		}

	// --- expressions: embedded subqueries ---
	case InExpression:
		walkExpr(v, n.Left)
		walkExprs(v, n.Items)
		walkQuery(v, n.Subquery)
	case ExistsExpression:
		walkQuery(v, n.Query)
	case ScalarSubqueryExpression:
		walkQuery(v, n.Query)
	case QuantifiedExpression:
		walkExpr(v, n.Left)
		walkQuery(v, n.Query)

	// --- selections ---
	case AliasedSelection:
		walkSelection(v, n.Selection)

	// --- DDL ---
	case CreateTableStmt:
		Walk(v, n.Table)
		for _, c := range n.Columns {
			Walk(v, c)
		}
		for _, ck := range n.Checks {
			Walk(v, ck)
		}
	case AlterTableStmt:
		Walk(v, n.Table)
		for _, a := range n.Alterations {
			Walk(v, a)
		}
	case ColumnDef:
		walkExpr(v, n.Default)
		walkExpr(v, n.OnUpdate)
		if n.Generated != nil {
			walkExpr(v, n.Generated.Expression)
		}
	case TableCheck:
		walkExpr(v, n.Expression)
	case Alteration:
		if n.Column != nil {
			Walk(v, *n.Column)
		}
		walkExpr(v, n.Expression)
	case CreateViewStmt:
		Walk(v, n.Table)
		walkQuery(v, n.Query)
	case CreateMaterializedViewStmt:
		Walk(v, n.Table)
		walkQuery(v, n.Query)
	case CreateIndexStmt:
		Walk(v, n.Table)
	case DropTableStmt:
		for _, t := range n.Tables {
			Walk(v, t)
		}
	case TruncateStmt:
		for _, t := range n.Tables {
			Walk(v, t)
		}
	case SetStmt:
		for _, sv := range n.Vars {
			Walk(v, sv)
		}
	case SetVar:
		walkExpr(v, n.Value)
	case LockTablesStmt:
		for _, l := range n.Locks {
			Walk(v, l)
		}
	case TableLock:
		Walk(v, n.Table)

	default:
		// Leaf node: a column, value, raw fragment, star, excluded reference, a
		// table reference, or a DDL statement that nests no expression or query —
		// nothing further to descend into.
	}
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
	if n, ok := e.(Node); ok {
		walkScopeSubqueries(n, visit)
	}
}

// WalkSelectionSubqueries is WalkSubqueries for a SELECT-list item, looking
// through the selection-only nodes (an AS alias, COUNT's target) that are not
// themselves Expressions to reach the subqueries inside.
func WalkSelectionSubqueries(s Selection, visit func(Query)) {
	if n, ok := s.(Node); ok {
		walkScopeSubqueries(n, visit)
	}
}

// walkScopeSubqueries walks root and calls visit for each subquery embedded in
// the expressions reachable from it, pruning at each subquery so a found query's
// own structure is left to the caller. It is the shared engine of WalkSubqueries
// and WalkSelectionSubqueries.
func walkScopeSubqueries(root Node, visit func(Query)) {
	Inspect(root, func(node Node) bool {
		if q, ok := node.(Query); ok {
			visit(q)
			return false // a subquery is a separate scope; do not descend into it
		}
		return true
	})
}

// WalkExpr visits e and every expression nested within its own operands, in
// pre-order. It does NOT descend into embedded subqueries (a ScalarSubquery, or
// the subquery of an IN/EXISTS/quantified predicate): each subquery is a separate
// scope, walked on its own (see Walk and WalkSubqueries).
//
// Where Walk traverses the whole tree and WalkSubqueries finds the subqueries
// embedded in an expression, WalkExpr is the expression-level traversal within a
// single scope: analysis tools build on it to collect column references or
// inspect predicates inside one clause.
func WalkExpr(e Expression, visit func(Expression)) {
	if n, ok := e.(Node); ok {
		walkScopeExprs(n, visit)
	}
}

// WalkSelectionExpr walks the expression carried by a SELECT-list item, looking
// through an AS alias. A "*" selection carries no expression. It is WalkExpr for
// a Selection, paralleling WalkSelectionSubqueries.
func WalkSelectionExpr(s Selection, visit func(Expression)) {
	if n, ok := s.(Node); ok {
		walkScopeExprs(n, visit)
	}
}

// walkScopeExprs walks root and calls visit for each expression reachable from
// it within a single scope — descending through operands but pruning at each
// embedded subquery, a separate scope. It is the shared engine of WalkExpr and
// WalkSelectionExpr.
func walkScopeExprs(root Node, visit func(Expression)) {
	Inspect(root, func(node Node) bool {
		if _, ok := node.(Query); ok {
			return false // a subquery is a separate scope; do not descend into it
		}
		if e, ok := node.(Expression); ok {
			visit(e)
		}
		return true
	})
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
// It does not recurse into nested queries: it is a single-level Walk that prunes
// at each child query, so callers drive the deeper recursion. Any callback may be
// nil. It is the public, zero-copy structural seam analysis tools build on
// instead of the clause-copying Parts accessors.
func Decompose(q Query, onCTEName func(string), onName func(string), onChild func(Query)) {
	onCTEName, onName, onChild = orNoopString(onCTEName), orNoopString(onName), orNoopQuery(onChild)
	root, ok := astOf(q).(Node)
	if !ok {
		return
	}
	atRoot := true
	Inspect(root, func(node Node) bool {
		if node == nil {
			return true
		}
		if atRoot {
			atRoot = false
			return true // descend into the root query's own clauses
		}
		switch x := node.(type) {
		case Query:
			onChild(x)
			return false // an immediate child query; stop before its own structure
		case CTE:
			onCTEName(x.name.String())
			return true // descend to surface the CTE body as a child query
		case SourceExpression:
			onName(x.Name.String())
			return true // descend to surface a subquery source as a child query
		case Table:
			onName(x.QualifiedName())
			return true
		default:
			return true
		}
	})
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
