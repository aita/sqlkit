package querytrace

import (
	"strings"

	"github.com/aita/sqlkit/sql"
)

// topLevelWhere returns the WHERE expression of a statement's top-level node, or
// nil when the statement has none (a set operation, INSERT, VALUES, or DDL). It
// reads the query's own clauses through the sql Parts accessors; it never
// descends into subqueries, because a predicate that scopes the outer statement
// lives in that statement's own WHERE.
func topLevelWhere(q sql.Query) sql.Expression {
	switch v := derefQuery(q).(type) {
	case sql.SelectQuery:
		return v.Parts().Where
	case sql.UpdateQuery:
		return v.Parts().Where
	case sql.DeleteQuery:
		return v.Parts().Where
	default:
		return nil
	}
}

// topLevelFilterColumns returns the bare, lower-cased column names referenced in
// the statement's top-level WHERE clause, deduplicated. It is best-effort: it
// walks the common predicate node types and treats anything else (including
// subqueries) as a leaf, so an exotic predicate may under-report. nil when there
// is no WHERE.
func topLevelFilterColumns(q sql.Query) []string {
	where := topLevelWhere(q)
	if where == nil {
		return nil
	}
	var names []string
	walkPredicate(where, func(e sql.Expression) {
		if c, ok := e.(sql.ColumnExpression); ok {
			names = append(names, bareColumn(c.Name))
		}
	})
	return dedupe(names)
}

// maxInListLen returns the length of the longest IN (...) value list in the
// statement's top-level WHERE, or 0 when there is none. A subquery IN
// ("col IN (SELECT ...)") carries no value list and does not count. It is
// best-effort over the same predicate node set as topLevelFilterColumns.
func maxInListLen(q sql.Query) int {
	where := topLevelWhere(q)
	if where == nil {
		return 0
	}
	max := 0
	walkPredicate(where, func(e sql.Expression) {
		if in, ok := e.(sql.InExpression); ok && in.Subquery == nil {
			if n := len(in.Items); n > max {
				max = n
			}
		}
	})
	return max
}

// walkPredicate visits e and every expression nested within it in pre-order,
// recursing through the composite predicate, function, and value nodes the SQL
// AST builds for a WHERE clause. It does not descend into embedded subqueries —
// those are a nested query's concern, not the predicate's. Unrecognized nodes
// are treated as leaves, so the walk degrades gracefully on shapes it does not
// model rather than panicking.
func walkPredicate(e sql.Expression, visit func(sql.Expression)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case sql.BinaryExpression:
		walkPredicate(x.Left, visit)
		walkPredicate(x.Right, visit)
	case sql.LogicalExpression:
		walkEachPredicate(x.Operands, visit)
	case sql.NotExpression:
		walkPredicate(x.Operand, visit)
	case sql.UnaryExpression:
		walkPredicate(x.Operand, visit)
	case sql.NullPredicate:
		walkPredicate(x.Operand, visit)
	case sql.DistinctPredicate:
		walkPredicate(x.Left, visit)
		walkPredicate(x.Right, visit)
	case sql.BetweenExpression:
		walkPredicate(x.Operand, visit)
		walkPredicate(x.Low, visit)
		walkPredicate(x.High, visit)
	case sql.InExpression:
		walkPredicate(x.Left, visit)
		walkEachPredicate(x.Items, visit)
	case sql.QuantifiedExpression:
		walkPredicate(x.Left, visit)
	case sql.QuantifiedArrayExpression:
		walkPredicate(x.Left, visit)
		walkPredicate(x.Array, visit)
	case sql.ILikeExpression:
		walkPredicate(x.Left, visit)
		walkPredicate(x.Pattern, visit)
	case sql.RegexpExpression:
		walkPredicate(x.Left, visit)
		walkPredicate(x.Pattern, visit)
	case sql.SimilarToExpression:
		walkPredicate(x.Left, visit)
		walkPredicate(x.Pattern, visit)
	case sql.ConcatExpression:
		walkEachPredicate(x.Operands, visit)
	case sql.RowExpression:
		walkEachPredicate(x.Elements, visit)
	case sql.ArrayExpression:
		walkEachPredicate(x.Elements, visit)
	case sql.FuncExpression:
		walkEachPredicate(x.Args, visit)
	case sql.CastExpression:
		walkPredicate(x.Operand, visit)
	case sql.CaseExpression:
		walkPredicate(x.Operand, visit)
		for _, w := range x.Whens {
			walkPredicate(w.Condition, visit)
			walkPredicate(w.Result, visit)
		}
		walkPredicate(x.ElseValue, visit)
	default:
		// Leaf nodes (ColumnExpression, ValueExpression, RawExpression, an
		// embedded subquery, ...) carry no further predicate operands.
	}
}

func walkEachPredicate(es []sql.Expression, visit func(sql.Expression)) {
	for _, e := range es {
		walkPredicate(e, visit)
	}
}

// bareColumn returns the unqualified, lower-cased column name from a possibly
// qualified reference ("public.users.tenant_id" -> "tenant_id"). Matching is
// case-insensitive because an unquoted SQL identifier folds case.
func bareColumn(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// containsAny reports whether cols contains any of the bare column names in
// want. Both sides are compared as bare, lower-cased names.
func containsAny(cols, want []string) bool {
	if len(cols) == 0 || len(want) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		set[c] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[bareColumn(w)]; ok {
			return true
		}
	}
	return false
}
