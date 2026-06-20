package sql

import (
	"fmt"
	"strings"
)

// mergeMatch selects which rows a WHEN clause applies to.
type mergeMatch int

const (
	mergeMatched            mergeMatch = iota // WHEN MATCHED
	mergeNotMatchedByTarget                   // WHEN NOT MATCHED [BY TARGET]
	mergeNotMatchedBySource                   // WHEN NOT MATCHED BY SOURCE
)

// mergeAction selects the action a WHEN clause performs.
type mergeAction int

const (
	mergeActionNone mergeAction = iota
	mergeDoNothing
	mergeUpdate
	mergeDelete
	mergeInsert
)

// MergeWhen is the fluent builder for one WHEN clause of a MERGE: a match kind,
// an optional AND condition, and the action to perform. Build it with
// WhenMatched, WhenNotMatched, or WhenNotMatchedBySource, then chain And and a
// Then* action; pass the result to MergeBuilder.When, which records it as the
// MergeWhenClause carried by MergeStmt.
type MergeWhen struct {
	match         mergeMatch
	condition     Expression
	action        mergeAction
	sets          []Assignment
	insertColumns []Ident
	insertValues  []Expression
	insertDefault bool
}

// WhenMatched starts a WHEN MATCHED clause, applied to rows present in both the
// target and the source. Its action is ThenUpdate, ThenDelete, or ThenDoNothing.
func WhenMatched() MergeWhen { return MergeWhen{match: mergeMatched} }

// WhenNotMatched starts a WHEN NOT MATCHED [BY TARGET] clause, applied to source
// rows with no target match. Its action is ThenInsert or ThenDoNothing.
func WhenNotMatched() MergeWhen { return MergeWhen{match: mergeNotMatchedByTarget} }

// WhenNotMatchedBySource starts a WHEN NOT MATCHED BY SOURCE clause, applied to
// target rows with no source match. Its action is ThenUpdate, ThenDelete, or
// ThenDoNothing.
func WhenNotMatchedBySource() MergeWhen { return MergeWhen{match: mergeNotMatchedBySource} }

// And adds an extra condition to the clause (WHEN ... AND condition THEN ...),
// narrowing the rows it applies to.
func (w MergeWhen) And(condition Expression) MergeWhen {
	w.condition = condition
	return w
}

// ThenUpdate makes the clause UPDATE SET the given assignments.
func (w MergeWhen) ThenUpdate(assignments ...Assignment) MergeWhen {
	w.action = mergeUpdate
	w.sets = assignments
	return w
}

// ThenDelete makes the clause DELETE the matched row.
func (w MergeWhen) ThenDelete() MergeWhen {
	w.action = mergeDelete
	return w
}

// ThenDoNothing makes the clause take no action (DO NOTHING) for the rows it
// matches, skipping them.
func (w MergeWhen) ThenDoNothing() MergeWhen {
	w.action = mergeDoNothing
	return w
}

// ThenInsert makes the clause INSERT a row. Non-expression values become bind
// values; pass no columns and no values for INSERT DEFAULT VALUES.
func (w MergeWhen) ThenInsert(columns []string, values ...any) MergeWhen {
	w.action = mergeInsert
	w.insertColumns = idents(columns)
	w.insertValues = make([]Expression, len(values))
	for i, value := range values {
		w.insertValues[i] = Expr(value)
	}
	w.insertDefault = len(columns) == 0 && len(values) == 0
	return w
}

// MergeStmt is the immutable AST for a MERGE statement (SQL:2003, PostgreSQL
// 15+): it matches rows of a target table against a USING data source on an ON
// condition and applies a list of WHEN clauses. Build it with MergeInto; read
// its exported fields directly to introspect.
type MergeStmt struct {
	posField
	With        []CTE
	Target      Table
	TargetAlias Ident
	Using       Source
	On          Expression
	When        []MergeWhenClause
	Returning   []Selection
}

// ToSQL compiles the MERGE statement.
func (q MergeStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q MergeStmt) compile(c *Compiler) (string, []any, error) {
	return c.CompileMerge(q)
}

// MergeBuilder is the fluent constructor for a MergeStmt.
type MergeBuilder struct{ s MergeStmt }

// MergeInto creates a MERGE query builder for a target table.
func MergeInto(target Table) MergeBuilder {
	return MergeBuilder{s: MergeStmt{Target: target}}
}

// Stmt returns the built MERGE statement — the pure AST value.
func (b MergeBuilder) Stmt() MergeStmt { return b.s }

// As sets an alias for the target table (MERGE INTO target AS alias).
func (b MergeBuilder) As(alias string) MergeBuilder {
	b.s.TargetAlias = NewIdent(alias)
	return b
}

// Using sets the data source matched against the target — a table or subquery,
// optionally aliased with SourceAlias/Alias.
func (b MergeBuilder) Using(source Source) MergeBuilder {
	b.s.Using = source
	return b
}

// On sets the join condition that pairs source rows with target rows.
func (b MergeBuilder) On(condition Expression) MergeBuilder {
	b.s.On = condition
	return b
}

// When appends WHEN clauses, evaluated in order for each row.
func (b MergeBuilder) When(whens ...MergeWhen) MergeBuilder {
	next := append([]MergeWhenClause(nil), b.s.When...)
	for _, w := range whens {
		next = append(next, mergeWhenView(w))
	}
	b.s.When = next
	return b
}

// WithCTE prepends common table expressions (WITH ...).
func (b MergeBuilder) WithCTE(ctes ...CTE) MergeBuilder {
	b.s.With = append(append([]CTE(nil), b.s.With...), ctes...)
	return b
}

// Returning adds a RETURNING clause (PostgreSQL 17+).
func (b MergeBuilder) Returning(selections ...Selection) MergeBuilder {
	b.s.Returning = append(append([]Selection(nil), b.s.Returning...), selections...)
	return b
}

