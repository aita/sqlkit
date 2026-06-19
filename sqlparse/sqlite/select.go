package sqlite

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// convertSelect converts a single SELECT statement into a sql.SelectStmt,
// building the AST as a struct literal (the parser-facing construction path the
// sql package documents).
func convertSelect(s *rsql.SelectStatement) (sql.Query, error) {
	if s == nil {
		return nil, sqlparse.Unsupported("nil SELECT")
	}
	// Set operations carry their right operand in Compound and apply the trailing
	// ORDER BY / LIMIT to the combined result; that is left to a later pass.
	if s.Compound != nil || s.Union.IsValid() || s.Intersect.IsValid() || s.Except.IsValid() {
		return nil, sqlparse.Unsupported("compound SELECT (UNION/INTERSECT/EXCEPT)")
	}
	// VALUES (...), ... as a standalone statement is a distinct shape handled
	// elsewhere; reject it here so it round-trips rather than parsing as columns.
	if len(s.ValueLists) > 0 {
		return nil, sqlparse.Unsupported("VALUES statement")
	}
	if s.WithClause != nil {
		return nil, sqlparse.Unsupported("WITH clause")
	}
	if len(s.Windows) > 0 {
		return nil, sqlparse.Unsupported("WINDOW clause")
	}

	var stmt sql.SelectStmt

	columns, err := convertColumns(s.Columns)
	if err != nil {
		return nil, err
	}
	stmt.Columns = columns

	if s.Distinct.IsValid() {
		stmt.Distinct = true
	}

	if s.Source != nil {
		base, joins, err := convertSource(s.Source)
		if err != nil {
			return nil, err
		}
		stmt.From = base
		stmt.Joins = joins
	}

	if s.WhereExpr != nil {
		where, err := convertExpr(s.WhereExpr)
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}

	if len(s.GroupByExprs) > 0 {
		group := make([]sql.Expression, 0, len(s.GroupByExprs))
		for _, g := range s.GroupByExprs {
			expr, err := convertExpr(g)
			if err != nil {
				return nil, err
			}
			group = append(group, expr)
		}
		stmt.GroupBy = group
	}

	if s.HavingExpr != nil {
		having, err := convertExpr(s.HavingExpr)
		if err != nil {
			return nil, err
		}
		stmt.Having = having
	}

	if len(s.OrderingTerms) > 0 {
		terms, err := convertOrderingTerms(s.OrderingTerms)
		if err != nil {
			return nil, err
		}
		stmt.OrderBy = terms
	}

	if s.LimitExpr != nil {
		n, err := intLiteral(s.LimitExpr, "LIMIT")
		if err != nil {
			return nil, err
		}
		stmt.Limit = n
		stmt.HasLimit = true
	}
	if s.OffsetExpr != nil {
		n, err := intLiteral(s.OffsetExpr, "OFFSET")
		if err != nil {
			return nil, err
		}
		stmt.Offset = n
		stmt.HasOffset = true
	}

	return stmt, nil
}

// convertColumns converts the SELECT result-column list.
func convertColumns(columns []*rsql.ResultColumn) ([]sql.Selection, error) {
	out := make([]sql.Selection, 0, len(columns))
	for _, col := range columns {
		selection, err := convertResultColumn(col)
		if err != nil {
			return nil, err
		}
		out = append(out, selection)
	}
	return out, nil
}

func convertResultColumn(col *rsql.ResultColumn) (sql.Selection, error) {
	// "SELECT *": an unqualified star.
	if col.Star.IsValid() {
		return sql.Star(), nil
	}
	// "SELECT tbl.*": a qualified star carried as a QualifiedRef with Star set.
	if ref, ok := col.Expr.(*rsql.QualifiedRef); ok && ref.Star.IsValid() {
		if ref.Schema != nil {
			return nil, sqlparse.Unsupported("schema-qualified '*'")
		}
		return sql.StarOf(ref.Table.Name), nil
	}

	expr, err := convertSelectionExpr(col.Expr)
	if err != nil {
		return nil, err
	}
	if col.Alias != nil {
		return sql.As(expr, col.Alias.Name), nil
	}
	return expr, nil
}

// convertSelectionExpr converts a result-column expression to a Selection. Most
// expression nodes are also valid selections; a bare literal is not (as a bound
// value it is only an Expression), so it is rendered as a raw constant — faithful
// and common in EXISTS (SELECT 1 ...). An expression that is neither errors.
func convertSelectionExpr(node rsql.Expr) (sql.Selection, error) {
	if lit, ok := literalSelection(node); ok {
		return lit, nil
	}
	expr, err := convertExpr(node)
	if err != nil {
		return nil, err
	}
	selection, ok := expr.(sql.Selection)
	if !ok {
		return nil, sqlparse.Unsupportedf("select-list expression %T", node)
	}
	return selection, nil
}

// literalSelection renders a literal result column as a raw SQL constant, which
// is a Selection (a bound value is not). It reports false for non-literals.
func literalSelection(node rsql.Expr) (sql.Selection, bool) {
	switch lit := node.(type) {
	case *rsql.NumberLit:
		return sql.Raw(lit.Value), true
	case *rsql.NullLit:
		return sql.Raw("NULL"), true
	case *rsql.BoolLit:
		if lit.Value {
			return sql.Raw("TRUE"), true
		}
		return sql.Raw("FALSE"), true
	case *rsql.StringLit:
		return sql.Raw("'" + strings.ReplaceAll(lit.Value, "'", "''") + "'"), true
	default:
		return nil, false
	}
}

func convertOrderingTerms(terms []*rsql.OrderingTerm) ([]sql.OrderTerm, error) {
	out := make([]sql.OrderTerm, 0, len(terms))
	for _, t := range terms {
		expr, err := convertExpr(t.X)
		if err != nil {
			return nil, err
		}
		term := sql.Asc(expr)
		if t.Desc.IsValid() {
			term = sql.Desc(expr)
		}
		switch {
		case t.NullsFirst.IsValid():
			term = term.NullsFirst()
		case t.NullsLast.IsValid():
			term = term.NullsLast()
		}
		out = append(out, term)
	}
	return out, nil
}

// intLiteral reads a LIMIT/OFFSET operand, which sqlkit models as a plain int.
func intLiteral(node rsql.Expr, clause string) (int, error) {
	lit, ok := node.(*rsql.NumberLit)
	if !ok {
		return 0, sqlparse.Unsupportedf("non-integer %s", clause)
	}
	n, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, sqlparse.Unsupportedf("non-integer %s", clause)
	}
	return n, nil
}
