package sqlite

import (
	"github.com/aita/sqlkit/sql"
	rsql "github.com/rqlite/sql"
)

// offsetOf normalizes an rqlite position (a 0-based byte offset) into the value
// sqlkit stores in NodePos: the byte offset plus one, so the zero value is
// reserved for "no position". An invalid (zero) position stores 0.
func offsetOf(p rsql.Pos) int {
	if !p.IsValid() {
		return 0
	}
	return p.Offset + 1
}

// positioned records p as the source offset of e when e is a node that carries a
// position, returning it unchanged otherwise (a RawExpression passthrough). It
// lets a convert wrap a result built through a constructor that returns the
// Expression interface — sql.And, sql.In — without a type assertion at the call
// site.
func positioned(e sql.Expression, p rsql.Pos) sql.Expression {
	pos := offsetOf(p)
	switch x := e.(type) {
	case sql.ColumnExpression:
		x.NodePos = pos
		return x
	case sql.ValueExpression:
		x.NodePos = pos
		return x
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
	case sql.RegexpExpression:
		x.NodePos = pos
		return x
	case sql.ConcatExpression:
		x.NodePos = pos
		return x
	case sql.CaseExpression:
		x.NodePos = pos
		return x
	case sql.FuncExpression:
		x.NodePos = pos
		return x
	case sql.CountExpression:
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
	default:
		return e
	}
}

// colAt and valAt are the positioned constructors the parser uses: they build a
// builder node and record its source offset, keeping position-setting out of the
// public sql builder API.
func colAt(name string, p rsql.Pos) sql.ColumnExpression {
	e := sql.Col(name)
	e.NodePos = offsetOf(p)
	return e
}

func valAt(value any, p rsql.Pos) sql.ValueExpression {
	e := sql.Val(value)
	e.NodePos = offsetOf(p)
	return e
}

// stmtPos returns the source position of a statement's leading token, so the
// top-level node records where the statement begins. A WITH clause, when present,
// precedes the statement keyword.
func stmtPos(stmt rsql.Statement) rsql.Pos {
	switch s := stmt.(type) {
	case *rsql.SelectStatement:
		if s.WithClause != nil {
			return s.WithClause.With
		}
		return s.Select
	case *rsql.InsertStatement:
		if s.WithClause != nil {
			return s.WithClause.With
		}
		if s.Replace.IsValid() {
			return s.Replace
		}
		return s.Insert
	case *rsql.UpdateStatement:
		if s.WithClause != nil {
			return s.WithClause.With
		}
		return s.Update
	case *rsql.DeleteStatement:
		if s.WithClause != nil {
			return s.WithClause.With
		}
		return s.Delete
	case *rsql.CreateTableStatement:
		return s.Create
	case *rsql.CreateIndexStatement:
		return s.Create
	case *rsql.DropTableStatement:
		return s.Drop
	default:
		return rsql.Pos{}
	}
}