// ToSQL compiles the building MERGE statement.
func (b MergeBuilder) ToSQL(dialect Dialect) (string, []any, error) { return b.s.ToSQL(dialect) }

func (b MergeBuilder) compile(c *Compiler) (string, []any, error) { return b.s.compile(c) }

func (b MergeBuilder) buildStmt() Query { return b.s }

// CompileMerge compiles a MERGE statement. The dialect must implement
// MergeDialect (PostgreSQL and the standard target do); others error.
func (c *Compiler) CompileMerge(q MergeStmt) (string, []any, error) {
	if d, ok := c.Dialect.(MergeDialect); !ok || !d.SupportsMerge() {
		return "", nil, fmt.Errorf("sqlkit/sql: dialect %s does not support MERGE", c.Dialect.Name())
	}
	if q.Target.Name.IsZero() {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires a target table")
	}
	if q.Using == nil {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires a USING source")
	}
	if q.On == nil {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires an ON condition")
	}
	if len(q.When) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires at least one WHEN clause")
	}

	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.With)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)

	sb.WriteString("MERGE INTO ")
	sb.WriteString(c.Dialect.QuoteIdent(q.Target.QualifiedName()))
	if !q.TargetAlias.IsZero() {
		sb.WriteString(" AS ")
		sb.WriteString(c.Dialect.QuoteIdent(q.TargetAlias.String()))
	}

	usingSQL, usingArgs, err := c.CompileSource(q.Using)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(" USING ")
	sb.WriteString(usingSQL)
	args = append(args, usingArgs...)

	onSQL, onArgs, err := c.CompileExpression(q.On)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(" ON ")
	sb.WriteString(onSQL)
	args = append(args, onArgs...)

	for _, when := range q.When {
		if err := c.writeMergeWhen(&sb, &args, when); err != nil {
			return "", nil, err
		}
	}

	if err := c.writeReturning(&sb, &args, q.Returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// writeMergeWhen appends one WHEN clause and its action.
func (c *Compiler) writeMergeWhen(sb *strings.Builder, args *[]any, w MergeWhenClause) error {
	switch w.Match {
	case "MATCHED":
		sb.WriteString(" WHEN MATCHED")
	case "NOT MATCHED":
		sb.WriteString(" WHEN NOT MATCHED")
	case "NOT MATCHED BY SOURCE":
		sb.WriteString(" WHEN NOT MATCHED BY SOURCE")
	}
	if w.Condition != nil {
		condSQL, condArgs, err := c.CompileExpression(w.Condition)
		if err != nil {
			return err
		}
		sb.WriteString(" AND ")
		sb.WriteString(condSQL)
		*args = append(*args, condArgs...)
	}
	sb.WriteString(" THEN ")

	switch w.Action {
	case "DO NOTHING":
		sb.WriteString("DO NOTHING")
		return nil
	case "DELETE":
		if w.Match == "NOT MATCHED" {
			return fmt.Errorf("sqlkit/sql: WHEN NOT MATCHED cannot DELETE")
		}
		sb.WriteString("DELETE")
		return nil
	case "UPDATE":
		if w.Match == "NOT MATCHED" {
			return fmt.Errorf("sqlkit/sql: WHEN NOT MATCHED cannot UPDATE")
		}
		if len(w.Sets) == 0 {
			return fmt.Errorf("sqlkit/sql: MERGE UPDATE requires at least one assignment")
		}
		return c.writeMergeUpdate(sb, args, w.Sets)
	case "INSERT":
		if w.Match != "NOT MATCHED" {
			return fmt.Errorf("sqlkit/sql: only WHEN NOT MATCHED can INSERT")
		}
		return c.writeMergeInsert(sb, args, w)
	default:
		return fmt.Errorf("sqlkit/sql: MERGE WHEN clause has no action")
	}
}

func (c *Compiler) writeMergeUpdate(sb *strings.Builder, args *[]any, sets []Assignment) error {
	sb.WriteString("UPDATE SET ")
	for i, assignment := range sets {
		if i > 0 {
			sb.WriteString(", ")
		}
		lhsSQL, lhsArgs, err := c.assignmentLHS(assignment)
		if err != nil {
			return err
		}
		valueSQL, valueArgs, err := c.CompileExpression(assignment.Value)
		if err != nil {
			return err
		}
		sb.WriteString(lhsSQL)
		sb.WriteString(" = ")
		sb.WriteString(valueSQL)
		*args = append(*args, lhsArgs...)
		*args = append(*args, valueArgs...)
	}
	return nil
}

func (c *Compiler) writeMergeInsert(sb *strings.Builder, args *[]any, w MergeWhenClause) error {
	sb.WriteString("INSERT")
	if w.InsertDefault {
		sb.WriteString(" DEFAULT VALUES")
		return nil
	}
	if len(w.InsertColumns) > 0 {
		sb.WriteString(" (")
		for i, column := range w.InsertColumns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c.Dialect.QuoteIdent(column.String()))
		}
		sb.WriteString(")")
	}
	if len(w.InsertColumns) > 0 && len(w.InsertValues) != len(w.InsertColumns) {
		return fmt.Errorf("sqlkit/sql: MERGE INSERT has %d values for %d columns",
			len(w.InsertValues), len(w.InsertColumns))
	}
	sb.WriteString(" VALUES (")
	for i, value := range w.InsertValues {
		if i > 0 {
			sb.WriteString(", ")
		}
		valueSQL, valueArgs, err := c.CompileExpression(value)
		if err != nil {
			return err
		}
		sb.WriteString(valueSQL)
		*args = append(*args, valueArgs...)
	}
	sb.WriteString(")")
	return nil
}
