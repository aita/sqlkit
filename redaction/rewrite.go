package redaction

import (
	"strings"
	"unicode"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// rewriteExpr returns e with the string literals tied to a sensitive column
// masked. It recurses through every operand so a pattern nested in a composite
// predicate is reached, and into embedded subqueries (each with its own scope),
// and applies masking at the comparison and membership nodes where a column meets
// a literal. Unrecognized nodes are returned unchanged.
func (m *Redactor) rewriteExpr(e sql.Expression, sc *scope) sql.Expression {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case sql.BinaryExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		x.Right = m.rewriteExpr(x.Right, sc)
		if m.sensitiveColumn(x.Left, sc) {
			x.Right = m.maskValue(x.Right)
		} else if m.sensitiveColumn(x.Right, sc) {
			x.Left = m.maskValue(x.Left)
		} else {
			// Row constructor comparison: (a, b) = (v1, v2). Mask each value
			// element whose paired column element is sensitive.
			x.Left, x.Right = m.maskRowPair(x.Left, x.Right, sc)
		}
		return x
	case sql.DistinctPredicate:
		x.Left = m.rewriteExpr(x.Left, sc)
		x.Right = m.rewriteExpr(x.Right, sc)
		if m.sensitiveColumn(x.Left, sc) {
			x.Right = m.maskValue(x.Right)
		} else if m.sensitiveColumn(x.Right, sc) {
			x.Left = m.maskValue(x.Left)
		} else {
			// Row-wise IS [NOT] DISTINCT FROM: (a, b) IS DISTINCT FROM (v1, v2).
			x.Left, x.Right = m.maskRowPair(x.Left, x.Right, sc)
		}
		return x
	case sql.InExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		leftSensitive := m.sensitiveColumn(x.Left, sc)
		if len(x.Items) > 0 {
			items := clone(x.Items)
			if lrow, isRow := x.Left.(sql.RowExpression); isRow {
				// Row-wise membership: (a, b) IN ((v1, v2), (v3, v4)). Mask each
				// item's element paired with a sensitive column of the left row.
				for i := range items {
					if irow, ok := items[i].(sql.RowExpression); ok {
						items[i] = m.maskRowValues(lrow, irow, sc)
					} else {
						items[i] = m.rewriteExpr(items[i], sc)
					}
				}
			} else {
				for i := range items {
					if leftSensitive {
						items[i] = m.maskValue(items[i])
					} else {
						items[i] = m.rewriteExpr(items[i], sc)
					}
				}
			}
			x.Items = items
		}
		if x.Subquery != nil {
			x.Subquery = m.rewriteQuery(x.Subquery, sc, sc.childCTEs)
			if leftSensitive {
				// name IN (SELECT 'x'): the subquery projects the values the
				// sensitive column is matched against; mask its projected literals.
				x.Subquery = m.maskAllProjections(x.Subquery)
			}
		}
		return x
	case sql.BetweenExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		x.Low = m.rewriteExpr(x.Low, sc)
		x.High = m.rewriteExpr(x.High, sc)
		if m.sensitiveColumn(x.Operand, sc) {
			x.Low = m.maskValue(x.Low)
			x.High = m.maskValue(x.High)
		}
		return x
	case sql.ILikeExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		x.Pattern = m.rewriteExpr(x.Pattern, sc)
		if m.sensitiveColumn(x.Left, sc) {
			x.Pattern = m.maskValue(x.Pattern)
		}
		return x
	case sql.RegexpExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		x.Pattern = m.rewriteExpr(x.Pattern, sc)
		if m.sensitiveColumn(x.Left, sc) {
			x.Pattern = m.maskValue(x.Pattern)
		}
		return x
	case sql.SimilarToExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		x.Pattern = m.rewriteExpr(x.Pattern, sc)
		if m.sensitiveColumn(x.Left, sc) {
			x.Pattern = m.maskValue(x.Pattern)
		}
		return x
	case sql.MatchExpression:
		// Full-text search: MATCH(cols) AGAINST(query). The query string is matched
		// against the searched columns, so when any of them is sensitive the query
		// literal carries a value compared to a sensitive column and must be masked.
		x.Query = m.rewriteExpr(x.Query, sc)
		for _, c := range x.Columns {
			if m.sensitiveColumn(c, sc) {
				x.Query = m.maskValue(x.Query)
				break
			}
		}
		return x
	case sql.QuantifiedArrayExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		x.Array = m.rewriteExpr(x.Array, sc)
		if m.sensitiveColumn(x.Left, sc) {
			// The array side carries values compared to the sensitive column, in
			// any form: an ARRAY[...] constructor, a cast of an array literal
			// ('{a,b}'::text[]), or a bare string array literal. maskValue masks
			// the string leaves and recurses through the cast/constructor wrapper.
			x.Array = m.maskValue(x.Array)
		}
		return x
	case sql.LogicalExpression:
		x.Operands = m.rewriteExprs(x.Operands, sc)
		return x
	case sql.NotExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		return x
	case sql.UnaryExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		return x
	case sql.NullPredicate:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		return x
	case sql.QuantifiedExpression:
		x.Left = m.rewriteExpr(x.Left, sc)
		if x.Query != nil {
			x.Query = m.rewriteQuery(x.Query, sc, sc.childCTEs)
			if m.sensitiveColumn(x.Left, sc) {
				// name = ANY(SELECT 'x'): the subquery projects the compared values.
				x.Query = m.maskAllProjections(x.Query)
			}
		}
		return x
	case sql.ConcatExpression:
		x.Operands = m.rewriteExprs(x.Operands, sc)
		return x
	case sql.RowExpression:
		x.Elements = m.rewriteExprs(x.Elements, sc)
		return x
	case sql.ArrayExpression:
		x.Elements = m.rewriteExprs(x.Elements, sc)
		return x
	case sql.FuncExpression:
		x.Args = m.rewriteExprs(x.Args, sc)
		return x
	case sql.CastExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		return x
	case sql.CollateExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		return x
	case sql.FieldExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		return x
	case sql.CaseExpression:
		x.Operand = m.rewriteExpr(x.Operand, sc)
		// In a simple CASE (CASE operand WHEN value ...) each WHEN value is
		// equality-compared against the operand, so when the operand reads a
		// sensitive column the values must be masked like any compared literal. In
		// a searched CASE (no operand) the WHEN arms are predicates, rewritten
		// recursively so a comparison inside them masks on its own.
		operandSensitive := x.Operand != nil && m.sensitiveColumn(x.Operand, sc)
		if len(x.Whens) > 0 {
			whens := clone(x.Whens)
			for i := range whens {
				if operandSensitive {
					whens[i].Condition = m.maskValue(whens[i].Condition)
				} else {
					whens[i].Condition = m.rewriteExpr(whens[i].Condition, sc)
				}
				whens[i].Result = m.rewriteExpr(whens[i].Result, sc)
			}
			x.Whens = whens
		}
		x.ElseValue = m.rewriteExpr(x.ElseValue, sc)
		return x
	case sql.ExistsExpression:
		if x.Query != nil {
			x.Query = m.rewriteQuery(x.Query, sc, sc.childCTEs)
		}
		return x
	case sql.ScalarSubqueryExpression:
		if x.Query != nil {
			x.Query = m.rewriteQuery(x.Query, sc, sc.childCTEs)
		}
		return x
	case sql.RawExpression:
		// An opaque SQL fragment. With a parser configured, re-parse it into a typed
		// expression so its inner literals are masked precisely.
		if rewritten, ok := m.rewriteRawExpr(string(x), sc); ok {
			return rewritten
		}
		// No parser (or the fragment did not parse): fall back to a best-effort,
		// fail-safe scan. When the fragment names a sensitive column, a string
		// literal inside it may be a value compared to that column, so mask every
		// literal in the fragment rather than leak the one that is. WithRawParser
		// replaces this with precise, per-literal masking.
		if m.rawReadsSensitive(string(x), sc) {
			return sql.RawExpression(maskRawStringLiterals(string(x), m.mask))
		}
		return e
	default:
		// Leaf nodes (ColumnExpression, ValueExpression, ...) carry no operand to
		// rewrite.
		return e
	}
}

