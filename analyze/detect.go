package analyze

import (
	"strings"

	"github.com/aita/sqlkit/sql"
)

// SourceRef is one table source visible in a query scope: a base table named in
// a FROM/JOIN/USING clause or as the target of an UPDATE/DELETE/INSERT/MERGE,
// together with the alias it is bound to. It is the unit that gives a column
// reference's qualifier something to resolve against.
//
// A derived-table source — a subquery or an inline VALUES — names no base table,
// so Table is empty; for a subquery, Subquery holds the inner query (its own
// scope), which a resolver may descend into to resolve the alias's columns.
type SourceRef struct {
	// Schema is the source's schema, when its name was schema-qualified; "" otherwise.
	Schema sql.Ident
	// Table is the base table name, or "" for a derived (subquery or VALUES) source.
	Table sql.Ident
	// Alias is the name the source is bound to (its AS alias), or "" when it is
	// referenced by its own table name.
	Alias sql.Ident
	// Subquery is the inner query of a derived-table source, or nil for a base
	// table or VALUES source.
	Subquery sql.Query
}

// ColumnRef is a column reference as written in a statement: its qualifier — the
// table name or alias before the final dot, "" when the reference is unqualified
// — and the bare column name. A reference qualified by schema and table
// ("public.users.id") keeps the whole prefix in Qualifier ("public.users").
//
// Case is preserved as written; the names are sql.Ident, so compare them with
// Ident.Equal rather than == (an unquoted SQL identifier folds case).
type ColumnRef struct {
	Qualifier sql.Ident
	Name      sql.Ident
	// SrcPos is the byte offset of the reference in the source SQL, or sql.NoPos
	// when it has no recorded position — a reference carried in the AST as a plain
	// name rather than an expression node (a USING column, an INSERT/MERGE column
	// list, a SET target), or one built rather than parsed.
	SrcPos int
}

// SourcesOf returns the table sources in q's own scope, in source order: the
// FROM/JOIN sources of a SELECT, the target and FROM/JOIN sources of an
// UPDATE/DELETE, the target of an INSERT, and the target and USING source of a
// MERGE. It does not descend into subqueries — each subquery is its own scope,
// reachable through a returned SourceRef.Subquery — and a set operation (whose
// arms are subqueries) yields no direct sources.
//
// A FROM/JOIN reference to a CTE presents as a base-table SourceRef here, because
// the scope of CTE names is not known from q alone; a resolver that holds the
// schema treats a name that matches no table as not a base table.
func SourcesOf(q sql.Query) []SourceRef {
	var out []SourceRef
	add := func(src sql.Source) {
		if r, ok := sourceRef(src); ok {
			out = append(out, r)
		}
	}
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		add(v.From)
		for _, j := range v.Joins {
			add(j.Source)
		}
	case sql.UpdateStmt:
		out = append(out, tableRef(v.Table, ""))
		for _, s := range v.From {
			add(s)
		}
		for _, j := range v.Joins {
			add(j.Source)
		}
	case sql.DeleteStmt:
		out = append(out, tableRef(v.Table, ""))
		for _, s := range v.Using {
			add(s)
		}
		for _, j := range v.Joins {
			add(j.Source)
		}
	case sql.InsertStmt:
		out = append(out, tableRef(v.Table, ""))
	case sql.MergeStmt:
		out = append(out, tableRef(v.Target, v.TargetAlias))
		add(v.Using)
	default:
		// CompoundStmt arms, ValuesQuery, and DDL have no immediate base-table
		// sources of their own.
	}
	return out
}

// Access is whether a column reference reads or writes the column.
type Access int

const (
	// Read is a column whose value is read: a predicate, the SELECT list, a
	// GROUP BY/ORDER BY term, the right-hand side of an assignment, an inserted
	// value, a RETURNING item.
	Read Access = iota
	// Write is a column whose value is set: the target of an UPDATE/MERGE SET
	// assignment, or a column named in an INSERT/MERGE column list.
	Write
)

// String renders the access for logs.
func (a Access) String() string {
	if a == Write {
		return "write"
	}
	return "read"
}

// Clause names the part of a statement a column reference appears in, so a caller
// can tell a WHERE predicate from a SET target from a RETURNING item.
type Clause int

const (
	// ClauseUnknown is the zero value, for a reference whose clause was not classified.
	ClauseUnknown   Clause = iota
	ClauseSelect           // SELECT list (and DISTINCT ON)
	ClauseJoinOn           // JOIN ... ON / USING
	ClauseWhere            // WHERE
	ClauseGroupBy          // GROUP BY
	ClauseHaving           // HAVING
	ClauseOrderBy          // ORDER BY
	ClauseWindow           // WINDOW / OVER partitions and ordering
	ClauseSet              // UPDATE/MERGE/ON CONFLICT SET assignment
	ClauseInsert           // INSERT/MERGE target column list
	ClauseValues           // INSERT/MERGE inserted values
	ClauseConflict         // ON CONFLICT target / predicate
	ClauseMergeOn          // MERGE ... ON / WHEN condition
	ClauseReturning        // RETURNING
)

