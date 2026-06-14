package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// tableFromRangeVar builds a target Table (schema-qualified, ignoring any alias)
// for a statement that writes to a single relation.
func tableFromRangeVar(rv *pg.RangeVar) sql.Table {
	return sql.Table{Schema: rv.GetSchemaname(), Name: rv.GetRelname()}
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

	if src := s.GetSelectStmt(); src != nil {
		inner := src.GetSelectStmt()
		if inner == nil {
			return nil, sqlparse.Unsupportedf("insert source %T", src.GetNode())
		}
		if values := inner.GetValuesLists(); len(values) > 0 {
			for _, rowNode := range values {
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

func applyOnConflict(q sql.InsertQuery, oc *pg.OnConflictClause) (sql.InsertQuery, error) {
	var opts []sql.ConflictOption
	if infer := oc.GetInfer(); infer != nil {
		if name := infer.GetConname(); name != "" {
			opts = append(opts, sql.OnConstraint(name))
		} else {
			var target []string
			for _, elem := range infer.GetIndexElems() {
				ie := elem.GetIndexElem()
				if ie == nil || ie.GetName() == "" {
					return q, sqlparse.Unsupported("ON CONFLICT expression target")
				}
				target = append(target, ie.GetName())
			}
			if len(target) > 0 {
				opts = append(opts, sql.OnColumns(target...))
			}
		}
	}

	switch oc.GetAction() {
	case pg.OnConflictAction_ONCONFLICT_NOTHING:
		opts = append(opts, sql.DoNothing())
	case pg.OnConflictAction_ONCONFLICT_UPDATE:
		assignments, err := convertAssignments(oc.GetTargetList(), true)
		if err != nil {
			return q, err
		}
		opts = append(opts, sql.DoUpdate(assignments...))
		if where := oc.GetWhereClause(); where != nil {
			expr, err := convertExpr(where)
			if err != nil {
				return q, err
			}
			opts = append(opts, sql.ConflictWhere(expr))
		}
	default:
		return q, sqlparse.Unsupportedf("ON CONFLICT action %s", oc.GetAction())
	}
	return q.OnConflict(opts...), nil
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

// convertAssignments turns a SET / DO UPDATE SET target list into assignments.
// When excluded is true, references to excluded.<col> map to sql.Excluded.
func convertAssignments(targets []*pg.Node, excluded bool) ([]sql.Assignment, error) {
	assignments := make([]sql.Assignment, 0, len(targets))
	for _, target := range targets {
		rt := target.GetResTarget()
		if rt == nil {
			return nil, sqlparse.Unsupportedf("assignment %T", target.GetNode())
		}
		if len(rt.GetIndirection()) > 0 {
			return nil, sqlparse.Unsupported("assignment to a subscript or field")
		}
		value, err := convertAssignmentValue(rt.GetVal(), excluded)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, sql.Set(rt.GetName(), value))
	}
	return assignments, nil
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
