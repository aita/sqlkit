package analyze

import "github.com/aita/sqlkit/sql"

// WalkExpr visits e and every expression nested within its own operands, in
// pre-order. It does NOT descend into embedded subqueries (a ScalarSubquery, or
// the subquery of an IN/EXISTS/quantified predicate): each subquery is a separate
// scope, walked on its own. Unrecognized nodes are treated as leaves, so the walk
// degrades gracefully on shapes it does not model rather than panicking.
//
// It is the expression-level traversal the rest of the package builds on —
// FilterColumns and MaxInList over a WHERE clause, WalkColumns over a whole
// statement.
func WalkExpr(e sql.Expression, visit func(sql.Expression)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case sql.BinaryExpression:
		WalkExpr(x.Left, visit)
		WalkExpr(x.Right, visit)
	case sql.LogicalExpression:
		walkEachExpr(x.Operands, visit)
	case sql.NotExpression:
		WalkExpr(x.Operand, visit)
	case sql.UnaryExpression:
		WalkExpr(x.Operand, visit)
	case sql.NullPredicate:
		WalkExpr(x.Operand, visit)
	case sql.DistinctPredicate:
		WalkExpr(x.Left, visit)
		WalkExpr(x.Right, visit)
	case sql.BetweenExpression:
		WalkExpr(x.Operand, visit)
		WalkExpr(x.Low, visit)
		WalkExpr(x.High, visit)
	case sql.InExpression:
		WalkExpr(x.Left, visit)
		walkEachExpr(x.Items, visit)
	case sql.QuantifiedExpression:
		WalkExpr(x.Left, visit)
	case sql.QuantifiedArrayExpression:
		WalkExpr(x.Left, visit)
		WalkExpr(x.Array, visit)
	case sql.ILikeExpression:
		WalkExpr(x.Left, visit)
		WalkExpr(x.Pattern, visit)
	case sql.RegexpExpression:
		WalkExpr(x.Left, visit)
		WalkExpr(x.Pattern, visit)
	case sql.SimilarToExpression:
		WalkExpr(x.Left, visit)
		WalkExpr(x.Pattern, visit)
	case sql.ConcatExpression:
		walkEachExpr(x.Operands, visit)
	case sql.RowExpression:
		walkEachExpr(x.Elements, visit)
	case sql.ArrayExpression:
		walkEachExpr(x.Elements, visit)
	case sql.FuncExpression:
		walkEachExpr(x.Args, visit)
	case sql.CastExpression:
		WalkExpr(x.Operand, visit)
	case sql.CollateExpression:
		WalkExpr(x.Operand, visit)
	case sql.FieldExpression:
		WalkExpr(x.Operand, visit)
	case sql.SubscriptExpression:
		WalkExpr(x.Operand, visit)
		for _, idx := range x.Indices {
			WalkExpr(idx.Lower, visit)
			WalkExpr(idx.Upper, visit)
		}
	case sql.StringAggExpression:
		WalkExpr(x.Arg, visit)
	case sql.ExtractExpression:
		WalkExpr(x.Operand, visit)
	case sql.PositionExpression:
		WalkExpr(x.Substring, visit)
		WalkExpr(x.String, visit)
	case sql.JSONObjectExpression:
		walkEachExpr(x.Pairs, visit)
	case sql.JSONAccessExpression:
		WalkExpr(x.Operand, visit)
		for _, p := range x.Path {
			if pe, ok := p.(sql.Expression); ok {
				WalkExpr(pe, visit)
			}
		}
	case sql.GroupingExpression:
		walkEachExpr(x.Elements, visit)
	case sql.GroupingSetsExpression:
		for _, set := range x.Sets {
			walkEachExpr(set, visit)
		}
	case sql.CountExpression:
		walkSelectionExpr(x.Target, visit)
	case sql.WindowExpression:
		WalkExpr(x.Fn, visit)
		walkEachExpr(x.Partitions, visit)
		for _, term := range x.OrderByTerms {
			WalkExpr(term.Expr, visit)
		}
	case sql.CaseExpression:
		WalkExpr(x.Operand, visit)
		for _, w := range x.Whens {
			WalkExpr(w.Condition, visit)
			WalkExpr(w.Result, visit)
		}
		WalkExpr(x.ElseValue, visit)
	default:
		// Leaf nodes (ColumnExpression, ValueExpression, RawExpression,
		// ExcludedExpression, an embedded subquery, ...) carry no further operands
		// in this scope.
	}
}

func walkEachExpr(es []sql.Expression, visit func(sql.Expression)) {
	for _, e := range es {
		WalkExpr(e, visit)
	}
}

// walkSelectionExpr walks the expression carried by a SELECT-list item, looking
// through an AS alias. A "*" selection carries no expression.
func walkSelectionExpr(s sql.Selection, visit func(sql.Expression)) {
	switch v := s.(type) {
	case sql.AliasedSelection:
		walkSelectionExpr(v.Selection, visit)
	case sql.Expression:
		WalkExpr(v, visit)
	default:
		// StarSelection and other non-expression selections carry no expression.
	}
}
