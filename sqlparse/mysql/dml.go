package mysql

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// singleTable extracts the one TableName a single-table DML statement writes to.
func singleTable(refs *ast.TableRefsClause, tp tablePos) (sql.Table, error) {
	if refs == nil || refs.TableRefs == nil {
		return sql.Table{}, sqlparse.Unsupported("statement without a table")
	}
	join := refs.TableRefs
	if join.Right != nil {
		return sql.Table{}, sqlparse.Unsupported("multi-table statement")
	}
	ts, ok := join.Left.(*ast.TableSource)
	if !ok {
		return sql.Table{}, sqlparse.Unsupportedf("table reference %T", join.Left)
	}
	tn, ok := ts.Source.(*ast.TableName)
	if !ok {
		return sql.Table{}, sqlparse.Unsupportedf("table source %T", ts.Source)
	}
	if err := checkTableName(tn); err != nil {
		return sql.Table{}, err
	}
	return tableAt(tn, tp), nil
}

func convertInsert(s *ast.InsertStmt, tp tablePos) (sql.Query, error) {
	if len(s.Returning) > 0 {
		return nil, sqlparse.Unsupported("INSERT ... RETURNING")
	}
	table, err := singleTable(s.Table, tp)
	if err != nil {
		return nil, err
	}
	q := sql.InsertInto(table)
	if s.IsReplace {
		q = sql.ReplaceInto(table)
	}

	if len(s.Columns) > 0 {
		names := make([]string, 0, len(s.Columns))
		for _, col := range s.Columns {
			names = append(names, col.Name.O)
		}
		q = q.Columns(names...)
	}

	switch {
	case len(s.Lists) > 0:
		// sqlkit's compiler requires an explicit column list for a VALUES insert,
		// and a row whose arity differs from it cannot be compiled — but TiDB accepts
		// both (it defers the checks). Fall back to raw rather than build an INSERT
		// the compiler must reject.
		if len(s.Columns) == 0 {
			return nil, sqlparse.Unsupported("INSERT without a column list")
		}
		for _, row := range s.Lists {
			if len(row) != len(s.Columns) {
				return nil, sqlparse.Unsupportedf("INSERT row has %d values for %d columns", len(row), len(s.Columns))
			}
			cells, err := convertInsertRow(row)
			if err != nil {
				return nil, err
			}
			q = q.Values(cells...)
		}
	case s.Select != nil:
		sub, err := convertResultSetQuery(s.Select, tp)
		if err != nil {
			return nil, err
		}
		q = q.FromSelect(sub)
	}

	if len(s.OnDuplicate) > 0 {
		if s.IsReplace {
			return nil, sqlparse.Unsupported("REPLACE ... ON DUPLICATE KEY UPDATE")
		}
		assignments, err := convertAssignments(s.OnDuplicate, true)
		if err != nil {
			return nil, err
		}
		// MySQL's ON DUPLICATE KEY UPDATE has no conflict target.
		q = q.OnConflict().DoUpdate(assignments...)
	}

	return q, nil
}

func convertInsertRow(row []ast.ExprNode) ([]any, error) {
	cells := make([]any, 0, len(row))
	for _, cell := range row {
		expr, err := convertExpr(cell)
		if err != nil {
			return nil, err
		}
		cells = append(cells, expr)
	}
	return cells, nil
}

func convertUpdate(s *ast.UpdateStmt, tp tablePos) (sql.Query, error) {
	if len(s.Returning) > 0 {
		return nil, sqlparse.Unsupported("UPDATE ... RETURNING")
	}
	assignments, err := convertAssignments(s.List, false)
	if err != nil {
		return nil, err
	}

	// A joined table reference (t1 JOIN t2 ...) is a multi-table UPDATE even when
	// only one table is assigned, so detect it from the refs rather than the
	// MultipleTable flag, which MySQL leaves false for a single-target join.
	var q sql.UpdateBuilder
	if refs := s.TableRefs; refs != nil && refs.TableRefs != nil && refs.TableRefs.Right != nil {
		if q, err = multiTableUpdate(s, assignments, tp); err != nil {
			return nil, err
		}
	} else {
		table, err := singleTable(s.TableRefs, tp)
		if err != nil {
			return nil, err
		}
		q = sql.Update(table).Set(assignments...)
	}

	if s.Where != nil {
		expr, err := convertExpr(s.Where)
		if err != nil {
			return nil, err
		}
		q = q.Where(expr)
	}
	if s.Order != nil {
		terms, err := convertByItems(s.Order.Items)
		if err != nil {
			return nil, err
		}
		q = q.OrderBy(terms...)
	}
	if s.Limit != nil {
		n, err := rowLimitCount(s.Limit)
		if err != nil {
			return nil, err
		}
		q = q.Limit(n)
	}
	if s.With != nil {
		ctes, err := convertWith(s.With, tp)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}
	return q, nil
}

// rowLimitCount reads the LIMIT count of an UPDATE/DELETE, which MySQL allows
// only as a bare count — an OFFSET there has no typed home.
func rowLimitCount(limit *ast.Limit) (int, error) {
	if limit.Offset != nil {
		return 0, sqlparse.Unsupported("LIMIT with OFFSET on UPDATE/DELETE")
	}
	return intLiteral(limit.Count)
}

