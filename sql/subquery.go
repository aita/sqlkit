package sql

// DerivedTable is an aliased subquery built from a query with Query.As. Like a
// CTE, it doubles as a FROM/JOIN source and a handle for referencing its output
// columns (Col), but it is inlined as a parenthesized query at its use site
// rather than declared in a leading WITH clause:
//
//	sub := Select(Col("id")).From(users).Where(...).As("sub")
//	Select(sub.Col("id")).From(sub)
//
// emits SELECT "sub"."id" FROM (SELECT "id" FROM "users" WHERE ...) AS "sub".
type DerivedTable struct {
	posField
	alias string
	query Query
}

// As wraps this SELECT as an aliased subquery (derived table) named alias.
func (b SelectBuilder) As(alias string) DerivedTable {
	return DerivedTable{alias: alias, query: b.s}
}

// As wraps this set-operation query as an aliased subquery named alias.
func (b CompoundBuilder) As(alias string) DerivedTable {
	return DerivedTable{alias: alias, query: b.s}
}

// Alias returns the derived table's alias.
func (d DerivedTable) Alias() string { return d.alias }

// Col references one of the derived table's output columns, qualified by its alias.
func (d DerivedTable) Col(name string) ColumnExpression {
	return ColumnExpression{Name: NewIdent(d.alias + "." + name)}
}

// SQLSource lets a DerivedTable be used as a FROM/JOIN source, emitting the
// parenthesized query followed by AS alias.
func (d DerivedTable) SQLSource() SourceExpression {
	return SourceExpression{Query: d.query, Alias: NewIdent(d.alias)}
}
