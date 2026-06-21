package analysis

import (
	"strings"

	"github.com/aita/sqlkit/sql"
)

// TopLevelWhere returns the WHERE expression of a statement's top-level node, or
// nil when the statement has none (a set operation, INSERT, VALUES, or DDL). It
// reads the query's own clauses; it never descends into subqueries, because a
// predicate that scopes the outer statement lives in that statement's own WHERE.
func TopLevelWhere(q sql.Query) sql.Expression {
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return v.Where
	case sql.UpdateStmt:
		return v.Where
	case sql.DeleteStmt:
		return v.Where
	default:
		return nil
	}
}

// FilterColumns returns the bare, lower-cased column names referenced in the
// statement's top-level WHERE clause, deduplicated. It is best-effort: it walks
// the WHERE expression with sql.WalkExpr (which does not descend into
// subqueries), so a predicate node it does not model may under-report. nil when
// there is no WHERE.
func FilterColumns(q sql.Query) []string {
	where := TopLevelWhere(q)
	if where == nil {
		return nil
	}
	var names []string
	sql.WalkExpr(where, func(e sql.Expression) {
		if c, ok := e.(sql.ColumnExpression); ok {
			names = append(names, BareColumn(c.Name.String()))
		}
	})
	return dedupe(names)
}

// MaxInList returns the length of the longest IN (...) value list in the
// statement's top-level WHERE, or 0 when there is none. A subquery IN
// ("col IN (SELECT ...)") carries no value list and does not count.
func MaxInList(q sql.Query) int {
	where := TopLevelWhere(q)
	if where == nil {
		return 0
	}
	max := 0
	sql.WalkExpr(where, func(e sql.Expression) {
		if in, ok := e.(sql.InExpression); ok && in.Subquery == nil {
			if n := len(in.Items); n > max {
				max = n
			}
		}
	})
	return max
}

// WherePredicates returns the top-level WHERE facts FilterColumns and MaxInList
// report, gathered in a single walk of the clause. It is the form a caller that
// wants both (querytrace, deriving its per-statement warnings) uses, to avoid
// traversing the same WHERE expression twice. The results are identical to
// calling FilterColumns and MaxInList separately; both are zero when there is no
// WHERE.
func WherePredicates(q sql.Query) (filterColumns []string, maxInList int) {
	where := TopLevelWhere(q)
	if where == nil {
		return nil, 0
	}
	var names []string
	sql.WalkExpr(where, func(e sql.Expression) {
		switch x := e.(type) {
		case sql.ColumnExpression:
			names = append(names, BareColumn(x.Name.String()))
		case sql.InExpression:
			if x.Subquery == nil && len(x.Items) > maxInList {
				maxInList = len(x.Items)
			}
		}
	})
	return dedupe(names), maxInList
}

// BareColumn returns the unqualified, lower-cased column name from a possibly
// qualified reference ("public.users.tenant_id" -> "tenant_id"). Matching is
// case-insensitive because an unquoted SQL identifier folds case.
func BareColumn(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// NullEqualityPredicate reports whether the statement's top-level WHERE compares
// a value against a NULL literal with = or <> (x = NULL / x <> NULL). SQL
// evaluates such a comparison to unknown rather than matching NULL, so the
// predicate silently drops every row; IS NULL / IS NOT NULL is meant. It walks
// the top-level WHERE only, like the other predicate facts, with sql.WalkExpr
// (which does not descend into subqueries), and is best-effort: a NULL written as
// a non-literal expression is not detected. It backs the eq_null check.
func NullEqualityPredicate(q sql.Query) bool {
	where := TopLevelWhere(q)
	if where == nil {
		return false
	}
	found := false
	sql.WalkExpr(where, func(e sql.Expression) {
		if b, ok := e.(sql.BinaryExpression); ok && isNullEquality(b) {
			found = true
		}
	})
	return found
}

// isNullEquality reports whether b compares an operand against a NULL literal
// with = or <>, the mistake that should be IS NULL / IS NOT NULL.
func isNullEquality(b sql.BinaryExpression) bool {
	switch b.Operator {
	case "=", "<>", "!=":
		return isNullLiteral(b.Left) || isNullLiteral(b.Right)
	default:
		return false
	}
}

// isNullLiteral reports whether e is a bound NULL value, the form a parsed NULL
// constant takes.
func isNullLiteral(e sql.Expression) bool {
	v, ok := e.(sql.ValueExpression)
	return ok && v.Value == nil
}
