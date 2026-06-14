package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// setOperand is satisfied by the query types that can start a set operation
// (SelectQuery and CompoundQuery), letting convertCompound chain operators
// without caring which concrete type it holds.
type setOperand interface {
	Union(sql.Query) sql.CompoundQuery
	UnionAll(sql.Query) sql.CompoundQuery
	Intersect(sql.Query) sql.CompoundQuery
	Except(sql.Query) sql.CompoundQuery
}

// cteMaker is satisfied by the query types that can become a CTE body.
type cteMaker interface {
	CTE(string, ...sql.CTEOption) sql.CTE
}

// convertSelect converts a SELECT statement, dispatching to convertCompound for
// set operations (UNION/INTERSECT/EXCEPT).
func convertSelect(s *pg.SelectStmt) (sql.Query, error) {
	if s == nil {
		return nil, sqlparse.Unsupported("nil SELECT")
	}
	if s.GetOp() != pg.SetOperation_SETOP_NONE {
		return convertCompound(s)
	}
	if len(s.GetValuesLists()) > 0 {
		return nil, sqlparse.Unsupported("VALUES list")
	}

	columns, err := convertTargetList(s.GetTargetList())
	if err != nil {
		return nil, err
	}
	q := sql.Select(columns...)

	if distinct := s.GetDistinctClause(); len(distinct) > 0 {
		// Plain DISTINCT is a single empty list element; DISTINCT ON (...) carries
		// the distinct expressions, which sqlkit cannot represent.
		for _, d := range distinct {
			if d.GetNode() != nil {
				return nil, sqlparse.Unsupported("DISTINCT ON")
			}
		}
		q = q.Distinct()
	}

	if from := s.GetFromClause(); len(from) > 0 {
		base, joins, err := convertFrom(from)
		if err != nil {
			return nil, err
		}
		if base != nil {
			q = q.From(base)
		}
		for _, j := range joins {
			q = applyJoin(q, j)
		}
	}

	if where := s.GetWhereClause(); where != nil {
		expr, err := convertExpr(where)
		if err != nil {
			return nil, err
		}
		q = q.Where(expr)
	}

	if group := s.GetGroupClause(); len(group) > 0 {
		exprs := make([]sql.Expression, 0, len(group))
		for _, g := range group {
			expr, err := convertExpr(g)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
		}
		q = q.GroupBy(exprs...)
	}

	if having := s.GetHavingClause(); having != nil {
		expr, err := convertExpr(having)
		if err != nil {
			return nil, err
		}
		q = q.Having(expr)
	}

	if order := s.GetSortClause(); len(order) > 0 {
		terms, err := convertSortClause(order)
		if err != nil {
			return nil, err
		}
		q = q.OrderBy(terms...)
	}

	if n, ok, err := limitValue(s.GetLimitCount()); err != nil {
		return nil, err
	} else if ok {
		q = q.Limit(n)
	}
	if n, ok, err := limitValue(s.GetLimitOffset()); err != nil {
		return nil, err
	} else if ok {
		q = q.Offset(n)
	}

	if with := s.GetWithClause(); with != nil {
		ctes, err := convertWith(with)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	return q, nil
}

func applyJoin(q sql.SelectQuery, j joinSpec) sql.SelectQuery {
	switch j.kind {
	case "LEFT JOIN":
		return q.LeftJoin(j.source, j.on)
	case "RIGHT JOIN":
		return q.RightJoin(j.source, j.on)
	case "FULL JOIN":
		return q.FullJoin(j.source, j.on)
	case "CROSS JOIN":
		return q.CrossJoin(j.source)
	default:
		return q.Join(j.source, j.on)
	}
}

func convertCompound(s *pg.SelectStmt) (sql.Query, error) {
	left, err := convertSelect(s.GetLarg())
	if err != nil {
		return nil, err
	}
	right, err := convertSelect(s.GetRarg())
	if err != nil {
		return nil, err
	}
	operand, ok := left.(setOperand)
	if !ok {
		return nil, sqlparse.Unsupportedf("set-operation operand %T", left)
	}

	var compound sql.CompoundQuery
	switch s.GetOp() {
	case pg.SetOperation_SETOP_UNION:
		if s.GetAll() {
			compound = operand.UnionAll(right)
		} else {
			compound = operand.Union(right)
		}
	case pg.SetOperation_SETOP_INTERSECT:
		if s.GetAll() {
			return nil, sqlparse.Unsupported("INTERSECT ALL")
		}
		compound = operand.Intersect(right)
	case pg.SetOperation_SETOP_EXCEPT:
		if s.GetAll() {
			return nil, sqlparse.Unsupported("EXCEPT ALL")
		}
		compound = operand.Except(right)
	default:
		return nil, sqlparse.Unsupportedf("set operation %s", s.GetOp())
	}

	if order := s.GetSortClause(); len(order) > 0 {
		terms, err := convertSortClause(order)
		if err != nil {
			return nil, err
		}
		compound = compound.OrderBy(terms...)
	}
	if n, ok, err := limitValue(s.GetLimitCount()); err != nil {
		return nil, err
	} else if ok {
		compound = compound.Limit(n)
	}
	if n, ok, err := limitValue(s.GetLimitOffset()); err != nil {
		return nil, err
	} else if ok {
		compound = compound.Offset(n)
	}
	return compound, nil
}

func convertTargetList(targets []*pg.Node) ([]sql.Selection, error) {
	columns := make([]sql.Selection, 0, len(targets))
	for _, target := range targets {
		rt := target.GetResTarget()
		if rt == nil {
			return nil, sqlparse.Unsupportedf("select item %T", target.GetNode())
		}
		selection, err := convertResTarget(rt)
		if err != nil {
			return nil, err
		}
		columns = append(columns, selection)
	}
	return columns, nil
}

func convertResTarget(rt *pg.ResTarget) (sql.Selection, error) {
	selection, err := convertSelectionExpr(rt.GetVal())
	if err != nil {
		return nil, err
	}
	if rt.GetName() != "" {
		if _, isStar := selection.(sql.StarSelection); isStar {
			return nil, sqlparse.Unsupported("alias on '*'")
		}
		return sql.As(selection, rt.GetName()), nil
	}
	return selection, nil
}

func convertSortClause(nodes []*pg.Node) ([]sql.OrderTerm, error) {
	terms := make([]sql.OrderTerm, 0, len(nodes))
	for _, node := range nodes {
		sb := node.GetSortBy()
		if sb == nil {
			return nil, sqlparse.Unsupportedf("ORDER BY item %T", node.GetNode())
		}
		expr, err := convertExpr(sb.GetNode())
		if err != nil {
			return nil, err
		}
		term := sql.Asc(expr)
		if sb.GetSortbyDir() == pg.SortByDir_SORTBY_DESC {
			term = sql.Desc(expr)
		}
		switch sb.GetSortbyNulls() {
		case pg.SortByNulls_SORTBY_NULLS_FIRST:
			term = term.NullsFirst()
		case pg.SortByNulls_SORTBY_NULLS_LAST:
			term = term.NullsLast()
		}
		terms = append(terms, term)
	}
	return terms, nil
}

// limitValue reads a LIMIT/OFFSET node, which must be an integer constant.
func limitValue(node *pg.Node) (int, bool, error) {
	if node == nil {
		return 0, false, nil
	}
	c := node.GetAConst()
	if c == nil || c.GetIval() == nil {
		return 0, false, sqlparse.Unsupported("non-integer LIMIT/OFFSET")
	}
	return int(c.GetIval().GetIval()), true, nil
}

func convertWith(wc *pg.WithClause) ([]sql.CTE, error) {
	ctes := make([]sql.CTE, 0, len(wc.GetCtes()))
	for _, node := range wc.GetCtes() {
		cte := node.GetCommonTableExpr()
		if cte == nil {
			return nil, sqlparse.Unsupportedf("WITH item %T", node.GetNode())
		}
		body, err := convertSelect(cte.GetCtequery().GetSelectStmt())
		if err != nil {
			return nil, err
		}
		maker, ok := body.(cteMaker)
		if !ok {
			return nil, sqlparse.Unsupportedf("CTE body %T", body)
		}

		var opts []sql.CTEOption
		if cols := cte.GetAliascolnames(); len(cols) > 0 {
			names := make([]string, 0, len(cols))
			for _, col := range cols {
				if col.GetString_() == nil {
					return nil, sqlparse.Unsupported("CTE column name")
				}
				names = append(names, col.GetString_().GetSval())
			}
			opts = append(opts, sql.Columns(names...))
		}
		if wc.GetRecursive() {
			opts = append(opts, sql.Recursive())
		}
		ctes = append(ctes, maker.CTE(cte.GetCtename(), opts...))
	}
	return ctes, nil
}
