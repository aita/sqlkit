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
// Nested scopes are followed: the body of a WITH (CTE), a derived (subquery) FROM
// source, and the arms of a set operation are each rewritten in their own scope,
// and a column re-exposed through a CTE, derived table, or UNION is traced back to
// its base table so the literal it is compared to is masked. A correlated
// reference into an enclosing query (a subquery comparing an outer table's column)
// resolves against the enclosing scope too. The remaining gap is depth: a column
// reaching a base table through many chained CTE/subquery hops within a single
// resolution is followed only to a bounded depth, beyond which it goes untraced.
//
// A raw SQL fragment — Raw(...) or RawSQL(...) — is opaque text. By default the
// redactor scans it for the sensitive column names it mentions; when it finds one
// (or the fragment is itself the value compared to a sensitive column) it masks
// every string literal in the fragment — a fail-safe heuristic that cannot tell
// which literal pairs with the column, so it over-masks rather than leak. Configure
// WithRawParser for precise, per-literal masking: the fragment is re-parsed into
// typed AST, masked like any expression, and substituted back.
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
	rawParser      RawParser
}

// Option configures a Redactor.
type Option func(*Redactor)

// RawParser parses a SQL statement string into a query AST. Supplied via
// WithRawParser, it lets the redactor look inside Raw(...) expression fragments and
// RawSQL(...) statements — parsing them, masking the sensitive literals within, and
// substituting the typed result — rather than falling back to the identifier-scan
// heuristic. It is a plain function so the redaction package takes no dependency on
// a parser: a caller passes the frontend it already has (e.g. postgres.ParseOne).
type RawParser func(statement string) (sql.Query, error)

// WithMask sets the string a masked literal is replaced with (the default is
// DefaultMask).
func WithMask(mask string) Option {
	return func(m *Redactor) { m.mask = mask }
}

// WithRawParser configures a parser used to re-parse raw SQL — Raw(...) expression
// fragments and RawSQL(...) statements — so the literals inside them are masked
// precisely. Without it a raw fragment is opaque: the redactor scans it for
// sensitive column names and, when it finds one, masks every string literal in the
// fragment as a fail-safe heuristic (over-masking, since it cannot tell which
// literal pairs with the column). With it, a fragment that the parser can model is
// rewritten like any typed expression, masking only the literals actually tied to a
// sensitive column. A fragment that does not parse, or that the frontend cannot
// model, falls back to the heuristic.
func WithRawParser(p RawParser) Option {
	return func(m *Redactor) { m.rawParser = p }
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
	return m.rewriteQuery(q, nil, nil)
}

// scope is the lexical resolution context while rewriting one query node: the
// node's own table sources (enriched with every CTE visible here, so a reference
// through a CTE traces to its base table), the CTEs nested queries inherit, and a
// link to the enclosing query's scope so a correlated reference can resolve
// against an outer scope.
type scope struct {
	parent    *scope
	sources   []analysis.SourceRef
	childCTEs []sql.CTE // CTEs visible to nested queries: ambient CTEs plus this node's WITH
}

// targetScope wraps an explicit single-source scope (an UPDATE/MERGE target) with
// no enclosing scope, so a SET target resolves against its table alone.
func targetScope(sources []analysis.SourceRef) *scope { return &scope{sources: sources} }

// rewriteQuery rewrites q in a fresh context: parent is the enclosing query's
// context (nil at the top, or for a scope that cannot be correlated), and ambient
// is the CTEs visible from above. The node's sources are enriched with the ambient
// CTEs so a reference through one of them resolves.
func (m *Redactor) rewriteQuery(q sql.Query, parent *scope, ambient []sql.CTE) sql.Query {
	if q == nil {
		return nil
	}
	sources := analysis.SourcesOf(q)
	if len(ambient) > 0 {
		analysis.EnrichSources(sources, ambient)
	}
	sc := &scope{parent: parent, sources: sources, childCTEs: appendCTEs(ambient, withOf(q))}
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return m.rewriteSelect(v, sc)
	case sql.CompoundStmt:
		// A set operation's arms are independent subqueries: each shares the
		// enclosing context and ambient CTEs of the operation itself.
		v.Left = m.rewriteQuery(v.Left, parent, ambient)
		v.Right = m.rewriteQuery(v.Right, parent, ambient)
		return v
	case sql.InsertStmt:
		return m.rewriteInsert(v, sc)
	case sql.UpdateStmt:
		return m.rewriteUpdate(v, sc)
	case sql.DeleteStmt:
		return m.rewriteDelete(v, sc)
	case sql.MergeStmt:
		return m.rewriteMerge(v, sc)
	case sql.RawQuery:
		// An opaque statement. With a parser configured, re-parse it so its literals
		// are masked; if it does not parse or comes back raw, leave it untouched.
		if m.rawParser != nil {
			if parsed, err := m.rawParser(v.SQL()); err == nil && parsed != nil {
				if _, raw := sql.Unwrap(parsed).(sql.RawQuery); !raw {
					return m.rewriteQuery(parsed, parent, ambient)
				}
			}
		}
		return q
	default:
		// ValuesQuery and DDL carry no column-bound literals to mask.
		return q
	}
}

