package analyze

import "github.com/aita/sqlkit/sql"

// Tables returns the base tables a parsed query references anywhere in its tree,
// in source order with duplicates removed; nil when q is nil, unsourced, or of an
// unrecognized type. It reads the query's structure only, through the sql
// package's read-only introspection seam (the statement fields and
// sql.WalkSubqueries), never compiling or mutating q.
//
// The walk follows FROM/JOIN subqueries, set-operation arms, CTE bodies, and the
// subqueries embedded in WHERE/SELECT/HAVING expressions (correlated ones
// included). A reference to a CTE is not a base table, so CTE names are excluded.
func Tables(q sql.Query) []string {
	return tablesOf(q, nil)
}

// tablesOf returns the base tables of q resolved under scope — the CTE names
// visible from the enclosing queries. It is the recursion behind Tables and
// behind every nested query (a subquery, set-operation arm, or CTE body).
func tablesOf(q sql.Query, scope cteNameSet) []string {
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return selectTables(v, scope)
	case sql.CompoundStmt:
		return compoundTables(v, scope)
	case sql.InsertStmt:
		return insertTables(v, scope)
	case sql.UpdateStmt:
		return updateTables(v, scope)
	case sql.DeleteStmt:
		return deleteTables(v, scope)
	case nil, sql.ValuesQuery:
		return nil
	default:
		tables, _ := ddlTables(v)
		return tables
	}
}

func selectTables(q sql.SelectStmt, scope cteNameSet) []string {
	scope = scope.with(cteNames(q.With))
	t := tableCollector{scope: scope}
	t.cteBodies(q.With)
	t.source(q.From)
	for _, j := range q.Joins {
		t.source(j.Source)
		t.expr(j.On)
	}
	for _, s := range q.Columns {
		t.selection(s)
	}
	t.expr(q.Where)
	t.expr(q.Having)
	t.exprs(q.GroupBy)
	t.exprs(q.DistinctOn)
	t.orderTerms(q.OrderBy)
	for _, w := range q.Windows {
		t.exprs(w.Partitions)
		t.orderTerms(w.OrderByTerms)
	}
	return t.tables()
}

func compoundTables(q sql.CompoundStmt, scope cteNameSet) []string {
	t := tableCollector{scope: scope}
	t.query(q.Left)
	t.query(q.Right)
	t.orderTerms(q.OrderBy)
	return t.tables()
}

func insertTables(q sql.InsertStmt, scope cteNameSet) []string {
	t := tableCollector{scope: scope}
	t.add(q.Table.QualifiedName())
	t.query(q.Select)
	for _, row := range q.Rows {
		t.exprs(row)
	}
	if q.Conflict != nil {
		for _, a := range q.Conflict.Sets {
			t.expr(a.Value)
		}
		t.expr(q.Conflict.Where)
	}
	for _, s := range q.Returning {
		t.selection(s)
	}
	return t.tables()
}

func updateTables(q sql.UpdateStmt, scope cteNameSet) []string {
	scope = scope.with(cteNames(q.With))
	t := tableCollector{scope: scope}
	t.cteBodies(q.With)
	t.add(q.Table.QualifiedName())
	for _, s := range q.From {
		t.source(s)
	}
	for _, j := range q.Joins {
		t.source(j.Source)
		t.expr(j.On)
	}
	for _, a := range q.Sets {
		t.expr(a.Value)
	}
	t.expr(q.Where)
	for _, s := range q.Returning {
		t.selection(s)
	}
	return t.tables()
}

func deleteTables(q sql.DeleteStmt, scope cteNameSet) []string {
	scope = scope.with(cteNames(q.With))
	t := tableCollector{scope: scope}
	t.cteBodies(q.With)
	t.add(q.Table.QualifiedName())
	for _, s := range q.Using {
		t.source(s)
	}
	for _, j := range q.Joins {
		t.source(j.Source)
		t.expr(j.On)
	}
	t.expr(q.Where)
	for _, s := range q.Returning {
		t.selection(s)
	}
	return t.tables()
}

