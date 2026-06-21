package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// tableFromRangeVar builds a target Table (schema-qualified, ignoring any alias)
// for a statement that writes to a single relation. The table and its name
// identifier record the relation's source position; pg_query locates the
// RangeVar but not its schema, so the schema identifier carries no position.
func tableFromRangeVar(rv *pg.RangeVar) sql.Table {
	pos := offsetOf(rv.GetLocation())
	t := sql.Table{Schema: sql.NewIdent(rv.GetSchemaname()), Name: sql.NewIdent(rv.GetRelname())}
	t.NodePos = pos
	t.Name.NodePos = pos
	return t
}

func convertInsert(s *pg.InsertStmt) (sql.Query, error) {
	q := sql.InsertInto(tableFromRangeVar(s.GetRelation()))

	if cols := s.GetCols(); len(cols) > 0 {
		names := make([]string, 0, len(cols))
		for _, col := range cols {
			rt := col.GetResTarget()
			if rt == nil {
				return nil, sqlparse.Unsupportedf("insert column %T", col.GetNode())
			}
			names = append(names, rt.GetName())
		}
		q = q.Columns(names...)
	}

	if s.GetSelectStmt() == nil {
		// No VALUES and no SELECT source means INSERT ... DEFAULT VALUES, which the
		// InsertQuery builder does not model; round-trip it raw.
		return nil, sqlparse.Unsupported("INSERT ... DEFAULT VALUES")
	}
	if src := s.GetSelectStmt(); src != nil {
		inner := src.GetSelectStmt()
		if inner == nil {
			return nil, sqlparse.Unsupportedf("insert source %T", src.GetNode())
		}
		if values := inner.GetValuesLists(); len(values) > 0 {
			// sqlkit's compiler requires an explicit column list for a VALUES insert,
			// and a row whose arity differs from it cannot be compiled. Fall back to
			// raw rather than build an INSERT the compiler must reject.
			ncols := len(s.GetCols())
			if ncols == 0 {
				return nil, sqlparse.Unsupported("INSERT without a column list")
			}
			for _, rowNode := range values {
				row := rowNode.GetList()
				if row == nil {
					return nil, sqlparse.Unsupportedf("VALUES row %T", rowNode.GetNode())
				}
				if len(row.GetItems()) != ncols {
					return nil, sqlparse.Unsupportedf("INSERT row has %d values for %d columns", len(row.GetItems()), ncols)
				}
				cells := make([]any, 0, len(row.GetItems()))
				for _, item := range row.GetItems() {
					expr, err := convertExpr(item)
					if err != nil {
						return nil, err
					}
					cells = append(cells, expr)
				}
				q = q.Values(cells...)
			}
		} else {
			sub, err := convertSelect(inner)
			if err != nil {
				return nil, err
			}
			q = q.FromSelect(sub)
		}
	}

	if oc := s.GetOnConflictClause(); oc != nil {
		var err error
		if q, err = applyOnConflict(q, oc); err != nil {
			return nil, err
		}
	}

	returning, err := convertReturning(s.GetReturningList())
	if err != nil {
		return nil, err
	}
	if len(returning) > 0 {
		q = q.Returning(returning...)
	}
	return q, nil
}

