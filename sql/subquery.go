package sql

// Subquery is an aliased derived table built from a query with Query.As. Like a
// CTE, it doubles as a FROM/JOIN source and a handle for referencing its output
// columns (Col), but it is inlined as a parenthesized query at its use site
// rather than declared in a leading WITH clause:
//
//	sub := Select(Col("id")).From(users).Where(...).As("sub")
//	Select(sub.Col("id")).From(sub)
//
// emits SELECT "sub"."id" FROM (SELECT "id" FROM "users" WHERE ...) AS "sub".
type Subquery struct {
	alias string
	query Query
}

// As wraps this SELECT as an aliased subquery (derived table) named alias.
func (q SelectQuery) As(alias string) Subquery {
	return Subquery{alias: alias, query: q}
}

// As wraps this set-operation query as an aliased subquery named alias.
func (q CompoundQuery) As(alias string) Subquery {
	return Subquery{alias: alias, query: q}
}

// Alias returns the subquery's alias.
func (s Subquery) Alias() string { return s.alias }

// Col references one of the subquery's output columns, qualified by its alias.
func (s Subquery) Col(name string) ColumnExpression {
	return ColumnExpression{Name: s.alias + "." + name}
}

// SQLSource lets a Subquery be used as a FROM/JOIN source, emitting the
// parenthesized query followed by AS alias.
func (s Subquery) SQLSource() SourceExpression {
	return SourceExpression{Query: s.query, Alias: s.alias}
}