// withOf returns a query node's own WITH clause, or nil for a node that carries none.
func withOf(q sql.Query) []sql.CTE {
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return v.With
	case sql.UpdateStmt:
		return v.With
	case sql.DeleteStmt:
		return v.With
	case sql.MergeStmt:
		return v.With
	default:
		return nil
	}
}

// appendCTEs concatenates two CTE lists into a fresh slice, dropping nils so a
// node with no WITH does not allocate.
func appendCTEs(ambient, own []sql.CTE) []sql.CTE {
	if len(own) == 0 {
		return ambient
	}
	if len(ambient) == 0 {
		return own
	}
	return append(append([]sql.CTE(nil), ambient...), own...)
}

func (m *Redactor) rewriteSelect(v sql.SelectStmt, sc *scope) sql.Query {
	v.With = m.rewriteCTEs(v.With, sc)
	v.From = m.rewriteSource(v.From, sc)
	v.Joins = m.rewriteJoins(v.Joins, sc)
	if len(v.Columns) > 0 {
		cols := clone(v.Columns)
		for i := range cols {
			cols[i] = m.rewriteSelection(cols[i], sc)
		}
		v.Columns = cols
	}
	v.Where = m.rewriteExpr(v.Where, sc)
	v.Having = m.rewriteExpr(v.Having, sc)
	v.GroupBy = m.rewriteExprs(v.GroupBy, sc)
	v.DistinctOn = m.rewriteExprs(v.DistinctOn, sc)
	v.OrderBy = m.rewriteOrderTerms(v.OrderBy, sc)
	v.Windows = m.rewriteNamedWindows(v.Windows, sc)
	return v
}

// rewriteNamedWindows rewrites the PARTITION BY / ORDER BY expressions of each
// WINDOW-clause definition, so a sensitive comparison hidden in a named window
// (WINDOW w AS (PARTITION BY (ssn = 'x'))) is masked like one in an inline OVER.
func (m *Redactor) rewriteNamedWindows(windows []sql.NamedWindow, sc *scope) []sql.NamedWindow {
	if len(windows) == 0 {
		return windows
	}
	out := clone(windows)
	for i := range out {
		out[i].Partitions = m.rewriteExprs(out[i].Partitions, sc)
		out[i].OrderByTerms = m.rewriteOrderTerms(out[i].OrderByTerms, sc)
	}
	return out
}

func (m *Redactor) rewriteInsert(v sql.InsertStmt, sc *scope) sql.Query {
	if len(v.Rows) > 0 {
		// The target's columns are named positionally by v.Columns; mask the value
		// in each row whose column is sensitive. Without a column list the positions
		// are the table's own columns, which we cannot name here, so only recurse.
		// (The compiler rejects a columnless INSERT VALUES outright, so this shape
		// never reaches SQL — unlike the analogous MERGE ... THEN INSERT VALUES,
		// which the compiler accepts; see rewriteInsertValues.)
		sensitive := make([]bool, len(v.Columns))
		for i, c := range v.Columns {
			sensitive[i] = m.isSensitive(sc, analysis.NamedColumn(c))
		}
		rows := make([][]sql.Expression, len(v.Rows))
		for r, row := range v.Rows {
			nr := clone(row)
			for i := range nr {
				if i < len(sensitive) && sensitive[i] {
					nr[i] = m.maskValue(nr[i])
				} else {
					nr[i] = m.rewriteExpr(nr[i], sc)
				}
			}
			rows[r] = nr
		}
		v.Rows = rows
	}
	if v.Select != nil {
		rewritten := m.rewriteQuery(v.Select, sc.parent, sc.childCTEs)
		// INSERT ... SELECT feeds the SELECT's projections into v.Columns
		// positionally, so a literal projected into a sensitive target column must
		// be masked just like a VALUES element. (Without a column list the positions
		// are the table's own columns, which we cannot name here.)
		if len(v.Columns) > 0 {
			sensitive := make([]bool, len(v.Columns))
			for i, c := range v.Columns {
				sensitive[i] = m.isSensitive(sc, analysis.NamedColumn(c))
			}
			rewritten = m.maskSelectProjections(rewritten, sensitive)
		}
		v.Select = rewritten
	}
	if v.Conflict != nil {
		c := *v.Conflict
		c.Sets = m.rewriteAssignments(c.Sets, sc)
		c.TargetExprs = m.rewriteExprs(c.TargetExprs, sc)
		c.Where = m.rewriteExpr(c.Where, sc)
		v.Conflict = &c
	}
	v.Returning = m.rewriteSelections(v.Returning, sc)
	return v
}

