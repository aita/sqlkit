package querytrace

// StmtKind classifies a SQL statement by the kind of statement it represents.
// It is a read-only label for tracing and analysis (query budgets, N+1
// detection, read-only enforcement) derived from a parsed query's shape.
type StmtKind int

const (
	// StmtUnknown is a statement of an unrecognized kind, or one that could not
	// be analyzed.
	StmtUnknown StmtKind = iota
	// StmtSelect is a row-returning read: SELECT, a set operation (UNION etc.),
	// or a standalone VALUES.
	StmtSelect
	// StmtInsert is an INSERT.
	StmtInsert
	// StmtUpdate is an UPDATE.
	StmtUpdate
	// StmtDelete is a DELETE.
	StmtDelete
	// StmtDDL is a schema statement (CREATE/ALTER/DROP/TRUNCATE/COMMENT).
	StmtDDL
)

// String renders the kind for logs and reports.
func (k StmtKind) String() string {
	switch k {
	case StmtSelect:
		return "select"
	case StmtInsert:
		return "insert"
	case StmtUpdate:
		return "update"
	case StmtDelete:
		return "delete"
	case StmtDDL:
		return "ddl"
	default:
		return "unknown"
	}
}

// IsRead reports whether the statement only reads rows.
func (k StmtKind) IsRead() bool { return k == StmtSelect }

// IsWrite reports whether the statement modifies data (INSERT/UPDATE/DELETE).
func (k StmtKind) IsWrite() bool {
	return k == StmtInsert || k == StmtUpdate || k == StmtDelete
}

// filtersRows reports whether the statement selects the rows it acts on through a
// WHERE clause — a SELECT, UPDATE, or DELETE. An INSERT does not (it names its
// rows directly), so the tenant predicate check skips it.
func (k StmtKind) filtersRows() bool {
	return k == StmtSelect || k == StmtUpdate || k == StmtDelete
}

// Shape is a read-only description of a parsed query's structure, used to drive
// tracing and analysis without recompiling the query or re-parsing its SQL.
//
// Two scopes are at play, by design:
//
//   - Tables is aggregated across the whole tree: it follows FROM/JOIN
//     subqueries, set-operation arms, CTE bodies, and the subqueries embedded in
//     expressions, because "what does this statement touch?" wants every base
//     table.
//   - The boolean flags describe the top-level statement node only. A WHERE,
//     JOIN, or LIMIT inside a subquery, a CTE body, or one arm of a UNION is
//     that nested query's property, not the outer statement's.
//
// So a UNION reports HasWhere/HasJoin false and HasLimit only for a LIMIT on the
// UNION itself, even when its arms filter, join, or limit.
type Shape struct {
	// Kind is the statement kind.
	Kind StmtKind
	// Tables are the base tables the statement references anywhere in its tree,
	// in source order with duplicates removed. A reference to a CTE is not a base
	// table, so CTE names are excluded.
	Tables []string
	// HasWhere reports whether the top-level SELECT/UPDATE/DELETE carries a WHERE
	// clause. It backs the update_without_where / delete_without_where checks. It
	// is always false for a set operation, which has no top-level WHERE.
	HasWhere bool
	// HasJoin reports whether the top-level statement carries a JOIN clause.
	HasJoin bool
	// HasLimit reports whether the top-level SELECT or set operation carries a
	// LIMIT. A LIMIT on a UNION arm belongs to that arm and is not reflected here.
	HasLimit bool
	// HasReturning reports whether a write carries a RETURNING clause.
	HasReturning bool
	// SelectStar reports whether the top-level SELECT projects every column with a
	// "*" (qualified or not) in its select list. It backs the select_star warning.
	// It is always false for a non-SELECT and for a set operation, whose arms own
	// their own select lists.
	SelectStar bool
}