func applyOnConflict(q sql.InsertBuilder, oc *pg.OnConflictClause) (sql.InsertBuilder, error) {
	hasTarget := false
	if infer := oc.GetInfer(); infer != nil {
		if name := infer.GetConname(); name != "" {
			q = q.OnConflictConstraint(name)
			hasTarget = true
		} else if elems := infer.GetIndexElems(); len(elems) > 0 {
			var err error
			if q, err = applyInferTarget(q, elems); err != nil {
				return q, err
			}
			hasTarget = true
		}
	}

	switch oc.GetAction() {
	case pg.OnConflictAction_ONCONFLICT_NOTHING:
		return q.DoNothing(), nil
	case pg.OnConflictAction_ONCONFLICT_UPDATE:
		// PostgreSQL requires a conflict target for DO UPDATE (only DO NOTHING may
		// omit it); the raw parser tolerates its absence, but the result cannot be
		// compiled, so fall back to raw.
		if !hasTarget {
			return q, sqlparse.Unsupported("ON CONFLICT DO UPDATE without a conflict target")
		}
		assignments, err := convertAssignments(oc.GetTargetList(), true)
		if err != nil {
			return q, err
		}
		q = q.DoUpdate(assignments...)
		if where := oc.GetWhereClause(); where != nil {
			expr, err := convertExpr(where)
			if err != nil {
				return q, err
			}
			q = q.Where(expr)
		}
		return q, nil
	default:
		return q, sqlparse.Unsupportedf("ON CONFLICT action %s", oc.GetAction())
	}
}

// applyInferTarget sets the ON CONFLICT conflict target from its index elements:
// OnConflict (columns) when every element is a plain column, otherwise
// OnConflictExpr (plain columns become column references). Elements with an
// opclass, collation, or ordering are not modeled and report Unsupported.
func applyInferTarget(q sql.InsertBuilder, elems []*pg.Node) (sql.InsertBuilder, error) {
	allColumns := true
	for _, elem := range elems {
		ie := elem.GetIndexElem()
		if ie == nil {
			return q, sqlparse.Unsupportedf("ON CONFLICT target %T", elem.GetNode())
		}
		if len(ie.GetOpclass()) > 0 || len(ie.GetCollation()) > 0 ||
			ie.GetOrdering() != pg.SortByDir_SORTBY_DEFAULT {
			return q, sqlparse.Unsupported("ON CONFLICT target with opclass/collation/ordering")
		}
		if ie.GetName() == "" {
			allColumns = false
		}
	}
	if allColumns {
		names := make([]string, 0, len(elems))
		for _, elem := range elems {
			names = append(names, elem.GetIndexElem().GetName())
		}
		return q.OnConflict(names...), nil
	}
	exprs := make([]sql.Expression, 0, len(elems))
	for _, elem := range elems {
		ie := elem.GetIndexElem()
		switch {
		case ie.GetExpr() != nil:
			expr, err := convertExpr(ie.GetExpr())
			if err != nil {
				return q, err
			}
			exprs = append(exprs, expr)
		case ie.GetName() != "":
			exprs = append(exprs, sql.Col(ie.GetName()))
		default:
			return q, sqlparse.Unsupported("ON CONFLICT target element")
		}
	}
	return q.OnConflictExpr(exprs...), nil
}

