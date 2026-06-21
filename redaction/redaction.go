// Package redact masks the sensitive string literals embedded in a parsed SQL
// statement. Given an introspected schema (meta.Metadata) and a Policy that names
// the PII columns, a Masker rewrites a statement's AST so that every string
// literal compared or assigned to a sensitive column is replaced with a fixed
// mask value, leaving the statement's shape — and every non-sensitive value —
// intact.
//
// It composes the lower layers: analysis.SourcesOf gives each query node its
// table sources, resolve ties a column reference to its table, and Policy says
// whether that column is sensitive. Resolution failing safe is the default —
// an unqualified column that is ambiguous across in-scope tables is masked when
// any candidate marks it PII — so the masker errs toward over-masking rather than
// leaking. Masking is confined to string literals (ValueExpression holding a
// string): numbers, bind placeholders, and column references are never touched.
//
// CTE bodies are a known gap: they are a separate scope the public AST cannot
// rebuild from outside the sql package, so literals inside a WITH body are left
// unmasked.
package redaction

import (
	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// DefaultMask is the replacement used for a masked string when none is configured.
const DefaultMask = "*****"

// Masker rewrites the sensitive string literals of a statement. Build one with
// New and reuse it across statements; it is read-only and safe for concurrent use.
type Masker struct {
	policy Policy
	mask   string
}

// Option configures a Masker.
type Option func(*Masker)

// WithMask sets the string a masked literal is replaced with (the default is
// DefaultMask).
func WithMask(mask string) Option {
	return func(m *Masker) { m.mask = mask }
}

// New builds a Masker for policy, configured by opts. It needs no schema:
// columns are resolved structurally (by alias and FROM scope) through analyze,
// and the policy decides sensitivity — an unqualified column that does not
// resolve to a single table is masked when any in-scope table marks it sensitive.
// String literals tied to a sensitive column are replaced with the mask value.
func New(policy Policy, opts ...Option) *Masker {
	m := &Masker{policy: policy, mask: DefaultMask}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Apply returns q with the string literals of its sensitive columns masked. It
// does not mutate q: rewritten clauses are rebuilt on copies, and an unchanged
// statement is returned as-is. A nil query, or one with no policy, passes through
// unchanged.
func (m *Masker) Apply(q sql.Query) sql.Query {
	if q == nil || m.policy == nil {
		return q
	}
	return m.rewriteQuery(q)
}

func (m *Masker) rewriteQuery(q sql.Query) sql.Query {
	if q == nil {
		return nil
	}
	scope := analysis.SourcesOf(q)
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return m.rewriteSelect(v, scope)
	case sql.CompoundStmt:
		v.Left = m.rewriteQuery(v.Left)
		v.Right = m.rewriteQuery(v.Right)
		return v
	case sql.InsertStmt:
		return m.rewriteInsert(v, scope)
	case sql.UpdateStmt:
		return m.rewriteUpdate(v, scope)
	case sql.DeleteStmt:
		return m.rewriteDelete(v, scope)
	case sql.MergeStmt:
		return m.rewriteMerge(v, scope)
	default:
		// ValuesQuery and DDL carry no column-bound literals to mask.
		return q
	}
}

func (m *Masker) rewriteSelect(v sql.SelectStmt, scope []analysis.SourceRef) sql.Query {
	v.From = m.rewriteSource(v.From)
	v.Joins = m.rewriteJoins(v.Joins, scope)
	if len(v.Columns) > 0 {
		cols := clone(v.Columns)
		for i := range cols {
			cols[i] = m.rewriteSelection(cols[i], scope)
		}
		v.Columns = cols
	}
	v.Where = m.rewriteExpr(v.Where, scope)
	v.Having = m.rewriteExpr(v.Having, scope)
	v.GroupBy = m.rewriteExprs(v.GroupBy, scope)
	v.DistinctOn = m.rewriteExprs(v.DistinctOn, scope)
	v.OrderBy = m.rewriteOrderTerms(v.OrderBy, scope)
	return v
}

func (m *Masker) rewriteInsert(v sql.InsertStmt, scope []analysis.SourceRef) sql.Query {
	if len(v.Rows) > 0 {
		// The target's columns are named positionally by v.Columns; mask the value
		// in each row whose column is sensitive. Without a column list the positions
		// are the table's own columns, which we cannot name here, so only recurse.
		sensitive := make([]bool, len(v.Columns))
		for i, c := range v.Columns {
			sensitive[i] = m.isSensitive(scope, analysis.NamedColumn(c))
		}
		rows := make([][]sql.Expression, len(v.Rows))
		for r, row := range v.Rows {
			nr := clone(row)
			for i := range nr {
				if i < len(sensitive) && sensitive[i] {
					nr[i] = m.maskValue(nr[i])
				} else {
					nr[i] = m.rewriteExpr(nr[i], scope)
				}
			}
			rows[r] = nr
		}
		v.Rows = rows
	}
	if v.Select != nil {
		v.Select = m.rewriteQuery(v.Select)
	}
	if v.Conflict != nil {
		c := *v.Conflict
		c.Sets = m.rewriteAssignments(c.Sets, scope)
		c.TargetExprs = m.rewriteExprs(c.TargetExprs, scope)
		c.Where = m.rewriteExpr(c.Where, scope)
		v.Conflict = &c
	}
	v.Returning = m.rewriteSelections(v.Returning, scope)
	return v
}

func (m *Masker) rewriteUpdate(v sql.UpdateStmt, scope []analysis.SourceRef) sql.Query {
	// SET targets are the update target's own columns; resolve them against it
	// alone so a same-named column on a joined table cannot make them ambiguous.
	v.Sets = m.rewriteAssignments(v.Sets, analysis.TargetScope(v.Table))
	v.From = m.rewriteSources(v.From)
	v.Joins = m.rewriteJoins(v.Joins, scope)
	v.Where = m.rewriteExpr(v.Where, scope)
	v.Returning = m.rewriteSelections(v.Returning, scope)
	return v
}

func (m *Masker) rewriteDelete(v sql.DeleteStmt, scope []analysis.SourceRef) sql.Query {
	v.Using = m.rewriteSources(v.Using)
	v.Joins = m.rewriteJoins(v.Joins, scope)
	v.Where = m.rewriteExpr(v.Where, scope)
	v.Returning = m.rewriteSelections(v.Returning, scope)
	return v
}

func (m *Masker) rewriteMerge(v sql.MergeStmt, scope []analysis.SourceRef) sql.Query {
	target := analysis.TargetScope(v.Target)
	v.Using = m.rewriteSource(v.Using)
	v.On = m.rewriteExpr(v.On, scope)
	if len(v.When) > 0 {
		whens := clone(v.When)
		for i := range whens {
			whens[i].Condition = m.rewriteExpr(whens[i].Condition, scope)
			whens[i].Sets = m.rewriteAssignments(whens[i].Sets, target)
			whens[i].InsertValues = m.rewriteInsertValues(whens[i].InsertColumns, whens[i].InsertValues, target)
		}
		v.When = whens
	}
	v.Returning = m.rewriteSelections(v.Returning, scope)
	return v
}

// rewriteInsertValues masks the values whose paired InsertColumns are sensitive
// (the MERGE ... WHEN NOT MATCHED THEN INSERT form), recursing through the rest.
func (m *Masker) rewriteInsertValues(columns []sql.Ident, values []sql.Expression, scope []analysis.SourceRef) []sql.Expression {
	if len(values) == 0 {
		return values
	}
	out := clone(values)
	for i := range out {
		if i < len(columns) && m.isSensitive(scope, analysis.NamedColumn(columns[i])) {
			out[i] = m.maskValue(out[i])
		} else {
			out[i] = m.rewriteExpr(out[i], scope)
		}
	}
	return out
}

// rewriteAssignments masks the value of each assignment whose target column is
// sensitive, recursing through targets and values otherwise.
func (m *Masker) rewriteAssignments(as []sql.Assignment, scope []analysis.SourceRef) []sql.Assignment {
	if len(as) == 0 {
		return as
	}
	out := clone(as)
	for i := range out {
		out[i].Target = m.rewriteExpr(out[i].Target, scope)
		out[i].Value = m.rewriteExpr(out[i].Value, scope)
		if !out[i].Column.IsZero() && m.isSensitive(scope, analysis.NamedColumn(out[i].Column)) {
			out[i].Value = m.maskValue(out[i].Value)
		}
	}
	return out
}

func (m *Masker) rewriteJoins(joins []sql.JoinClause, scope []analysis.SourceRef) []sql.JoinClause {
	if len(joins) == 0 {
		return joins
	}
	out := clone(joins)
	for i := range out {
		out[i].Source = m.rewriteSource(out[i].Source)
		out[i].On = m.rewriteExpr(out[i].On, scope)
	}
	return out
}

func (m *Masker) rewriteSources(sources []sql.Source) []sql.Source {
	if len(sources) == 0 {
		return sources
	}
	out := clone(sources)
	for i := range out {
		out[i] = m.rewriteSource(out[i])
	}
	return out
}

// rewriteSource rewrites a subquery source's inner query; a base table or VALUES
// source is returned unchanged.
func (m *Masker) rewriteSource(src sql.Source) sql.Source {
	if src == nil {
		return nil
	}
	e := src.SQLSource()
	if e.Query != nil {
		e.Query = m.rewriteQuery(e.Query)
		return e
	}
	return src
}

func (m *Masker) rewriteSelections(ss []sql.Selection, scope []analysis.SourceRef) []sql.Selection {
	if len(ss) == 0 {
		return ss
	}
	out := clone(ss)
	for i := range out {
		out[i] = m.rewriteSelection(out[i], scope)
	}
	return out
}

// rewriteSelection rewrites a SELECT-list item, looking through an AS alias and
// recursing into any subquery the item embeds. A non-expression item (a "*") is
// unchanged.
func (m *Masker) rewriteSelection(s sql.Selection, scope []analysis.SourceRef) sql.Selection {
	switch x := s.(type) {
	case sql.AliasedSelection:
		x.Selection = m.rewriteSelection(x.Selection, scope)
		return x
	case sql.Expression:
		if rewritten, ok := m.rewriteExpr(x, scope).(sql.Selection); ok {
			return rewritten
		}
		return s
	default:
		return s
	}
}

func (m *Masker) rewriteOrderTerms(terms []sql.OrderTerm, scope []analysis.SourceRef) []sql.OrderTerm {
	if len(terms) == 0 {
		return terms
	}
	out := clone(terms)
	for i := range out {
		out[i].Expr = m.rewriteExpr(out[i].Expr, scope)
	}
	return out
}

// clone returns a shallow copy of s so a rewrite never mutates the input query's
// backing arrays; an empty slice is returned unchanged.
func clone[T any](s []T) []T {
	if len(s) == 0 {
		return s
	}
	return append([]T(nil), s...)
}
