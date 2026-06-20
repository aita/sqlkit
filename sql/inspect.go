package sql

// This file is the read-only introspection seam of the query AST. The query
// builders keep their clauses in unexported fields so the only way to build or
// change a query is through the validated fluent methods; that also means code
// outside this package cannot read a query's structure directly. The Parts
// accessors below return a read-only snapshot of every clause of a query, with
// slices defensively copied, so analysis tools (see Walk) can inspect the whole
// AST without the package exposing — and freezing as API — its internal field
// layout, and without letting callers mutate a built query.

// SELECT and set-operation statements expose their structure as the public
// fields of SelectStmt and CompoundStmt directly (read them, or traverse with
// Walk/Decompose); they need no Parts snapshot. The Parts accessors below remain
// for the statement types whose builders still keep unexported fields.

// ConflictClause is the INSERT upsert action (ON CONFLICT / ON DUPLICATE KEY)
// carried by InsertStmt.Conflict. At most one of Target, TargetExprs, or
// TargetConstraint is set. DoUpdate distinguishes DO UPDATE (with Sets and an
// optional Where) from DO NOTHING.
type ConflictClause struct {
	posField
	Target           []Ident
	TargetExprs      []Expression
	TargetConstraint Ident
	DoUpdate         bool
	Sets             []Assignment
	Where            Expression
}

// MergeWhenClause is one MERGE WHEN clause as carried by MergeStmt.When. Match
// is "MATCHED", "NOT MATCHED", or "NOT MATCHED BY SOURCE"; Action is "UPDATE",
// "DELETE", "INSERT", "DO NOTHING", or "" when none was set. Sets applies to
// UPDATE; InsertColumns/InsertValues (or InsertDefault) to INSERT.
type MergeWhenClause struct {
	posField
	Match         string
	Condition     Expression
	Action        string
	Sets          []Assignment
	InsertColumns []Ident
	InsertValues  []Expression
	InsertDefault bool
}

// mergeWhenView converts a MergeWhen builder into its MergeWhenClause AST value.
func mergeWhenView(w MergeWhen) MergeWhenClause {
	match := map[mergeMatch]string{
		mergeMatched:            "MATCHED",
		mergeNotMatchedByTarget: "NOT MATCHED",
		mergeNotMatchedBySource: "NOT MATCHED BY SOURCE",
	}[w.match]
	action := map[mergeAction]string{
		mergeDoNothing: "DO NOTHING",
		mergeUpdate:    "UPDATE",
		mergeDelete:    "DELETE",
		mergeInsert:    "INSERT",
	}[w.action]
	return MergeWhenClause{
		Match:         match,
		Condition:     w.condition,
		Action:        action,
		Sets:          cloneSlice(w.sets),
		InsertColumns: cloneSlice(w.insertColumns),
		InsertValues:  cloneSlice(w.insertValues),
		InsertDefault: w.insertDefault,
	}
}

// Name returns the CTE's name, the identifier other clauses reference it by.
func (c CTE) Name() string { return c.name.String() }

// Body returns the query the CTE wraps.
func (c CTE) Body() Query { return c.query }

// OutputColumns returns the CTE's explicit output column list, or nil when none
// was declared.
func (c CTE) OutputColumns() []string { return identNames(c.columns) }

// IsRecursive reports whether the CTE was declared recursive.
func (c CTE) IsRecursive() bool { return c.recursive }

// cloneSlice returns a shallow copy of s, or nil when s is empty, so a snapshot
// never shares a backing array with the query it came from.
func cloneSlice[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return append([]T(nil), s...)
}

// cloneRows shallow-copies a row-of-expressions matrix.
func cloneRows(rows [][]Expression) [][]Expression {
	if len(rows) == 0 {
		return nil
	}
	out := make([][]Expression, len(rows))
	for i, r := range rows {
		out[i] = cloneSlice(r)
	}
	return out
}
