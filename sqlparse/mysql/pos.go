package mysql

import (
	"github.com/aita/sqlkit/sql"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// offsetOf normalizes a TiDB node's source offset into the value sqlkit stores in
// NodePos: the 0-based byte offset plus one, so the zero value is reserved for "no
// position". The TiDB parser records an offset on every expression node it
// reduces, so a converted expression always points back into the source SQL.
func offsetOf(node ast.Node) int {
	pos := node.OriginTextPosition()
	if pos < 0 {
		return 0
	}
	return pos + 1
}

// identAt builds an Ident for name carrying node's source offset, or an
// unpositioned Ident when the offset is not recorded. The TiDB parser leaves the
// offset at zero on identifier-only nodes it does not reduce as expressions
// (column names in DDL, for instance); since no identifier in a valid statement
// begins at byte 0, a zero offset means "unset" and the Ident stays position-free.
func identAt(name string, node ast.Node) sql.Ident {
	if pos := node.OriginTextPosition(); pos > 0 {
		return sql.NewIdentAt(name, pos)
	}
	return sql.NewIdent(name)
}

// positioned records node's source offset on e when e is a node that carries a
// position, returning it unchanged otherwise (a RawExpression, a passthrough). It
// lets a convert wrap a result built through a constructor that returns the
// Expression interface — sql.And, sql.In — without a type assertion at the call
// site. The set of cases mirrors the position-carrying expression types in the sql
// package.
func positioned(e sql.Expression, node ast.Node) sql.Expression {
	pos := offsetOf(node)
	switch x := e.(type) {
	case sql.BinaryExpression:
		x.NodePos = pos
		return x
	case sql.LogicalExpression:
		x.NodePos = pos
		return x
	case sql.NotExpression:
		x.NodePos = pos
		return x
	case sql.UnaryExpression:
		x.NodePos = pos
		return x
	case sql.NullPredicate:
		x.NodePos = pos
		return x
	case sql.DistinctPredicate:
		x.NodePos = pos
		return x
	case sql.BetweenExpression:
		x.NodePos = pos
		return x
	case sql.InExpression:
		x.NodePos = pos
		return x
	case sql.ILikeExpression:
		x.NodePos = pos
		return x
	case sql.RegexpExpression:
		x.NodePos = pos
		return x
	case sql.SimilarToExpression:
		x.NodePos = pos
		return x
	case sql.ConcatExpression:
		x.NodePos = pos
		return x
	case sql.RowExpression:
		x.NodePos = pos
		return x
	case sql.ArrayExpression:
		x.NodePos = pos
		return x
	case sql.QuantifiedExpression:
		x.NodePos = pos
		return x
	case sql.QuantifiedArrayExpression:
		x.NodePos = pos
		return x
	case sql.CaseExpression:
		x.NodePos = pos
		return x
	case sql.CastExpression:
		x.NodePos = pos
		return x
	case sql.CollateExpression:
		x.NodePos = pos
		return x
	case sql.FieldExpression:
		x.NodePos = pos
		return x
	case sql.SubscriptExpression:
		x.NodePos = pos
		return x
	case sql.FuncExpression:
		x.NodePos = pos
		return x
	case sql.CountExpression:
		x.NodePos = pos
		return x
	case sql.StringAggExpression:
		x.NodePos = pos
		return x
	case sql.ExtractExpression:
		x.NodePos = pos
		return x
	case sql.PositionExpression:
		x.NodePos = pos
		return x
	case sql.JSONObjectExpression:
		x.NodePos = pos
		return x
	case sql.JSONAccessExpression:
		x.NodePos = pos
		return x
	case sql.GroupingExpression:
		x.NodePos = pos
		return x
	case sql.GroupingSetsExpression:
		x.NodePos = pos
		return x
	case sql.WindowExpression:
		x.NodePos = pos
		return x
	case sql.ExistsExpression:
		x.NodePos = pos
		return x
	case sql.ScalarSubqueryExpression:
		x.NodePos = pos
		return x
	case sql.ColumnExpression:
		x.NodePos = pos
		return x
	case sql.ValueExpression:
		x.NodePos = pos
		return x
	default:
		return e
	}
}
