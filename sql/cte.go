package sql

// CTE is a named common table expression built from a query with Query.CTE.
// A CTE doubles as a FROM/JOIN source and a handle for referencing its output
// columns (Col). Attach it to a statement with WithCTE, which emits a leading
// WITH clause:
//
//	active := Select(Col("id")).From(users).Where(...).CTE("active")
//	Select(active.Col("id")).From(active).WithCTE(active)
type CTE struct {
	posField
	name      Ident
	columns   []Ident // optional explicit output column list
	query     Query
	recursive bool
}

// cteOptions accumulates the optional settings applied by CTEOption.
type cteOptions struct {
	columns   []Ident
	recursive bool
}

// CTEOption configures a CTE created with Query.CTE.
type CTEOption func(*cteOptions)

// Recursive marks the CTE as recursive. A WITH block containing any recursive
// CTE is emitted as WITH RECURSIVE. Build the recursive term by combining the
// base query with Union/UnionAll before calling CTE, referencing the CTE by its
// name (a plain Table/Col reference).
func Recursive() CTEOption {
	return func(o *cteOptions) { o.recursive = true }
}

// Columns declares an explicit output column list for the CTE,
// e.g. q.CTE("totals", Columns("org_id", "n")).
func Columns(names ...string) CTEOption {
	return func(o *cteOptions) { o.columns = idents(names) }
}

// CTE wraps this SELECT as a common table expression named name.
func (b SelectBuilder) CTE(name string, opts ...CTEOption) CTE {
	return newCTE(name, b.s, opts)
}

// CTE wraps this set-operation query as a common table expression. Use it for
// recursive CTEs, whose body is a UNION of the base and recursive terms.
func (b CompoundBuilder) CTE(name string, opts ...CTEOption) CTE {
	return newCTE(name, b.s, opts)
}

func newCTE(name string, query Query, opts []CTEOption) CTE {
	var o cteOptions
	for _, opt := range opts {
		opt(&o)
	}
	return CTE{name: NewIdent(name), columns: o.columns, query: query, recursive: o.recursive}
}

// Col references one of the CTE's output columns, qualified by the CTE name.
func (c CTE) Col(name string) ColumnExpression {
	return ColumnExpression{Name: NewIdent(c.name.String() + "." + name)}
}

// SQLSource lets a CTE be used as a FROM/JOIN source, referenced by name.
func (c CTE) SQLSource() SourceExpression { return SourceExpression{Name: c.name} }

// WithCTE prepends common table expressions to the SELECT, in the given order.
// Repeated calls append. List a CTE before any CTE whose body references it.
func (b SelectBuilder) WithCTE(ctes ...CTE) SelectBuilder {
	b.s.With = append(append([]CTE(nil), b.s.With...), ctes...)
	return b
}

// WithBody returns a copy of c with its defining query replaced by q.
func (c CTE) WithBody(q Query) CTE { c.query = q; return c }
