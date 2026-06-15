package querytrace

import "github.com/aita/sqlkit/sql"

// Describe returns the Shape of a parsed query. ok is false when q is nil or of
// an unrecognized type. It reads the query's structure only, through the sql
// package's read-only introspection seam (Query.Parts, sql.WalkSubqueries),
// never compiling or mutating q.
//
// Tables follows FROM/JOIN subqueries, set-operation arms, CTE bodies, and the
// subqueries embedded in WHERE/SELECT/HAVING expressions (correlated ones
// included). A reference to a CTE is not a base table, so CTE names are excluded.
func Describe(q sql.Query) (Shape, bool) {
	return describe(q, nil)
}

// cteNameSet is the set of CTE names visible to a query — those declared in its
// own WITH clause and any enclosing one. A FROM/JOIN reference whose name is in
// scope denotes that CTE, not a base table, so it is kept out of Shape.Tables.
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

func describe(q sql.Query, scope cteNameSet) (Shape, bool) {
	switch v := derefQuery(q).(type) {
	case nil:
		return Shape{}, false
	case sql.SelectQuery:
		return describeSelect(v, scope), true
	case sql.CompoundQuery:
		return describeCompound(v, scope), true
	case sql.ValuesQuery:
		return Shape{Kind: StmtSelect}, true
	case sql.InsertQuery:
		return describeInsert(v, scope), true
	case sql.UpdateQuery:
		return describeUpdate(v, scope), true
	case sql.DeleteQuery:
		return describeDelete(v, scope), true
	default:
		if tables, isDDL := ddlTables(v); isDDL {
			return Shape{Kind: StmtDDL, Tables: tables}, true
		}
		return Shape{}, false
	}
}

// derefQuery converts a pointer to a query node into its value form so the type
// switch need only handle value types.
func derefQuery(q sql.Query) sql.Query {
	switch p := q.(type) {
	case *sql.SelectQuery:
		return *p
	case *sql.CompoundQuery:
		return *p
	case *sql.InsertQuery:
		return *p
	case *sql.UpdateQuery:
		return *p
	case *sql.DeleteQuery:
		return *p
	case *sql.ValuesQuery:
		return *p
	default:
		return q
	}
}

func describeSelect(q sql.SelectQuery, scope cteNameSet) Shape {
	parts := q.Parts()
	scope = scope.with(cteNames(parts.With))
	t := tableCollector{scope: scope}
	t.cteBodies(parts.With)
	t.source(parts.From)
	for _, j := range parts.Joins {
		t.source(j.Source)
		t.expr(j.On)
	}
	for _, s := range parts.Columns {
		t.selection(s)
	}
	t.expr(parts.Where)
	t.expr(parts.Having)
	t.exprs(parts.GroupBy)
	t.exprs(parts.DistinctOn)
	t.orderTerms(parts.OrderBy)
	for _, w := range parts.Windows {
		t.exprs(w.Partitions)
		t.orderTerms(w.OrderByTerms)
	}
	return Shape{
		Kind:     StmtSelect,
		Tables:   t.tables(),
		HasWhere: parts.Where != nil,
		HasJoin:  len(parts.Joins) > 0,
		HasLimit: parts.Limit != nil,
	}
}

func describeCompound(q sql.CompoundQuery, scope cteNameSet) Shape {
	parts := q.Parts()
	t := tableCollector{scope: scope}
	t.query(parts.Left)
	t.query(parts.Right)
	t.orderTerms(parts.OrderBy)
	return Shape{
		Kind:     StmtSelect,
		Tables:   t.tables(),
		HasLimit: parts.Limit != nil,
	}
}

func describeInsert(q sql.InsertQuery, scope cteNameSet) Shape {
	parts := q.Parts()
	t := tableCollector{scope: scope}
	t.add(parts.Table.QualifiedName())
	t.query(parts.Select)
	for _, row := range parts.Rows {
		t.exprs(row)
	}
	if parts.Conflict != nil {
		for _, a := range parts.Conflict.Sets {
			t.expr(a.Value)
		}
		t.expr(parts.Conflict.Where)
	}
	for _, s := range parts.Returning {
		t.selection(s)
	}
	return Shape{
		Kind:         StmtInsert,
		Tables:       t.tables(),
		HasReturning: len(parts.Returning) > 0,
	}
}

func describeUpdate(q sql.UpdateQuery, scope cteNameSet) Shape {
	parts := q.Parts()
	scope = scope.with(cteNames(parts.With))
	t := tableCollector{scope: scope}
	t.cteBodies(parts.With)
	t.add(parts.Table.QualifiedName())
	for _, s := range parts.From {
		t.source(s)
	}
	for _, j := range parts.Joins {
		t.source(j.Source)
		t.expr(j.On)
	}
	for _, a := range parts.Sets {
		t.expr(a.Value)
	}
	t.expr(parts.Where)
	for _, s := range parts.Returning {
		t.selection(s)
	}
	return Shape{
		Kind:         StmtUpdate,
		Tables:       t.tables(),
		HasWhere:     parts.Where != nil,
		HasJoin:      len(parts.Joins) > 0,
		HasReturning: len(parts.Returning) > 0,
	}
}

func describeDelete(q sql.DeleteQuery, scope cteNameSet) Shape {
	parts := q.Parts()
	scope = scope.with(cteNames(parts.With))
	t := tableCollector{scope: scope}
	t.cteBodies(parts.With)
	t.add(parts.Table.QualifiedName())
	for _, s := range parts.Using {
		t.source(s)
	}
	for _, j := range parts.Joins {
		t.source(j.Source)
		t.expr(j.On)
	}
	t.expr(parts.Where)
	for _, s := range parts.Returning {
		t.selection(s)
	}
	return Shape{
		Kind:         StmtDelete,
		Tables:       t.tables(),
		HasWhere:     parts.Where != nil,
		HasJoin:      len(parts.Joins) > 0,
		HasReturning: len(parts.Returning) > 0,
	}
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
	if s, ok := describe(q, t.scope); ok {
		for _, name := range s.Tables {
			t.add(name)
		}
	}
}

// source collects the table a FROM/JOIN source names, or, for a subquery source,
// the tables of its inner query.
func (t *tableCollector) source(src sql.Source) {
	if src == nil {
		return
	}
	e := src.SQLSource()
	t.add(e.Name)
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
// names, read through the sql DDL introspection seam (Parts).
func ddlTables(q sql.Query) (tables []string, ok bool) {
	switch v := q.(type) {
	case sql.CreateTableStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.CreateViewStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.CreateIndexStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.DropIndexStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.DropForeignKeyStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.CommentOnTableStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.CommentOnColumnStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.AlterTableStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.DropTableStmt:
		return qualifiedNames(v.Parts().Tables), true
	case sql.DropViewStmt:
		return []string{v.Parts().Table.QualifiedName()}, true
	case sql.TruncateStmt:
		return qualifiedNames(v.Parts().Tables), true
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
// nil for an empty input so an unsourced query reports Tables == nil.
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