func convertUpdate(s *pg.UpdateStmt) (sql.Query, error) {
	q := sql.Update(tableFromRangeVar(s.GetRelation()))

	assignments, err := convertAssignments(s.GetTargetList(), false)
	if err != nil {
		return nil, err
	}
	q = q.Set(assignments...)

	if from := s.GetFromClause(); len(from) > 0 {
		sources, joins, err := convertSourceList(from)
		if err != nil {
			return nil, err
		}
		q = q.From(sources...)
		for _, j := range joins {
			switch j.kind {
			case "JOIN":
				q = q.Join(j.source, j.on)
			case "LEFT JOIN":
				q = q.LeftJoin(j.source, j.on)
			default:
				return nil, sqlparse.Unsupportedf("%s in UPDATE ... FROM", j.kind)
			}
		}
	}

	if where := s.GetWhereClause(); where != nil {
		expr, err := convertExpr(where)
		if err != nil {
			return nil, err
		}
		q = q.Where(expr)
	}

	if with := s.GetWithClause(); with != nil {
		ctes, err := convertWith(with)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	returning, err := convertReturning(s.GetReturningList())
	if err != nil {
		return nil, err
	}
	if len(returning) > 0 {
		q = q.Returning(returning...)
	}
	return q, nil
}

func convertDelete(s *pg.DeleteStmt) (sql.Query, error) {
	q := sql.DeleteFrom(tableFromRangeVar(s.GetRelation()))

	if using := s.GetUsingClause(); len(using) > 0 {
		sources, joins, err := convertSourceList(using)
		if err != nil {
			return nil, err
		}
		q = q.Using(sources...)
		for _, j := range joins {
			switch j.kind {
			case "JOIN":
				q = q.Join(j.source, j.on)
			case "LEFT JOIN":
				q = q.LeftJoin(j.source, j.on)
			default:
				return nil, sqlparse.Unsupportedf("%s in DELETE ... USING", j.kind)
			}
		}
	}

	if where := s.GetWhereClause(); where != nil {
		expr, err := convertExpr(where)
		if err != nil {
			return nil, err
		}
		q = q.Where(expr)
	}

	if with := s.GetWithClause(); with != nil {
		ctes, err := convertWith(with)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	returning, err := convertReturning(s.GetReturningList())
	if err != nil {
		return nil, err
	}
	if len(returning) > 0 {
		q = q.Returning(returning...)
	}
	return q, nil
}

func convertMerge(s *pg.MergeStmt) (sql.Query, error) {
	q := sql.MergeInto(tableFromRangeVar(s.GetRelation()))
	if alias := s.GetRelation().GetAlias(); alias != nil && alias.GetAliasname() != "" {
		q = q.As(alias.GetAliasname())
	}

	source, err := convertSource(s.GetSourceRelation())
	if err != nil {
		return nil, err
	}
	q = q.Using(source)

	on, err := convertExpr(s.GetJoinCondition())
	if err != nil {
		return nil, err
	}
	q = q.On(on)

	for _, node := range s.GetMergeWhenClauses() {
		wc := node.GetMergeWhenClause()
		if wc == nil {
			return nil, sqlparse.Unsupportedf("MERGE clause %T", node.GetNode())
		}
		when, err := convertMergeWhen(wc)
		if err != nil {
			return nil, err
		}
		q = q.When(when)
	}

	if with := s.GetWithClause(); with != nil {
		ctes, err := convertWith(with)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	returning, err := convertReturning(s.GetReturningList())
	if err != nil {
		return nil, err
	}
	if len(returning) > 0 {
		q = q.Returning(returning...)
	}
	return q, nil
}

func convertMergeWhen(wc *pg.MergeWhenClause) (sql.MergeWhen, error) {
	var when sql.MergeWhen
	switch wc.GetMatchKind() {
	case pg.MergeMatchKind_MERGE_WHEN_MATCHED:
		when = sql.WhenMatched()
	case pg.MergeMatchKind_MERGE_WHEN_NOT_MATCHED_BY_TARGET:
		when = sql.WhenNotMatched()
	case pg.MergeMatchKind_MERGE_WHEN_NOT_MATCHED_BY_SOURCE:
		when = sql.WhenNotMatchedBySource()
	default:
		return when, sqlparse.Unsupportedf("MERGE match kind %s", wc.GetMatchKind())
	}

	if cond := wc.GetCondition(); cond != nil {
		expr, err := convertExpr(cond)
		if err != nil {
			return when, err
		}
		when = when.And(expr)
	}

	switch wc.GetCommandType() {
	case pg.CmdType_CMD_NOTHING:
		return when.ThenDoNothing(), nil
	case pg.CmdType_CMD_DELETE:
		return when.ThenDelete(), nil
	case pg.CmdType_CMD_UPDATE:
		assignments, err := convertAssignments(wc.GetTargetList(), false)
		if err != nil {
			return when, err
		}
		return when.ThenUpdate(assignments...), nil
	case pg.CmdType_CMD_INSERT:
		return convertMergeInsert(when, wc)
	default:
		return when, sqlparse.Unsupportedf("MERGE action %s", wc.GetCommandType())
	}
}

func convertMergeInsert(when sql.MergeWhen, wc *pg.MergeWhenClause) (sql.MergeWhen, error) {
	columns := make([]string, 0, len(wc.GetTargetList()))
	for _, target := range wc.GetTargetList() {
		rt := target.GetResTarget()
		if rt == nil {
			return when, sqlparse.Unsupportedf("MERGE INSERT column %T", target.GetNode())
		}
		columns = append(columns, rt.GetName())
	}
	values := make([]any, 0, len(wc.GetValues()))
	for _, value := range wc.GetValues() {
		expr, err := convertExpr(value)
		if err != nil {
			return when, err
		}
		values = append(values, expr)
	}
	return when.ThenInsert(columns, values...), nil
}

// convertAssignments turns a SET / DO UPDATE SET target list into assignments.
// When excluded is true, references to excluded.<col> map to sql.Excluded.
func convertAssignments(targets []*pg.Node, excluded bool) ([]sql.Assignment, error) {
	assignments := make([]sql.Assignment, 0, len(targets))
	for _, target := range targets {
		rt := target.GetResTarget()
		if rt == nil {
			return nil, sqlparse.Unsupportedf("assignment %T", target.GetNode())
		}
		value, err := convertAssignmentValue(rt.GetVal(), excluded)
		if err != nil {
			return nil, err
		}
		if ind := rt.GetIndirection(); len(ind) > 0 {
			// An indirected target such as col[idx]: model the left-hand side as a
			// subscript expression. Field (composite) targets are not modeled.
			target, err := subscriptTarget(rt.GetName(), ind)
			if err != nil {
				return nil, err
			}
			assignments = append(assignments, sql.SetAt(target, value))
			continue
		}
		assignments = append(assignments, sql.Set(rt.GetName(), value))
	}
	return assignments, nil
}

// subscriptTarget builds the left-hand side of an indirected SET target,
// column[index]..., requiring every indirection step to be a subscript/slice.
func subscriptTarget(name string, indirection []*pg.Node) (sql.Expression, error) {
	indices := make([]sql.SubscriptIndex, 0, len(indirection))
	for _, item := range indirection {
		ai := item.GetAIndices()
		if ai == nil {
			return nil, sqlparse.Unsupported("assignment to a composite field")
		}
		idx := sql.SubscriptIndex{Slice: ai.GetIsSlice()}
		if lo := ai.GetLidx(); lo != nil {
			expr, err := convertExpr(lo)
			if err != nil {
				return nil, err
			}
			idx.Lower = expr
		}
		if up := ai.GetUidx(); up != nil {
			expr, err := convertExpr(up)
			if err != nil {
				return nil, err
			}
			idx.Upper = expr
		}
		indices = append(indices, idx)
	}
	return sql.SubscriptExpression{Operand: sql.Col(name), Indices: indices}, nil
}

func convertAssignmentValue(node *pg.Node, excluded bool) (sql.Expression, error) {
	if excluded {
		if cr := node.GetColumnRef(); cr != nil {
			fields := cr.GetFields()
			if len(fields) == 2 && fields[0].GetString_() != nil &&
				fields[0].GetString_().GetSval() == "excluded" && fields[1].GetString_() != nil {
				return sql.Excluded(fields[1].GetString_().GetSval()), nil
			}
		}
	}
	return convertExpr(node)
}

func convertReturning(list []*pg.Node) ([]sql.Selection, error) {
	if len(list) == 0 {
		return nil, nil
	}
	return convertTargetList(list)
}

// convertSourceList flattens a comma-separated FROM/USING list into base sources
// and any joins among them.
func convertSourceList(nodes []*pg.Node) ([]sql.Source, []joinSpec, error) {
	var sources []sql.Source
	var joins []joinSpec
	for _, node := range nodes {
		base, extra, err := flattenFrom(node)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, base)
		joins = append(joins, extra...)
	}
	return sources, joins, nil
}
