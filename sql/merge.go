package sql

import (
	"fmt"
	"strings"
)

// MergeQuery is the runtime representation for a MERGE statement (SQL:2003,
// PostgreSQL 15+): it matches rows of a target table against a USING data source
// on a join condition and applies a list of WHEN clauses. Build it with
// MergeInto and add clauses with When.
type MergeQuery struct {
	with        []CTE
	target      Table
	targetAlias string
	using       Source
	on          Expression
	whens       []MergeWhen
	returning   []Selection
}

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

// MergeWhen is one WHEN clause of a MERGE: a match kind, an optional AND
// condition, and the action to perform. Build it with WhenMatched,
// WhenNotMatched, or WhenNotMatchedBySource, then chain And and a Then* action.
type MergeWhen struct {
	match         mergeMatch
	condition     Expression
	action        mergeAction
	sets          []Assignment
	insertColumns []string
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
	w.insertColumns = columns
	w.insertValues = make([]Expression, len(values))
	for i, value := range values {
		w.insertValues[i] = Expr(value)
	}
	w.insertDefault = len(columns) == 0 && len(values) == 0
	return w
}

// MergeInto creates a MERGE query builder for a target table.
func MergeInto(target Table) MergeQuery {
	return MergeQuery{target: target}
}

// As sets an alias for the target table (MERGE INTO target AS alias).
func (q MergeQuery) As(alias string) MergeQuery {
	q.targetAlias = alias
	return q
}

// Using sets the data source matched against the target — a table or subquery,
// optionally aliased with SourceAlias/Alias.
func (q MergeQuery) Using(source Source) MergeQuery {
	q.using = source
	return q
}

// On sets the join condition that pairs source rows with target rows.
func (q MergeQuery) On(condition Expression) MergeQuery {
	q.on = condition
	return q
}

// When appends WHEN clauses, evaluated in order for each row.
func (q MergeQuery) When(whens ...MergeWhen) MergeQuery {
	q.whens = append(q.whens, whens...)
	return q
}

// WithCTE prepends common table expressions (WITH ...).
func (q MergeQuery) WithCTE(ctes ...CTE) MergeQuery {
	q.with = append(q.with, ctes...)
	return q
}

// Returning adds a RETURNING clause (PostgreSQL 17+).
func (q MergeQuery) Returning(selections ...Selection) MergeQuery {
	q.returning = append(q.returning, selections...)
	return q
}

// ToSQL compiles the MERGE query.
func (q MergeQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q MergeQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileMerge(q)
}

// CompileMerge compiles a MERGE statement. The dialect must implement
// MergeDialect (PostgreSQL and the standard target do); others error.
func (c *Compiler) CompileMerge(q MergeQuery) (string, []any, error) {
	if d, ok := c.Dialect.(MergeDialect); !ok || !d.SupportsMerge() {
		return "", nil, fmt.Errorf("sqlkit/sql: dialect %s does not support MERGE", c.Dialect.Name())
	}
	if q.target.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires a target table")
	}
	if q.using == nil {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires a USING source")
	}
	if q.on == nil {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires an ON condition")
	}
	if len(q.whens) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: MERGE requires at least one WHEN clause")
	}

	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.with)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)

	sb.WriteString("MERGE INTO ")
	sb.WriteString(c.Dialect.QuoteIdent(q.target.QualifiedName()))
	if q.targetAlias != "" {
		sb.WriteString(" AS ")
		sb.WriteString(c.Dialect.QuoteIdent(q.targetAlias))
	}

	usingSQL, usingArgs, err := c.CompileSource(q.using)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(" USING ")
	sb.WriteString(usingSQL)
	args = append(args, usingArgs...)

	onSQL, onArgs, err := c.CompileExpression(q.on)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(" ON ")
	sb.WriteString(onSQL)
	args = append(args, onArgs...)

	for _, when := range q.whens {
		if err := c.writeMergeWhen(&sb, &args, when); err != nil {
			return "", nil, err
		}
	}

	if err := c.writeReturning(&sb, &args, q.returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// writeMergeWhen appends one WHEN clause and its action.
func (c *Compiler) writeMergeWhen(sb *strings.Builder, args *[]any, w MergeWhen) error {
	switch w.match {
	case mergeMatched:
		sb.WriteString(" WHEN MATCHED")
	case mergeNotMatchedByTarget:
		sb.WriteString(" WHEN NOT MATCHED")
	case mergeNotMatchedBySource:
		sb.WriteString(" WHEN NOT MATCHED BY SOURCE")
	}
	if w.condition != nil {
		condSQL, condArgs, err := c.CompileExpression(w.condition)
		if err != nil {
			return err
		}
		sb.WriteString(" AND ")
		sb.WriteString(condSQL)
		*args = append(*args, condArgs...)
	}
	sb.WriteString(" THEN ")

	switch w.action {
	case mergeDoNothing:
		sb.WriteString("DO NOTHING")
		return nil
	case mergeDelete:
		if w.match == mergeNotMatchedByTarget {
			return fmt.Errorf("sqlkit/sql: WHEN NOT MATCHED cannot DELETE")
		}
		sb.WriteString("DELETE")
		return nil
	case mergeUpdate:
		if w.match == mergeNotMatchedByTarget {
			return fmt.Errorf("sqlkit/sql: WHEN NOT MATCHED cannot UPDATE")
		}
		if len(w.sets) == 0 {
			return fmt.Errorf("sqlkit/sql: MERGE UPDATE requires at least one assignment")
		}
		return c.writeMergeUpdate(sb, args, w.sets)
	case mergeInsert:
		if w.match != mergeNotMatchedByTarget {
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

func (c *Compiler) writeMergeInsert(sb *strings.Builder, args *[]any, w MergeWhen) error {
	sb.WriteString("INSERT")
	if w.insertDefault {
		sb.WriteString(" DEFAULT VALUES")
		return nil
	}
	if len(w.insertColumns) > 0 {
		sb.WriteString(" (")
		for i, column := range w.insertColumns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c.Dialect.QuoteIdent(column))
		}
		sb.WriteString(")")
	}
	if len(w.insertColumns) > 0 && len(w.insertValues) != len(w.insertColumns) {
		return fmt.Errorf("sqlkit/sql: MERGE INSERT has %d values for %d columns",
			len(w.insertValues), len(w.insertColumns))
	}
	sb.WriteString(" VALUES (")
	for i, value := range w.insertValues {
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