// String renders the clause for logs.
func (c Clause) String() string {
	switch c {
	case ClauseSelect:
		return "select"
	case ClauseJoinOn:
		return "join_on"
	case ClauseWhere:
		return "where"
	case ClauseGroupBy:
		return "group_by"
	case ClauseHaving:
		return "having"
	case ClauseOrderBy:
		return "order_by"
	case ClauseWindow:
		return "window"
	case ClauseSet:
		return "set"
	case ClauseInsert:
		return "insert"
	case ClauseValues:
		return "values"
	case ClauseConflict:
		return "conflict"
	case ClauseMergeOn:
		return "merge_on"
	case ClauseReturning:
		return "returning"
	default:
		return "unknown"
	}
}

// ColumnUse is one occurrence of a column reference, located: the reference
// itself, the clause it appears in, and whether it is read or written. It is what
// WalkColumns reports, so a caller knows not just which columns a statement
// touches but where and how.
type ColumnUse struct {
	Column ColumnRef
	Clause Clause
	Access Access
}

// ColumnRefOf returns the column reference a ColumnExpression names: its qualifier
// and bare name split on the final dot, carrying the node's source position. It is
// the conversion the column-walking passes share, so a caller that already holds a
// ColumnExpression (a rewrite pass) resolves it the same way WalkColumns does.
func ColumnRefOf(c sql.ColumnExpression) ColumnRef {
	qualifier, name := splitColumn(sql.Ident(c.Name))
	return ColumnRef{Qualifier: qualifier, Name: name, SrcPos: c.Pos()}
}

// NamedColumn returns an unqualified column reference for a column named directly
// by string rather than by an expression node — a USING column, an INSERT/MERGE
// column-list entry, or a SET target. The AST carries these as bare names, so the
// reference has no source position.
func NamedColumn(name string) ColumnRef {
	return ColumnRef{Name: sql.Ident(name), SrcPos: sql.NoPos}
}

// TargetScope is the single-source scope of a statement's target table — the scope
// to resolve the columns an INSERT/UPDATE/MERGE assigns against, since they always
// belong to the target, never a joined source.
func TargetScope(t sql.Table) []SourceRef {
	return []SourceRef{tableRef(t, "")}
}

// Columns returns the column uses in q's own clauses, deduplicated in first-seen
// order. It covers the SELECT list, JOIN conditions and USING columns,
// WHERE/HAVING/GROUP BY/ORDER BY and window clauses, the assigned columns and
// values of INSERT/UPDATE/MERGE, ON CONFLICT targets, and RETURNING items. It
// does not descend into subqueries — each subquery is its own scope, walked on
// its own (see Walk and SourcesOf).
//
// It is best-effort over the expression nodes WalkExpr models, so an exotic
// expression may under-report.
func Columns(q sql.Query) []ColumnUse {
	var uses []ColumnUse
	WalkColumns(q, func(u ColumnUse) { uses = append(uses, u) })
	return dedupeUses(uses)
}

