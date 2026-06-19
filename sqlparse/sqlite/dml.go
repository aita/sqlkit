package sqlite

import (
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// tableName builds a sql.Table from an optional schema and a name identifier.
func tableName(schema *rsql.Ident, name *rsql.Ident) sql.Table {
	t := sql.Table{Name: name.Name}
	if schema != nil {
		t.Schema = schema.Name
	}
	return t
}

// tableFromQualified builds a target Table from a QualifiedTableName, ignoring
// any alias (the write target is named, not aliased, in sqlkit).
func tableFromQualified(q *rsql.QualifiedTableName) sql.Table {
	return tableName(q.Schema, q.Name)
}

func convertInsert(s *rsql.InsertStatement) (sql.Query, error) {
	// REPLACE and INSERT OR <action> are SQLite-specific conflict shorthands the
	// InsertStmt does not model; round-trip them.
	if s.Replace.IsValid() || s.InsertOr.IsValid() {
		return nil, sqlparse.Unsupported("REPLACE / INSERT OR")
	}
	if s.DefaultValues.IsValid() {
		return nil, sqlparse.Unsupported("INSERT ... DEFAULT VALUES")
	}

	q := sql.InsertInto(tableName(s.Schema, s.Table))

	if len(s.Columns) > 0 {
		names := make([]string, 0, len(s.Columns))
		for _, col := range s.Columns {
			names = append(names, col.Name)
		}
		q = q.Columns(names...)
	}

	switch {
	case len(s.ValueLists) > 0:
		for _, row := range s.ValueLists {
			cells := make([]any, 0, len(row.Exprs))
			for _, item := range row.Exprs {
				expr, err := convertExpr(item)
				if err != nil {
					return nil, err
				}
				cells = append(cells, expr)
			}
			q = q.Values(cells...)
		}
	case s.Select != nil:
		sub, err := convertSelect(s.Select)
		if err != nil {
			return nil, err
		}
		q = q.FromSelect(sub)
	default:
		return nil, sqlparse.Unsupported("INSERT without VALUES or SELECT")
	}

	if s.UpsertClause != nil {
		var err error
		if q, err = applyUpsert(q, s.UpsertClause); err != nil {
			return nil, err
		}
	}

	if s.ReturningClause != nil {
		returning, err := convertReturning(s.ReturningClause)
		if err != nil {
			return nil, err
		}
		q = q.Returning(returning...)
	}
	return q, nil
}

func applyUpsert(q sql.InsertBuilder, u *rsql.UpsertClause) (sql.InsertBuilder, error) {
	// A conflict-target WHERE (partial-index predicate) is not modeled.
	if u.WhereExpr != nil {
		return q, sqlparse.Unsupported("ON CONFLICT target WHERE")
	}

	if len(u.Columns) > 0 {
		target, exprs, allColumns, err := conflictTarget(u.Columns)
		if err != nil {
			return q, err
		}
		if allColumns {
			q = q.OnConflict(target...)
		} else {
			q = q.OnConflictExpr(exprs...)
		}
	}

	if u.DoNothing.IsValid() {
		return q.DoNothing(), nil
	}
	if u.DoUpdate.IsValid() {
		assignments, err := convertAssignments(u.Assignments, true)
		if err != nil {
			return q, err
		}
		q = q.DoUpdate(assignments...)
		if u.UpdateWhereExpr != nil {
			where, err := convertExpr(u.UpdateWhereExpr)
			if err != nil {
				return q, err
			}
			q = q.Where(where)
		}
		return q, nil
	}
	return q, nil
}

// conflictTarget extracts an ON CONFLICT target. It reports allColumns=true with
// the plain column names when every target is a bare column, otherwise returns
// the targets as expressions for ON CONFLICT ((expr), ...).
func conflictTarget(columns []*rsql.IndexedColumn) (names []string, exprs []sql.Expression, allColumns bool, err error) {
	allColumns = true
	for _, col := range columns {
		if ident, ok := col.X.(*rsql.Ident); ok {
			names = append(names, ident.Name)
		} else {
			allColumns = false
		}
	}
	if allColumns {
		return names, nil, true, nil
	}
	exprs = make([]sql.Expression, 0, len(columns))
	for _, col := range columns {
		expr, err := convertExpr(col.X)
		if err != nil {
			return nil, nil, false, err
		}
		exprs = append(exprs, expr)
	}
	return nil, exprs, false, nil
}

func convertUpdate(s *rsql.UpdateStatement) (sql.Query, error) {
	if s.UpdateOr.IsValid() {
		return nil, sqlparse.Unsupported("UPDATE OR")
	}

	q := sql.Update(tableFromQualified(s.Table))

	assignments, err := convertAssignments(s.Assignments, false)
	if err != nil {
		return nil, err
	}
	q = q.Set(assignments...)

	if s.Source != nil {
		base, joins, err := convertSource(s.Source)
		if err != nil {
			return nil, err
		}
		if base != nil {
			q = q.From(base)
		}
		for _, j := range joins {
			switch j.Kind {
			case "JOIN", "CROSS JOIN":
				q = q.Join(j.Source, j.On)
			case "LEFT JOIN":
				q = q.LeftJoin(j.Source, j.On)
			default:
				return nil, sqlparse.Unsupportedf("%s in UPDATE ... FROM", j.Kind)
			}
		}
	}

	if s.WhereExpr != nil {
		where, err := convertExpr(s.WhereExpr)
		if err != nil {
			return nil, err
		}
		q = q.Where(where)
	}

	if s.WithClause != nil {
		ctes, err := convertWith(s.WithClause)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	if s.ReturningClause != nil {
		returning, err := convertReturning(s.ReturningClause)
		if err != nil {
			return nil, err
		}
		q = q.Returning(returning...)
	}
	return q, nil
}

func convertDelete(s *rsql.DeleteStatement) (sql.Query, error) {
	// SQLite's optional ORDER BY / LIMIT on DELETE is not modeled.
	if len(s.OrderingTerms) > 0 || s.LimitExpr != nil {
		return nil, sqlparse.Unsupported("DELETE with ORDER BY / LIMIT")
	}

	q := sql.DeleteFrom(tableFromQualified(s.Table))

	if s.WhereExpr != nil {
		where, err := convertExpr(s.WhereExpr)
		if err != nil {
			return nil, err
		}
		q = q.Where(where)
	}

	if s.WithClause != nil {
		ctes, err := convertWith(s.WithClause)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	if s.ReturningClause != nil {
		returning, err := convertReturning(s.ReturningClause)
		if err != nil {
			return nil, err
		}
		q = q.Returning(returning...)
	}
	return q, nil
}

// convertAssignments turns SET / DO UPDATE SET assignments into sql.Assignment.
// When excluded is true, references to excluded.<col> map to sql.Excluded.
func convertAssignments(assignments []*rsql.Assignment, excluded bool) ([]sql.Assignment, error) {
	out := make([]sql.Assignment, 0, len(assignments))
	for _, a := range assignments {
		if len(a.Columns) != 1 {
			// Tuple assignment "(a, b) = (...)" is not modeled.
			return nil, sqlparse.Unsupported("multi-column assignment")
		}
		value, err := convertAssignmentValue(a.Expr, excluded)
		if err != nil {
			return nil, err
		}
		out = append(out, sql.Set(a.Columns[0].Name, value))
	}
	return out, nil
}

// convertAssignmentValue converts an assignment's right-hand side, mapping an
// excluded.<col> reference to sql.Excluded inside an upsert DO UPDATE.
func convertAssignmentValue(node rsql.Expr, excluded bool) (sql.Expression, error) {
	if excluded {
		if ref, ok := node.(*rsql.QualifiedRef); ok && ref.Schema == nil &&
			strings.EqualFold(ref.Table.Name, "excluded") && !ref.Star.IsValid() {
			return sql.Excluded(ref.Column.Name), nil
		}
	}
	return convertExpr(node)
}

func convertReturning(r *rsql.ReturningClause) ([]sql.Selection, error) {
	return convertColumns(r.Columns)
}
