package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// setOperand is satisfied by the query types that can start a set operation
// (SelectBuilder and CompoundBuilder), letting convertCompound chain operators
// without caring which concrete type it holds.
type setOperand interface {
	Union(sql.Query) sql.CompoundBuilder
	UnionAll(sql.Query) sql.CompoundBuilder
	Intersect(sql.Query) sql.CompoundBuilder
	IntersectAll(sql.Query) sql.CompoundBuilder
	Except(sql.Query) sql.CompoundBuilder
	ExceptAll(sql.Query) sql.CompoundBuilder
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
		return convertValuesList(s.GetValuesLists())
	}
	// A SELECT with no target list (the degenerate bare "SELECT" libpg_query still
	// accepts) has nothing to render and is not valid standalone SQL; fall back to
	// a raw statement rather than build a select the compiler must reject.
	if len(s.GetTargetList()) == 0 {
		return nil, sqlparse.Unsupported("SELECT without a target list")
	}

	columns, err := convertTargetList(s.GetTargetList())
	if err != nil {
		return nil, err
	}
	q := sql.Select(columns...)

	if distinct := s.GetDistinctClause(); len(distinct) > 0 {
		// Plain DISTINCT is a single empty list element; DISTINCT ON (...) carries
		// the distinct expressions.
		if distinct[0].GetNode() == nil {
			q = q.Distinct()
		} else {
			exprs := make([]sql.Expression, 0, len(distinct))
			for _, d := range distinct {
				expr, err := convertExpr(d)
				if err != nil {
					return nil, err
				}
				exprs = append(exprs, expr)
			}
			q = q.DistinctOn(exprs...)
		}
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

	if windows := s.GetWindowClause(); len(windows) > 0 {
		defs := make([]sql.NamedWindow, 0, len(windows))
		for _, node := range windows {
			wd := node.GetWindowDef()
			if wd == nil {
				return nil, sqlparse.Unsupportedf("WINDOW item %T", node.GetNode())
			}
			def, err := convertNamedWindow(wd)
			if err != nil {
				return nil, err
			}
			defs = append(defs, def)
		}
		q = q.Window(defs...)
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

	if locks := s.GetLockingClause(); len(locks) > 0 {
		q, err = applyLocking(q, locks)
		if err != nil {
			return nil, err
		}
	}

	return q, nil
}

// applyLocking maps SELECT row-locking clauses (FOR UPDATE / FOR SHARE / FOR NO
// KEY UPDATE / FOR KEY SHARE, with OF / SKIP LOCKED / NOWAIT).
func applyLocking(q sql.SelectBuilder, locks []*pg.Node) (sql.SelectBuilder, error) {
	for _, node := range locks {
		lc := node.GetLockingClause()
		if lc == nil {
			return q, sqlparse.Unsupportedf("locking clause %T", node.GetNode())
		}
		var opts []sql.LockOption
		if of := lockedRelNames(lc.GetLockedRels()); len(of) > 0 {
			opts = append(opts, sql.LockOf(of...))
		}
		switch lc.GetWaitPolicy() {
		case pg.LockWaitPolicy_LockWaitSkip:
			opts = append(opts, sql.SkipLocked())
		case pg.LockWaitPolicy_LockWaitError:
			opts = append(opts, sql.NoWait())
		}
		switch lc.GetStrength() {
		case pg.LockClauseStrength_LCS_FORUPDATE:
			q = q.ForUpdate(opts...)
		case pg.LockClauseStrength_LCS_FORSHARE:
			q = q.ForShare(opts...)
		case pg.LockClauseStrength_LCS_FORNOKEYUPDATE:
			q = q.ForNoKeyUpdate(opts...)
		case pg.LockClauseStrength_LCS_FORKEYSHARE:
			q = q.ForKeyShare(opts...)
		default:
			return q, sqlparse.Unsupportedf("lock strength %s", lc.GetStrength())
		}
	}
	return q, nil
}

func lockedRelNames(rels []*pg.Node) []string {
	names := make([]string, 0, len(rels))
	for _, r := range rels {
		if rv := r.GetRangeVar(); rv != nil {
			names = append(names, rv.GetRelname())
		}
	}
	return names
}

func applyJoin(q sql.SelectBuilder, j joinSpec) sql.SelectBuilder {
	if j.natural {
		if j.kind == "LEFT JOIN" {
			return q.NaturalLeftJoin(j.source)
		}
		return q.NaturalJoin(j.source)
	}
	if j.using != nil {
		if j.kind == "LEFT JOIN" {
			return q.LeftJoinUsing(j.source, j.using...)
		}
		return q.JoinUsing(j.source, j.using...)
	}
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

	var compound sql.CompoundBuilder
	switch s.GetOp() {
	case pg.SetOperation_SETOP_UNION:
		if s.GetAll() {
			compound = operand.UnionAll(right)
		} else {
			compound = operand.Union(right)
		}
	case pg.SetOperation_SETOP_INTERSECT:
		if s.GetAll() {
			compound = operand.IntersectAll(right)
		} else {
			compound = operand.Intersect(right)
		}
	case pg.SetOperation_SETOP_EXCEPT:
		if s.GetAll() {
			compound = operand.ExceptAll(right)
		} else {
			compound = operand.Except(right)
		}
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
		// ORDER BY ... USING <operator> selects a custom ordering operator (and
		// reverses direction for >). sqlkit's OrderTerm models only ASC/DESC, so
		// fall back to a raw passthrough rather than silently drop the operator and
		// change the sort meaning.
		if sb.GetSortbyDir() == pg.SortByDir_SORTBY_USING {
			return nil, sqlparse.Unsupported("ORDER BY ... USING operator")
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

// convertValuesList converts a standalone VALUES statement into a ValuesQuery.
func convertValuesList(rows []*pg.Node) (sql.Query, error) {
	q := sql.Values()
	for _, rowNode := range rows {
		row := rowNode.GetList()
		if row == nil {
			return nil, sqlparse.Unsupportedf("VALUES row %T", rowNode.GetNode())
		}
		cells := make([]any, 0, len(row.GetItems()))
		for _, item := range row.GetItems() {
			expr, err := convertExpr(item)
			if err != nil {
				return nil, err
			}
			cells = append(cells, expr)
		}
		q = q.Row(cells...)
	}
	return q, nil
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