// rewriteRawExpr re-parses a raw SQL expression fragment, masks the sensitive
// literals within it, and returns the typed result, reporting ok=false to leave the
// raw fragment untouched: when no parser is configured, when the fragment does not
// parse, or when the frontend cannot model it (it comes back as raw). The fragment
// is parsed as the projection of a SELECT, the shape a statement parser accepts.
func (m *Redactor) rewriteRawExpr(fragment string, sc *scope) (sql.Expression, bool) {
	if m.rawParser == nil {
		return nil, false
	}
	q, err := m.rawParser("SELECT " + fragment)
	if err != nil || q == nil {
		return nil, false
	}
	e, ok := firstSelectExpr(q)
	if !ok {
		return nil, false
	}
	if _, raw := e.(sql.RawExpression); raw {
		return nil, false // the frontend could not model it; keep the original raw
	}
	return m.rewriteExpr(e, sc), true
}

// firstSelectExpr returns the expression of a parsed "SELECT <expr>" query's first
// projection, looking through an AS alias. It reports ok=false for any other shape.
func firstSelectExpr(q sql.Query) (sql.Expression, bool) {
	sel, ok := sql.Unwrap(q).(sql.SelectStmt)
	if !ok || len(sel.Columns) == 0 {
		return nil, false
	}
	s := sel.Columns[0]
	if al, ok := s.(sql.AliasedSelection); ok {
		s = al.Selection
	}
	e, ok := s.(sql.Expression)
	return e, ok
}

