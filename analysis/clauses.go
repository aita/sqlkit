package analysis

import "github.com/aita/sqlkit/sql"

// HasTopLevelWhere reports whether a statement's top-level node carries a WHERE
// clause — a SELECT, UPDATE, or DELETE. It is false for a set operation, INSERT,
// VALUES, or DDL, which have no top-level WHERE. A WHERE inside a subquery, a CTE
// body, or one arm of a UNION is that nested query's property, not the outer
// statement's, and the name says so: it asks only about the top-level node. It
// backs the update_without_where / delete_without_where checks.
func HasTopLevelWhere(q sql.Query) bool { return TopLevelWhere(q) != nil }

// HasTopLevelJoin reports whether a statement's top-level node carries a JOIN
// clause — a SELECT, UPDATE, or DELETE. A JOIN inside a subquery, a CTE body, or
// one arm of a UNION belongs to that nested query and is not counted.
func HasTopLevelJoin(q sql.Query) bool {
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return len(v.Joins) > 0
	case sql.UpdateStmt:
		return len(v.Joins) > 0
	case sql.DeleteStmt:
		return len(v.Joins) > 0
	default:
		return false
	}
}

// SelectsStar reports whether a top-level SELECT projects every column with a "*"
// (qualified or not) anywhere in its select list. It is false for a non-SELECT and
// for a set operation, whose arms own their own select lists. It backs the
// select_star check.
func SelectsStar(q sql.Query) bool {
	if v, ok := sql.Unwrap(q).(sql.SelectStmt); ok {
		return hasStar(v.Columns)
	}
	return false
}

// hasStar reports whether a select list projects a "*" item (qualified or not),
// unwrapping an AS alias. A "*" anywhere in the list counts: "SELECT users.*, id"
// is still an unbounded projection of one source.
func hasStar(cols []sql.Selection) bool {
	for _, c := range cols {
		if isStar(c) {
			return true
		}
	}
	return false
}

// isStar reports whether a single select-list item is a "*" selection, looking
// through an AS alias.
func isStar(s sql.Selection) bool {
	switch v := s.(type) {
	case sql.StarSelection:
		return true
	case sql.AliasedSelection:
		return isStar(v.Selection)
	default:
		return false
	}
}