// cteNameSet is the set of CTE names visible to a query — those declared in its
// own WITH clause and any enclosing one. A FROM/JOIN reference whose name is in
// scope denotes that CTE, not a base table, so it is kept out of the result.
type cteNameSet map[string]struct{}

// with returns a scope that also contains names. It copies on write so sibling
// queries do not see each other's CTE names; the receiver is returned unchanged
// when names is empty.
func (s cteNameSet) with(names []string) cteNameSet {
	if len(names) == 0 {
		return s
	}
	next := make(cteNameSet, len(s)+len(names))
	for name := range s {
		next[name] = struct{}{}
	}
	for _, name := range names {
		next[name] = struct{}{}
	}
	return next
}

func (s cteNameSet) has(name string) bool {
	_, ok := s[name]
	return ok
}

// cteNames returns the declared names of ctes.
func cteNames(ctes []sql.CTE) []string {
	if len(ctes) == 0 {
		return nil
	}
	names := make([]string, len(ctes))
	for i, c := range ctes {
		names[i] = c.Name()
	}
	return names
}

// tableCollector accumulates base-table names while honoring CTE scope, walking
// the query through the sql introspection seam.
type tableCollector struct {
	scope cteNameSet
	names []string
}

// add records a bare table name unless it resolves to a CTE in scope. Implementing
// sql.Source does not make a node a base table: a CTE reference also presents as a
// source, so a name in scope denotes that CTE and is skipped.
func (t *tableCollector) add(name string) {
	if name == "" || t.scope.has(name) {
		return
	}
	t.names = append(t.names, name)
}

// cteBodies collects the base tables referenced inside each CTE body.
func (t *tableCollector) cteBodies(ctes []sql.CTE) {
	for _, c := range ctes {
		t.query(c.Body())
	}
}

// query collects the tables of a nested query (a subquery, set-operation arm, or
// CTE body), resolving it under the current scope so it still sees the enclosing
// CTE names.
func (t *tableCollector) query(q sql.Query) {
	for _, name := range tablesOf(q, t.scope) {
		t.add(name)
	}
}

// source collects the table a FROM/JOIN source names, or, for a subquery source,
// the tables of its inner query.
func (t *tableCollector) source(src sql.Source) {
	if src == nil {
		return
	}
	e := src.SQLSource()
	t.add(e.Name.String())
	if e.Query != nil {
		t.query(e.Query)
	}
}

// expr collects the tables of any subquery embedded in e.
func (t *tableCollector) expr(e sql.Expression) {
	sql.WalkSubqueries(e, t.query)
}

func (t *tableCollector) exprs(es []sql.Expression) {
	for _, e := range es {
		t.expr(e)
	}
}

func (t *tableCollector) orderTerms(terms []sql.OrderTerm) {
	for _, term := range terms {
		t.expr(term.Expr)
	}
}

// selection collects the tables of any subquery embedded in a SELECT-list item.
func (t *tableCollector) selection(s sql.Selection) {
	sql.WalkSelectionSubqueries(s, t.query)
}

// tables returns the collected names, deduplicated in first-seen order.
func (t *tableCollector) tables() []string { return dedupe(t.names) }

// ddlTables reports whether q is a DDL statement and, if so, the tables it
// names, read through the sql DDL introspection seam.
func ddlTables(q sql.Query) (tables []string, ok bool) {
	switch v := sql.Unwrap(q).(type) {
	case sql.CreateTableStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.CreateViewStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.CreateIndexStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.DropIndexStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.DropForeignKeyStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.CommentOnTableStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.CommentOnColumnStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.AlterTableStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.DropTableStmt:
		return qualifiedNames(v.Tables), true
	case sql.DropViewStmt:
		return []string{v.Table.QualifiedName()}, true
	case sql.TruncateStmt:
		return qualifiedNames(v.Tables), true
	case sql.CreateSchemaStmt, sql.DropSchemaStmt:
		return nil, true
	default:
		return nil, false
	}
}

func qualifiedNames(tables []sql.Table) []string {
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.QualifiedName())
	}
	return names
}

// dedupe removes duplicate names while preserving first-seen order. It returns
// nil for an empty input so an unsourced query reports no tables.
func dedupe(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := names[:0]
	for _, n := range names {
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