// rewriteExprs rewrites each expression in es, returning a fresh slice so the
// input query's backing array is never mutated.
func (m *Redactor) rewriteExprs(es []sql.Expression, sc *scope) []sql.Expression {
	if len(es) == 0 {
		return es
	}
	out := clone(es)
	for i := range out {
		out[i] = m.rewriteExpr(out[i], sc)
	}
	return out
}

// sensitiveColumn reports whether operand e, in sc, is or reads a policy-sensitive
// column — so a literal compared to it should be masked. A bare column reference is
// resolved directly; a column wrapped in a scalar expression (a function like
// lower(name), a cast, a concatenation) or named inside a raw SQL fragment is
// reached too, so the comparison "lower(name) = 'x'" still masks the literal. A row
// constructor is left for maskRowPair to handle element-wise, so it is not treated
// as a single sensitive operand here.
func (m *Redactor) sensitiveColumn(e sql.Expression, sc *scope) bool {
	if _, isRow := e.(sql.RowExpression); isRow {
		return false
	}
	if col, ok := columnOf(e); ok {
		return m.isSensitive(sc, col)
	}
	return m.exprReadsSensitive(e, sc)
}

// exprReadsSensitive reports whether e reads a sensitive column other than as a
// bare reference — wrapped in a function, cast, concatenation, or raw fragment. It
// stays within the current scope: WalkExpr does not descend into embedded
// subqueries, which carry their own scope and are rewritten on their own.
func (m *Redactor) exprReadsSensitive(e sql.Expression, sc *scope) bool {
	found := false
	sql.WalkExpr(e, func(x sql.Expression) {
		if found {
			return
		}
		switch v := x.(type) {
		case sql.ColumnExpression:
			if m.isSensitive(sc, analysis.ColumnRefOf(v)) {
				found = true
			}
		case sql.ExcludedExpression:
			// excluded.<col> (PostgreSQL) / VALUES(<col>) (MySQL) is the proposed
			// INSERT value for a column of the target table, so it reads that column;
			// resolve its bare name against the scope, which holds the target.
			if m.isSensitive(sc, analysis.NamedColumn(v.Column)) {
				found = true
			}
		case sql.RawExpression:
			if m.rawReadsSensitive(string(v), sc) {
				found = true
			}
		}
	})
	return found
}

// rawReadsSensitive reports whether a raw SQL fragment names a sensitive column.
// The fragment is opaque text, so it is scanned for identifier tokens (and
// qualifier.column pairs), each tested against the policy in sc; string literals in
// the fragment are skipped so a word inside a quoted value is not mistaken for a
// column. It is a best-effort, fail-safe scan — it errs toward masking.
func (m *Redactor) rawReadsSensitive(raw string, sc *scope) bool {
	for _, ref := range rawColumnRefs(raw) {
		if m.isSensitive(sc, ref) {
			return true
		}
	}
	return false
}