func (m *Redactor) rewriteUpdate(v sql.UpdateStmt, sc *scope) sql.Query {
	v.With = m.rewriteCTEs(v.With, sc)
	// SET targets are the update target's own columns; resolve them against it
	// alone so a same-named column on a joined table cannot make them ambiguous.
	target := targetScope(analysis.TargetScope(v.Table))
	v.Sets = m.rewriteAssignments(v.Sets, target)
	v.From = m.rewriteSources(v.From, sc)
	v.Joins = m.rewriteJoins(v.Joins, sc)
	v.Where = m.rewriteExpr(v.Where, sc)
	v.Returning = m.rewriteSelections(v.Returning, sc)
	return v
}

func (m *Redactor) rewriteDelete(v sql.DeleteStmt, sc *scope) sql.Query {
	v.With = m.rewriteCTEs(v.With, sc)
	v.Using = m.rewriteSources(v.Using, sc)
	v.Joins = m.rewriteJoins(v.Joins, sc)
	v.Where = m.rewriteExpr(v.Where, sc)
	v.Returning = m.rewriteSelections(v.Returning, sc)
	return v
}

func (m *Redactor) rewriteMerge(v sql.MergeStmt, sc *scope) sql.Query {
	v.With = m.rewriteCTEs(v.With, sc)
	target := targetScope(analysis.TargetScope(v.Target))
	v.Using = m.rewriteSource(v.Using, sc)
	v.On = m.rewriteExpr(v.On, sc)
	if len(v.When) > 0 {
		whens := clone(v.When)
		for i := range whens {
			whens[i].Condition = m.rewriteExpr(whens[i].Condition, sc)
			whens[i].Sets = m.rewriteAssignments(whens[i].Sets, target)
			whens[i].InsertValues = m.rewriteInsertValues(whens[i].InsertColumns, whens[i].InsertValues, target)
		}
		v.When = whens
	}
	v.Returning = m.rewriteSelections(v.Returning, sc)
	return v
}

// maskSelectProjections masks the i-th projection of an INSERT ... SELECT's
// result when sensitive[i] is set — the projection feeds the i-th (sensitive)
// target column, so a literal there is a value assigned to that column. It
// descends into both arms of a set operation so every UNION arm is masked, and
// leaves a projection whose position cannot be mapped (a wildcard, or an index
// past the column list) untouched. Only the value of a matched projection is
// masked (maskValue), so a projected base column is unchanged.
func (m *Redactor) maskSelectProjections(q sql.Query, sensitive []bool) sql.Query {
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		if len(v.Columns) == 0 {
			return q
		}
		cols := clone(v.Columns)
		for i := range cols {
			if i < len(sensitive) && sensitive[i] {
				cols[i] = m.maskSelection(cols[i])
			}
		}
		v.Columns = cols
		return v
	case sql.CompoundStmt:
		v.Left = m.maskSelectProjections(v.Left, sensitive)
		v.Right = m.maskSelectProjections(v.Right, sensitive)
		return v
	default:
		return q
	}
}

// maskAllProjections masks every projection of a query — used for a subquery that
// stands on the value side of a comparison against a sensitive column
// (name = (SELECT 'x'), name IN (SELECT 'x'), name = ANY(SELECT 'x')), where the
// projected value is what the sensitive column is compared to. It descends into
// both arms of a set operation. maskSelection masks only literal-bearing
// projections, so a projected base column is left unchanged.
func (m *Redactor) maskAllProjections(q sql.Query) sql.Query {
	if q == nil {
		return q
	}
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		if len(v.Columns) == 0 {
			return q
		}
		cols := clone(v.Columns)
		for i := range cols {
			cols[i] = m.maskSelection(cols[i])
		}
		v.Columns = cols
		return v
	case sql.CompoundStmt:
		v.Left = m.maskAllProjections(v.Left)
		v.Right = m.maskAllProjections(v.Right)
		return v
	default:
		return q
	}
}

// maskSelection masks the value expression a SELECT-list item projects, looking
// through an AS alias; a wildcard or non-expression item is left unchanged.
func (m *Redactor) maskSelection(s sql.Selection) sql.Selection {
	switch x := s.(type) {
	case sql.AliasedSelection:
		x.Selection = m.maskSelection(x.Selection)
		return x
	case sql.Expression:
		if masked, ok := m.maskValue(x).(sql.Selection); ok {
			return masked
		}
		return s
	default:
		return s
	}
}

