// Package redact masks the sensitive string literals embedded in a parsed SQL
// statement. Given an introspected schema (meta.Metadata) and a Policy that names
// the PII columns, a Redactor rewrites a statement's AST so that every string
// literal compared or assigned to a sensitive column is replaced with a fixed
// mask value, leaving the statement's shape — and every non-sensitive value —
// intact.
//
// It composes the lower layers: analysis.SourcesOf gives each query node its
// table sources, resolve ties a column reference to its table, and Policy says
// whether that column is sensitive. A reference that resolves to one table is
// matched precisely — carrying its schema, so a schema-qualified policy
// (Columns("public.users.name")) targets a single table and leaves a same-named
// column on another schema intact. Resolution failing safe is the default for a
// reference that does not resolve: an unqualified column that is ambiguous across
// in-scope tables is masked when any candidate marks it PII, so the redactor errs
// toward over-masking rather than leaking; WithMaskUnresolved(false) turns that
// off for resolve-only masking. Masking is confined to string literals
// (ValueExpression holding a string): numbers, bind placeholders, and column
// references are never touched.
// A string literal wrapped in an expression — a cast, concatenation, function
// call, collation, row constructor, or CASE result — is reached too, so a value
// composed through such a wrapper does not survive into the rendered SQL.
//
// Two scopes remain a known gap, both because the public AST cannot rebuild them
// from outside the sql package: literals inside a WITH (CTE) body, and a
// sensitive column re-exposed through a derived (subquery) FROM source, are left
// unmasked.
package redaction

import (
	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// DefaultMask is the replacement used for a masked string when none is configured.
const DefaultMask = "*****"

// Redactor rewrites the sensitive string literals of a statement. Build one with
// New and reuse it across statements; it is read-only and safe for concurrent use.
type Redactor struct {
	policy         Policy
	mask           string
	maskUnresolved bool
}

// Option configures a Redactor.
type Option func(*Redactor)

// WithMask sets the string a masked literal is replaced with (the default is
// DefaultMask).
func WithMask(mask string) Option {
	return func(m *Redactor) { m.mask = mask }
}

// WithMaskUnresolved controls how a column reference that does not resolve to a
// single in-scope table is treated — one that is unqualified across several
// tables (ambiguous), or whose qualifier matches no source (unknown).
//
// When true (the default) such a reference is masked if any in-scope table marks
// a same-named column sensitive: the policy fails safe, erring toward
// over-masking so a sensitive value never leaks through an imprecise reference.
// Pass false for precise masking — only a reference that resolves to exactly one
// table is matched against the policy, and an unresolved one is left intact. Use
// it when the statements are fully qualified and over-masking is not acceptable.
func WithMaskUnresolved(mask bool) Option {
	return func(m *Redactor) { m.maskUnresolved = mask }
}

// New builds a Redactor for policy, configured by opts. It needs no schema:
// columns are resolved structurally (by alias and FROM scope) through analyze,
// and the policy decides sensitivity. By default an unqualified column that does
// not resolve to a single table is masked when any in-scope table marks it
// sensitive (fail safe); WithMaskUnresolved(false) turns that off for precise,
// resolve-only masking. String literals tied to a sensitive column are replaced
// with the mask value.
func New(policy Policy, opts ...Option) *Redactor {
	m := &Redactor{policy: policy, mask: DefaultMask, maskUnresolved: true}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Apply returns q with the string literals of its sensitive columns masked. It
// does not mutate q: rewritten clauses are rebuilt on copies, and an unchanged
// statement is returned as-is. A nil query, or one with no policy, passes through
// unchanged.
func (m *Redactor) Apply(q sql.Query) sql.Query {
	if q == nil || m.policy == nil {
		return q
	}
	return m.rewriteQuery(q)
}

func (m *Redactor) rewriteQuery(q sql.Query) sql.Query {
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

func (m *Redactor) rewriteSelect(v sql.SelectStmt, scope []analysis.SourceRef) sql.Query {
	v.With = m.rewriteCTEs(v.With)
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

func (m *Redactor) rewriteInsert(v sql.InsertStmt, scope []analysis.SourceRef) sql.Query {
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

func (m *Redactor) rewriteUpdate(v sql.UpdateStmt, scope []analysis.SourceRef) sql.Query {
	v.With = m.rewriteCTEs(v.With)
	// SET targets are the update target's own columns; resolve them against it
	// alone so a same-named column on a joined table cannot make them ambiguous.
	v.Sets = m.rewriteAssignments(v.Sets, analysis.TargetScope(v.Table))
	v.From = m.rewriteSources(v.From)
	v.Joins = m.rewriteJoins(v.Joins, scope)
	v.Where = m.rewriteExpr(v.Where, scope)
	v.Returning = m.rewriteSelections(v.Returning, scope)
	return v
}

func (m *Redactor) rewriteDelete(v sql.DeleteStmt, scope []analysis.SourceRef) sql.Query {
	v.With = m.rewriteCTEs(v.With)
	v.Using = m.rewriteSources(v.Using)
	v.Joins = m.rewriteJoins(v.Joins, scope)
	v.Where = m.rewriteExpr(v.Where, scope)
	v.Returning = m.rewriteSelections(v.Returning, scope)
	return v
}

func (m *Redactor) rewriteMerge(v sql.MergeStmt, scope []analysis.SourceRef) sql.Query {
	v.With = m.rewriteCTEs(v.With)
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
func (m *Redactor) rewriteInsertValues(columns []sql.Ident, values []sql.Expression, scope []analysis.SourceRef) []sql.Expression {
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
func (m *Redactor) rewriteAssignments(as []sql.Assignment, scope []analysis.SourceRef) []sql.Assignment {
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

func (m *Redactor) rewriteJoins(joins []sql.JoinClause, scope []analysis.SourceRef) []sql.JoinClause {
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

func (m *Redactor) rewriteSources(sources []sql.Source) []sql.Source {
	if len(sources) == 0 {
		return sources
	}
	out := clone(sources)
	for i := range out {
		out[i] = m.rewriteSource(out[i])
	}
	return out
}

// rewriteCTEs rewrites each CTE's body query, returning a fresh slice so the
// input query's backing array is never mutated. An empty slice is returned as-is.
func (m *Redactor) rewriteCTEs(ctes []sql.CTE) []sql.CTE {
	if len(ctes) == 0 {
		return ctes
	}
	out := clone(ctes)
	for i := range out {
		out[i] = out[i].WithBody(m.rewriteQuery(out[i].Body()))
	}
	return out
}

// rewriteSource rewrites a subquery source's inner query; a base table or VALUES
// source is returned unchanged.
func (m *Redactor) rewriteSource(src sql.Source) sql.Source {
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

func (m *Redactor) rewriteSelections(ss []sql.Selection, scope []analysis.SourceRef) []sql.Selection {
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
func (m *Redactor) rewriteSelection(s sql.Selection, scope []analysis.SourceRef) sql.Selection {
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

func (m *Redactor) rewriteOrderTerms(terms []sql.OrderTerm, scope []analysis.SourceRef) []sql.OrderTerm {
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