// rawColumnRefs extracts the column references a raw SQL fragment may name: each
// maximal run of identifier characters, split on a trailing qualifier dot, with
// single-quoted string literals skipped. It over-collects (a function name is
// returned as a bare reference too), which only costs a policy lookup that fails.
func rawColumnRefs(raw string) []analysis.ColumnRef {
	var refs []analysis.ColumnRef
	var tok strings.Builder
	inStr := false
	flush := func() {
		if tok.Len() == 0 {
			return
		}
		t := tok.String()
		tok.Reset()
		if i := strings.LastIndexByte(t, '.'); i >= 0 {
			if n := t[i+1:]; n != "" {
				refs = append(refs, analysis.ColumnRef{Qualifier: sql.NewIdent(t[:i]), Name: sql.NewIdent(n)})
				return
			}
		}
		refs = append(refs, analysis.ColumnRef{Name: sql.NewIdent(t)})
	}
	for _, r := range raw {
		if inStr {
			if r == '\'' {
				inStr = false
			}
			continue
		}
		switch {
		case r == '\'':
			flush()
			inStr = true
		case r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r):
			tok.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return refs
}

// isSensitive reports whether col is a policy column once resolved against sc and,
// for a correlated reference, its enclosing scopes. At each scope it fans out
// through derived tables, CTEs, and set operations (analysis.ResolveColumnAll):
//
//   - exactly one candidate table — a precise resolution — is matched directly;
//   - several candidates (an ambiguous reference, or one fanned out across UNION
//     arms or joined tables) are masked when maskUnresolved is set and any is a
//     policy column;
//   - no candidate here — the reference may be correlated to an enclosing query,
//     so resolution continues outward.
//
// When nothing resolves at any level and maskUnresolved is set, it fails safe,
// masking when any in-scope table across all levels marks the column sensitive.
// With maskUnresolved cleared, only a single-candidate precise resolution masks.
func (m *Redactor) isSensitive(sc *scope, col analysis.ColumnRef) bool {
	for s := sc; s != nil; s = s.parent {
		cands := analysis.ResolveColumnAllVia(s.sources, col, s.childCTEs)
		switch len(cands) {
		case 0:
			continue // unknown here — try the enclosing (correlated) scope
		case 1:
			return m.candidateSensitive(cands[0])
		default:
			return m.maskUnresolved && m.anyCandidateSensitive(cands)
		}
	}
	if !m.maskUnresolved {
		return false
	}
	// Unknown at every level: fail safe — mask if any in-scope table, in this scope
	// or an enclosing one, marks the column sensitive.
	for s := sc; s != nil; s = s.parent {
		for _, src := range s.sources {
			if !src.Table.IsZero() && m.policy.IsSensitive(src.Schema.String(), src.Table.String(), col.Name.String()) {
				return true
			}
		}
	}
	return false
}

// candidateSensitive reports whether a resolved candidate is a policy column. It
// uses the candidate's own Column — the underlying base column — which a projection
// may have renamed from the reference that was resolved.
func (m *Redactor) candidateSensitive(c analysis.Resolution) bool {
	return m.policy.IsSensitive(c.Schema.String(), c.Table.String(), c.Column.Name.String())
}

// anyCandidateSensitive reports whether any resolved candidate is a policy column.
func (m *Redactor) anyCandidateSensitive(cands []analysis.Resolution) bool {
	for _, c := range cands {
		if m.candidateSensitive(c) {
			return true
		}
	}
	return false
}

// maskValue replaces a string literal with the mask value, leaving every other
// leaf — a number, a bind placeholder, a column — untouched. When the value is a
// string literal wrapped in an expression (a cast, concatenation, function call,
// collation, row constructor, or a binary operator such as the parser-modeled
// "||"), it recurses so the embedded literal does not survive into the rendered
// SQL.
func (m *Redactor) maskValue(e sql.Expression) sql.Expression {
	if v, ok := e.(sql.ValueExpression); ok {
		if _, isString := v.Value.(string); isString {
			return sql.Val(m.mask)
		}
		return e
	}
	switch x := e.(type) {
	case sql.CastExpression:
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.CollateExpression:
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.ConcatExpression:
		x.Operands = m.maskValues(x.Operands)
		return x
	case sql.RawExpression:
		// The whole raw fragment is the value compared to a sensitive column, so
		// every string literal in it carries sensitive data; mask them all. (Without
		// this a raw value like name = Raw("'secret'") would survive untouched.)
		return sql.RawExpression(maskRawStringLiterals(string(x), m.mask))
	case sql.ScalarSubqueryExpression:
		// name = (SELECT 'x'): the subquery's projection is the value compared to
		// the sensitive column, so mask its projected literal(s).
		x.Query = m.maskAllProjections(x.Query)
		return x
	case sql.BinaryExpression:
		// A value built with a binary operator carries its literals in both
		// operands — the parser models string concatenation as "a || b", a
		// BinaryExpression rather than a ConcatExpression. Mask both sides so an
		// embedded literal (name = 'a' || 'b') does not survive; a non-string
		// operand (a column, a number) is left untouched by the recursion.
		x.Left = m.maskValue(x.Left)
		x.Right = m.maskValue(x.Right)
		return x
	case sql.FuncExpression:
		x.Args = m.maskValues(x.Args)
		return x
	case sql.RowExpression:
		x.Elements = m.maskValues(x.Elements)
		return x
	case sql.ArrayExpression:
		x.Elements = m.maskValues(x.Elements)
		return x
	case sql.CaseExpression:
		// The whole CASE is the value being compared to a sensitive column, so
		// every result branch carries sensitive data; mask each THEN and the ELSE.
		// The WHEN conditions are predicates, not the value, so they are left alone.
		if len(x.Whens) > 0 {
			whens := clone(x.Whens)
			for i := range whens {
				whens[i].Result = m.maskValue(whens[i].Result)
			}
			x.Whens = whens
		}
		x.ElseValue = m.maskValue(x.ElseValue)
		return x
	case sql.UnaryExpression:
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.FieldExpression:
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.SubscriptExpression:
		// The indices are positions, not the value being compared; mask the operand.
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.JSONObjectExpression:
		// Both keys and values are operands of the constructed value; mask all.
		x.Pairs = m.maskValues(x.Pairs)
		return x
	case sql.JSONAccessExpression:
		// The path steps are static accessors, not bound values; mask the operand.
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.StringAggExpression:
		x.Arg = m.maskValue(x.Arg)
		return x
	case sql.ExtractExpression:
		x.Operand = m.maskValue(x.Operand)
		return x
	case sql.PositionExpression:
		x.Substring = m.maskValue(x.Substring)
		x.String = m.maskValue(x.String)
		return x
	case sql.WindowExpression:
		// The windowed function carries the value; the frame/partition clauses do not.
		x.Fn = m.maskValue(x.Fn)
		return x
	}
	return e
}

// maskRowPair masks the value-side elements of a row-constructor comparison whose
// paired column element is sensitive — the "(a, b) = (v1, v2)" form, where neither
// whole side is itself a single sensitive column. The value side may be on either
// operand. Returns the operands unchanged when both are not row constructors.
func (m *Redactor) maskRowPair(left, right sql.Expression, sc *scope) (sql.Expression, sql.Expression) {
	lrow, lok := left.(sql.RowExpression)
	rrow, rok := right.(sql.RowExpression)
	if !lok || !rok {
		return left, right
	}
	// Mask in both directions so a sensitive column on either side masks the
	// paired element on the other.
	left = m.maskRowValues(rrow, lrow, sc)
	right = m.maskRowValues(lrow, rrow, sc)
	return left, right
}

// maskRowValues returns vals with each element masked whose positionally paired
// element in cols is a sensitive column — the value side of a row-wise comparison
// such as "(name, id) <op> ('secret', 1)". It is shared by every row-wise form
// (=, IS DISTINCT FROM, and row-constructor IN) so the value paired with a
// sensitive column is masked the same way regardless of the operator.
func (m *Redactor) maskRowValues(cols, vals sql.RowExpression, sc *scope) sql.RowExpression {
	out := clone(vals.Elements)
	for i := range out {
		if i < len(cols.Elements) && m.sensitiveColumn(cols.Elements[i], sc) {
			out[i] = m.maskValue(out[i])
		}
	}
	vals.Elements = out
	return vals
}

// maskValues masks each string literal in es (recursively), returning a fresh
// slice so the input query's backing array is never mutated.
func (m *Redactor) maskValues(es []sql.Expression) []sql.Expression {
	if len(es) == 0 {
		return es
	}
	out := clone(es)
	for i := range out {
		out[i] = m.maskValue(out[i])
	}
	return out
}

// maskRawStringLiterals replaces the content of every single-quoted string
// literal in a raw SQL fragment with the mask, leaving identifiers, operators,
// and numbers intact; a doubled ” inside a literal is an escaped quote, not a
// terminator. It is the best-effort masking applied to a raw fragment when no
// RawParser is configured. Being unable to tell which literal pairs with a
// sensitive column, it is only invoked once the fragment is known to read one (or
// to be the value compared to one) and then masks every literal — fail-safe, the
// same over-masking-rather-than-leaking stance the resolver takes.
func maskRawStringLiterals(raw, mask string) string {
	mask = strings.ReplaceAll(mask, "'", "''") // keep the emitted literal well-formed
	runes := []rune(raw)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		if runes[i] != '\'' {
			b.WriteRune(runes[i])
			continue
		}
		// Start of a string literal: find its terminator, honoring '' escapes.
		j := i + 1
		for j < len(runes) {
			if runes[j] == '\'' {
				if j+1 < len(runes) && runes[j+1] == '\'' {
					j += 2 // an escaped quote inside the literal
					continue
				}
				break // the terminating quote
			}
			j++
		}
		b.WriteByte('\'')
		b.WriteString(mask)
		if j < len(runes) {
			b.WriteByte('\'') // closing quote (absent only for a malformed, unterminated literal)
		}
		i = j
	}
	return b.String()
}

// columnOf returns the column reference a ColumnExpression names, reporting
// ok=false for any other expression. The reference itself comes from analyze, so
// the redactor resolves a column the same way the analysis layer does.
func columnOf(e sql.Expression) (analysis.ColumnRef, bool) {
	c, ok := e.(sql.ColumnExpression)
	if !ok {
		return analysis.ColumnRef{}, false
	}
	return analysis.ColumnRefOf(c), true
}