// multiTableUpdate maps MySQL's UPDATE t1 JOIN t2 ... SET ... onto the builder's
// Update(base).Join(...) form. The base must be a plain table and the joins must
// be INNER/LEFT (the only kinds UpdateQuery models); anything else round-trips
// raw.
func multiTableUpdate(s *ast.UpdateStmt, assignments []sql.Assignment, tp tablePos) (sql.UpdateBuilder, error) {
	base, joins, err := convertFrom(s.TableRefs.TableRefs, tp)
	if err != nil {
		return sql.UpdateBuilder{}, err
	}
	table, ok := base.(sql.Table)
	if !ok {
		return sql.UpdateBuilder{}, sqlparse.Unsupportedf("multi-table UPDATE base %T", base)
	}
	q := sql.Update(table).Set(assignments...)
	for _, j := range joins {
		switch {
		case j.natural || j.using != nil:
			return sql.UpdateBuilder{}, sqlparse.Unsupported("NATURAL/USING join in multi-table UPDATE")
		case j.kind == "JOIN":
			q = q.Join(j.source, j.on)
		case j.kind == "LEFT JOIN":
			q = q.LeftJoin(j.source, j.on)
		default:
			return sql.UpdateBuilder{}, sqlparse.Unsupportedf("%s in multi-table UPDATE", j.kind)
		}
	}
	return q, nil
}

func convertDelete(s *ast.DeleteStmt, tp tablePos) (sql.Query, error) {
	if len(s.Returning) > 0 {
		return nil, sqlparse.Unsupported("DELETE ... RETURNING")
	}
	var q sql.DeleteBuilder
	var err error
	if s.IsMultiTable {
		if q, err = multiTableDelete(s, tp); err != nil {
			return nil, err
		}
	} else {
		table, terr := singleTable(s.TableRefs, tp)
		if terr != nil {
			return nil, terr
		}
		q = sql.DeleteFrom(table)
	}

	if s.Where != nil {
		expr, err := convertExpr(s.Where)
		if err != nil {
			return nil, err
		}
		q = q.Where(expr)
	}
	if s.Order != nil {
		terms, err := convertByItems(s.Order.Items)
		if err != nil {
			return nil, err
		}
		q = q.OrderBy(terms...)
	}
	if s.Limit != nil {
		n, err := rowLimitCount(s.Limit)
		if err != nil {
			return nil, err
		}
		q = q.Limit(n)
	}
	if s.With != nil {
		ctes, err := convertWith(s.With, tp)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}
	return q, nil
}

// multiTableDelete maps MySQL's DELETE t FROM t JOIN j ... onto
// DeleteFrom(t).Join(...). It handles a single delete target whose name matches
// the FROM base (an aliased target or a target distinct from the base has no
// typed home and round-trips raw).
func multiTableDelete(s *ast.DeleteStmt, tp tablePos) (sql.DeleteBuilder, error) {
	if s.Tables == nil || len(s.Tables.Tables) != 1 {
		return sql.DeleteBuilder{}, sqlparse.Unsupported("multi-target DELETE")
	}
	if err := checkTableName(s.Tables.Tables[0]); err != nil {
		return sql.DeleteBuilder{}, err
	}
	target := tableAt(s.Tables.Tables[0], tp)
	base, joins, err := convertFrom(s.TableRefs.TableRefs, tp)
	if err != nil {
		return sql.DeleteBuilder{}, err
	}
	// Compare by name, not by value: the target and the FROM base are different
	// source occurrences of the same table, so they carry different positions.
	if baseTable, ok := base.(sql.Table); !ok || !baseTable.Name.Equal(target.Name) || !baseTable.Schema.Equal(target.Schema) {
		return sql.DeleteBuilder{}, sqlparse.Unsupported("DELETE target distinct from the FROM base")
	}
	q := sql.DeleteFrom(target)
	for _, j := range joins {
		switch {
		case j.natural || j.using != nil:
			return sql.DeleteBuilder{}, sqlparse.Unsupported("NATURAL/USING join in multi-table DELETE")
		case j.kind == "CROSS JOIN":
			q = q.Using(j.source)
		case j.kind == "JOIN":
			q = q.Join(j.source, j.on)
		case j.kind == "LEFT JOIN":
			q = q.LeftJoin(j.source, j.on)
		default:
			return sql.DeleteBuilder{}, sqlparse.Unsupportedf("%s in multi-table DELETE", j.kind)
		}
	}
	return q, nil
}

// convertAssignments turns a SET / ON DUPLICATE KEY UPDATE list into assignments.
// When duplicate is true, VALUES(col) references map to sql.Excluded, the
// portable spelling of MySQL's "the value that would have been inserted".
func convertAssignments(list []*ast.Assignment, duplicate bool) ([]sql.Assignment, error) {
	assignments := make([]sql.Assignment, 0, len(list))
	for _, a := range list {
		value, err := convertAssignmentValue(a.Expr, duplicate)
		if err != nil {
			return nil, err
		}
		name, err := columnRefName(a.Column)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, sql.Set(name, value))
	}
	return assignments, nil
}

func convertAssignmentValue(node ast.ExprNode, duplicate bool) (sql.Expression, error) {
	if duplicate {
		// VALUES(col) names the value that would have been inserted; sqlkit spells
		// it Excluded(col) and the MySQL dialect renders it back as VALUES(col).
		if v, ok := node.(*ast.ValuesExpr); ok && v.Column != nil {
			return sql.Excluded(v.Column.Name.Name.O), nil
		}
	}
	return convertExpr(node)
}
