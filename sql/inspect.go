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

// InsertParts is a read-only snapshot of every clause of an InsertQuery.
// Exactly one of Rows or Select carries the inserted data. It is returned by
// InsertQuery.Parts.
type InsertParts struct {
	Table     Table
	Columns   []string
	Rows      [][]Expression
	Select    Query
	Conflict  *ConflictClause
	Returning []Selection
}

// ConflictClause is a read-only snapshot of an INSERT upsert action
// (ON CONFLICT / ON DUPLICATE KEY). At most one of Target, TargetExprs, or
// TargetConstraint is set. DoUpdate distinguishes DO UPDATE (with Sets and an
// optional Where) from DO NOTHING.
type ConflictClause struct {
	Target           []string
	TargetExprs      []Expression
	TargetConstraint string
	DoUpdate         bool
	Sets             []Assignment
	Where            Expression
}

// Parts returns a read-only snapshot of q's clauses.
func (q InsertQuery) Parts() InsertParts {
	c := InsertParts{
		Table:     q.table,
		Columns:   cloneSlice(q.columns),
		Rows:      cloneRows(q.rows),
		Select:    q.selectSrc,
		Returning: cloneSlice(q.returning),
	}
	if q.conflict != nil {
		c.Conflict = &ConflictClause{
			Target:           cloneSlice(q.conflict.target),
			TargetExprs:      cloneSlice(q.conflict.targetExprs),
			TargetConstraint: q.conflict.targetConstraint,
			DoUpdate:         q.conflict.doUpdate,
			Sets:             cloneSlice(q.conflict.sets),
			Where:            q.conflict.where,
		}
	}
	return c
}

// UpdateParts is a read-only snapshot of every clause of an UpdateQuery.
// It is returned by UpdateQuery.Parts.
type UpdateParts struct {
	With      []CTE
	Table     Table
	Sets      []Assignment
	From      []Source
	Joins     []JoinClause
	Where     Expression
	Returning []Selection
}

// Parts returns a read-only snapshot of q's clauses.
func (q UpdateQuery) Parts() UpdateParts {
	return UpdateParts{
		With:      cloneSlice(q.with),
		Table:     q.table,
		Sets:      cloneSlice(q.sets),
		From:      cloneSlice(q.from),
		Joins:     cloneSlice(q.joins),
		Where:     q.where,
		Returning: cloneSlice(q.returning),
	}
}

// DeleteParts is a read-only snapshot of every clause of a DeleteQuery.
// It is returned by DeleteQuery.Parts.
type DeleteParts struct {
	With      []CTE
	Table     Table
	Using     []Source
	Joins     []JoinClause
	Where     Expression
	Returning []Selection
}

// Parts returns a read-only snapshot of q's clauses.
func (q DeleteQuery) Parts() DeleteParts {
	return DeleteParts{
		With:      cloneSlice(q.with),
		Table:     q.table,
		Using:     cloneSlice(q.using),
		Joins:     cloneSlice(q.joins),
		Where:     q.where,
		Returning: cloneSlice(q.returning),
	}
}

// MergeParts is a read-only snapshot of every clause of a MergeQuery.
// It is returned by MergeQuery.Parts.
type MergeParts struct {
	With        []CTE
	Target      Table
	TargetAlias string
	Using       Source
	On          Expression
	When        []MergeWhenClause
	Returning   []Selection
}

// MergeWhenClause is a read-only snapshot of one MERGE WHEN clause. Match is
// "MATCHED", "NOT MATCHED", or "NOT MATCHED BY SOURCE"; Action is "UPDATE",
// "DELETE", "INSERT", "DO NOTHING", or "" when none was set. Sets applies to
// UPDATE; InsertColumns/InsertValues (or InsertDefault) to INSERT.
type MergeWhenClause struct {
	Match         string
	Condition     Expression
	Action        string
	Sets          []Assignment
	InsertColumns []string
	InsertValues  []Expression
	InsertDefault bool
}

// Parts returns a read-only snapshot of q's clauses.
func (q MergeQuery) Parts() MergeParts {
	c := MergeParts{
		With:        cloneSlice(q.with),
		Target:      q.target,
		TargetAlias: q.targetAlias,
		Using:       q.using,
		On:          q.on,
		Returning:   cloneSlice(q.returning),
	}
	if len(q.whens) > 0 {
		c.When = make([]MergeWhenClause, len(q.whens))
		for i, w := range q.whens {
			c.When[i] = mergeWhenView(w)
		}
	}
	return c
}

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
func (c CTE) Name() string { return c.name }

// Body returns the query the CTE wraps.
func (c CTE) Body() Query { return c.query }

// OutputColumns returns the CTE's explicit output column list, or nil when none
// was declared.
func (c CTE) OutputColumns() []string { return cloneSlice(c.columns) }

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
