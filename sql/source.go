package sql

// Source is a table, subquery, or other FROM/JOIN source.
type Source interface {
	SQLSource() SourceExpression
}

// SourceExpression is the compiled representation of a FROM/JOIN source.
type SourceExpression struct {
	Name  string
	Query Query
	Alias string
}

// SQLSource marks SourceExpression as a Source.
func (s SourceExpression) SQLSource() SourceExpression { return s }

// Table is a table reference usable in FROM/JOIN clauses.
type Table struct {
	Schema string
	Name   string
}

// QualifiedName returns the table name with an optional schema prefix.
func (t Table) QualifiedName() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// SQLSource marks Table as a Source.
func (t Table) SQLSource() SourceExpression { return SourceExpression{Name: t.QualifiedName()} }

// Tbl creates a FROM/JOIN source that refers to a table or CTE by name, the
// source-side counterpart to Col. It is shorthand for Table{Name: name} — handy
// for referencing a recursive CTE by name from its own body, e.g.
// Join(Tbl("tree"), Eq(Col("nodes.parent_id"), Col("tree.id"))).
func Tbl(name string) Table { return Table{Name: name} }

// Alias creates a subquery source from a query and alias.
func Alias(query Query, alias string) SourceExpression {
	return SourceExpression{Query: query, Alias: alias}
}

// SourceAlias applies an alias to a table or other source.
func SourceAlias(source Source, alias string) SourceExpression {
	expr := source.SQLSource()
	expr.Alias = alias
	return expr
}

// Subquery creates a FROM/JOIN source from a query and alias.
// Deprecated: use Query.As(alias) for a reusable handle with a Col accessor,
// Alias(query, alias) for an ad-hoc aliased source, or pass query directly when
// no alias is needed.
func Subquery(query Query, alias string) SourceExpression { return Alias(query, alias) }