// rewriteInsertValues masks the values whose paired InsertColumns are sensitive
// (the MERGE ... WHEN NOT MATCHED THEN INSERT form), recursing through the rest.
func (m *Redactor) rewriteInsertValues(columns []sql.Ident, values []sql.Expression, sc *scope) []sql.Expression {
	if len(values) == 0 {
		return values
	}
	out := clone(values)
	for i := range out {
		switch {
		case i < len(columns):
			if m.isSensitive(sc, analysis.NamedColumn(columns[i])) {
				out[i] = m.maskValue(out[i])
			} else {
				out[i] = m.rewriteExpr(out[i], sc)
			}
		case m.maskUnresolved:
			// No column list (a valid MERGE ... THEN INSERT VALUES that assigns the
			// table's own column order): this value's target column can't be named,
			// so its sensitivity can't be resolved. Fail safe like any other
			// unresolved reference, rather than assume it's fine and leak it.
			out[i] = m.maskValue(out[i])
		default:
			out[i] = m.rewriteExpr(out[i], sc)
		}
	}
	return out
}

// rewriteAssignments masks the value of each assignment whose target column is
// sensitive, recursing through targets and values otherwise.
func (m *Redactor) rewriteAssignments(as []sql.Assignment, sc *scope) []sql.Assignment {
	if len(as) == 0 {
		return as
	}
	out := clone(as)
	for i := range out {
		out[i].Target = m.rewriteExpr(out[i].Target, sc)
		out[i].Value = m.rewriteExpr(out[i].Value, sc)
		if !out[i].Column.IsZero() && m.isSensitive(sc, analysis.NamedColumn(out[i].Column)) {
			out[i].Value = m.maskValue(out[i].Value)
		}
	}
	return out
}

func (m *Redactor) rewriteJoins(joins []sql.JoinClause, sc *scope) []sql.JoinClause {
	if len(joins) == 0 {
		return joins
	}
	out := clone(joins)
	for i := range out {
		out[i].Source = m.rewriteSource(out[i].Source, sc)
		out[i].On = m.rewriteExpr(out[i].On, sc)
	}
	return out
}

func (m *Redactor) rewriteSources(sources []sql.Source, sc *scope) []sql.Source {
	if len(sources) == 0 {
		return sources
	}
	out := clone(sources)
	for i := range out {
		out[i] = m.rewriteSource(out[i], sc)
	}
	return out
}

// rewriteCTEs rewrites each CTE's body query, returning a fresh slice so the input
// query's backing array is never mutated. A CTE body is a scope of its own — it
// cannot reference the WITH owner's FROM aliases — so it is rewritten under the
// owner's enclosing context (sc.parent), with the sibling and ambient CTEs
// (sc.childCTEs) visible so a CTE that builds on an earlier one resolves through it.
func (m *Redactor) rewriteCTEs(ctes []sql.CTE, sc *scope) []sql.CTE {
	if len(ctes) == 0 {
		return ctes
	}
	out := clone(ctes)
	for i := range out {
		out[i] = out[i].WithBody(m.rewriteQuery(out[i].Body(), sc.parent, sc.childCTEs))
	}
	return out
}

// rewriteSource rewrites a subquery source's inner query; a base table or VALUES
// source is returned unchanged. A plain FROM/JOIN subquery is not correlated to
// the other sources of its own query, so it is rewritten under the enclosing scope
// (sc.parent); a LATERAL subquery may reference the sources to its left, so it is
// rewritten under sc itself. Either way the ambient CTEs (sc.childCTEs) are visible.
func (m *Redactor) rewriteSource(src sql.Source, sc *scope) sql.Source {
	if src == nil {
		return nil
	}
	e := src.SQLSource()
	if e.Query != nil {
		parent := sc.parent
		if e.Lateral {
			parent = sc
		}
		e.Query = m.rewriteQuery(e.Query, parent, sc.childCTEs)
		return e
	}
	return src
}

func (m *Redactor) rewriteSelections(ss []sql.Selection, sc *scope) []sql.Selection {
	if len(ss) == 0 {
		return ss
	}
	out := clone(ss)
	for i := range out {
		out[i] = m.rewriteSelection(out[i], sc)
	}
	return out
}

// rewriteSelection rewrites a SELECT-list item, looking through an AS alias and
// recursing into any subquery the item embeds. A non-expression item (a "*") is
// unchanged.
func (m *Redactor) rewriteSelection(s sql.Selection, sc *scope) sql.Selection {
	switch x := s.(type) {
	case sql.AliasedSelection:
		x.Selection = m.rewriteSelection(x.Selection, sc)
		return x
	case sql.Expression:
		if rewritten, ok := m.rewriteExpr(x, sc).(sql.Selection); ok {
			return rewritten
		}
		return s
	default:
		return s
	}
}

func (m *Redactor) rewriteOrderTerms(terms []sql.OrderTerm, sc *scope) []sql.OrderTerm {
	if len(terms) == 0 {
		return terms
	}
	out := clone(terms)
	for i := range out {
		out[i].Expr = m.rewriteExpr(out[i].Expr, sc)
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