// WalkColumns calls visit for every column use in q's own clauses, in occurrence
// order and without deduplication — each tagged with the clause it appears in and
// whether it is read or written. It is the streaming form of Columns, for callers
// that want each occurrence (a rewrite pass), not the distinct set. Like Columns
// it does not descend into subqueries.
func WalkColumns(q sql.Query, visit func(ColumnUse)) {
	// expr reports the column references inside an expression at clause/access.
	expr := func(clause Clause, access Access, e sql.Expression) {
		WalkExpr(e, func(x sql.Expression) {
			if c, ok := x.(sql.ColumnExpression); ok {
				visit(ColumnUse{Column: ColumnRefOf(c), Clause: clause, Access: access})
			}
		})
	}
	named := func(clause Clause, access Access, n string) {
		if n != "" {
			visit(ColumnUse{Column: NamedColumn(n), Clause: clause, Access: access})
		}
	}
	exprs := func(clause Clause, access Access, es []sql.Expression) {
		for _, e := range es {
			expr(clause, access, e)
		}
	}
	sels := func(clause Clause, ss []sql.Selection) {
		for _, s := range ss {
			walkSelectionExpr(s, func(e sql.Expression) { expr(clause, Read, e) })
		}
	}
	joins := func(js []sql.JoinClause) {
		for _, j := range js {
			expr(ClauseJoinOn, Read, j.On)
			for _, u := range j.Using {
				named(ClauseJoinOn, Read, u)
			}
		}
	}
	// assigns reports a SET clause: the target column (and any indirected target)
	// is written, the assigned value is read.
	assigns := func(as []sql.Assignment) {
		for _, a := range as {
			named(ClauseSet, Write, a.Column)
			expr(ClauseSet, Write, a.Target)
			expr(ClauseSet, Read, a.Value)
		}
	}
	orderTerms := func(clause Clause, terms []sql.OrderTerm) {
		for _, t := range terms {
			expr(clause, Read, t.Expr)
		}
	}

	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		sels(ClauseSelect, v.Columns)
		joins(v.Joins)
		expr(ClauseWhere, Read, v.Where)
		expr(ClauseHaving, Read, v.Having)
		exprs(ClauseGroupBy, Read, v.GroupBy)
		exprs(ClauseSelect, Read, v.DistinctOn)
		orderTerms(ClauseOrderBy, v.OrderBy)
		for _, w := range v.Windows {
			exprs(ClauseWindow, Read, w.Partitions)
			orderTerms(ClauseWindow, w.OrderByTerms)
		}
	case sql.CompoundStmt:
		orderTerms(ClauseOrderBy, v.OrderBy)
	case sql.InsertStmt:
		for _, c := range v.Columns {
			named(ClauseInsert, Write, c)
		}
		for _, row := range v.Rows {
			exprs(ClauseValues, Read, row)
		}
		if v.Conflict != nil {
			for _, c := range v.Conflict.Target {
				named(ClauseConflict, Read, c)
			}
			exprs(ClauseConflict, Read, v.Conflict.TargetExprs)
			assigns(v.Conflict.Sets)
			expr(ClauseConflict, Read, v.Conflict.Where)
		}
		sels(ClauseReturning, v.Returning)
	case sql.UpdateStmt:
		assigns(v.Sets)
		joins(v.Joins)
		expr(ClauseWhere, Read, v.Where)
		sels(ClauseReturning, v.Returning)
	case sql.DeleteStmt:
		joins(v.Joins)
		expr(ClauseWhere, Read, v.Where)
		sels(ClauseReturning, v.Returning)
	case sql.MergeStmt:
		expr(ClauseMergeOn, Read, v.On)
		for _, w := range v.When {
			expr(ClauseMergeOn, Read, w.Condition)
			assigns(w.Sets)
			for _, c := range w.InsertColumns {
				named(ClauseInsert, Write, c)
			}
			exprs(ClauseValues, Read, w.InsertValues)
		}
		sels(ClauseReturning, v.Returning)
	default:
		// ValuesQuery and DDL statements carry no column references to resolve.
	}
}

// sourceRef converts a FROM/JOIN Source into a SourceRef, reporting ok=false for
// a nil source. A subquery source carries its inner query; a VALUES source and a
// base table carry no Subquery.
func sourceRef(src sql.Source) (SourceRef, bool) {
	if src == nil {
		return SourceRef{}, false
	}
	e := src.SQLSource()
	switch {
	case e.Query != nil:
		return SourceRef{Alias: sql.Ident(e.Alias), Subquery: e.Query}, true
	case e.Values != nil:
		return SourceRef{Alias: sql.Ident(e.Alias)}, true
	default:
		schema, table := splitTable(sql.Ident(e.Name))
		return SourceRef{Schema: schema, Table: table, Alias: sql.Ident(e.Alias)}, true
	}
}

// tableRef builds a base-table SourceRef from a statement's target table.
func tableRef(t sql.Table, alias string) SourceRef {
	return SourceRef{Schema: sql.Ident(t.Schema), Table: sql.Ident(t.Name), Alias: sql.Ident(alias)}
}

// splitTable splits a possibly schema-qualified table name into its schema and
// bare table name; the schema is "" when the name is unqualified.
func splitTable(name sql.Ident) (schema, table sql.Ident) {
	if i := strings.LastIndexByte(string(name), '.'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// splitColumn splits a possibly qualified column reference into the qualifier
// (everything before the final dot) and the bare column name; the qualifier is ""
// when the reference is unqualified.
func splitColumn(name sql.Ident) (qualifier, bare sql.Ident) {
	if i := strings.LastIndexByte(string(name), '.'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// dedupeUses removes duplicate uses (same column reference, clause, and access)
// while preserving first-seen order, returning nil for an empty input. Source
// position is not part of the identity: two occurrences of the same column in the
// same clause are one use, and the first one's SrcPos is kept.
func dedupeUses(uses []ColumnUse) []ColumnUse {
	if len(uses) == 0 {
		return nil
	}
	type key struct {
		qualifier, name sql.Ident
		clause          Clause
		access          Access
	}
	seen := make(map[key]struct{}, len(uses))
	out := uses[:0]
	for _, u := range uses {
		k := key{u.Column.Qualifier, u.Column.Name, u.Clause, u.Access}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, u)
	}
	return out
}
