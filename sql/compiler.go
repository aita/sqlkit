package sql

import (
	"fmt"
	"strconv"
	"strings"
)

// Compiler turns SQL expressions and queries into SQL text plus arguments.
// A single compiler instance is shared across a statement and its subqueries
// so that bind placeholder numbering stays consistent.
type Compiler struct {
	Dialect Dialect
	ordinal int
	// inlineValues renders a ValueExpression as an inline literal instead of a
	// bind placeholder; CompileInline sets it. The result carries no args.
	inlineValues bool
}

// NewCompiler creates a compiler for the supplied dialect.
func NewCompiler(dialect Dialect) *Compiler {
	if dialect == nil {
		dialect = PostgreSQLDialect{}
	}
	return &Compiler{Dialect: dialect}
}

// CompileStatic compiles an expression that binds no values into SQL text, for
// static contexts such as DDL DEFAULT and CHECK clauses where bind placeholders
// are not allowed. It errors if expr is nil or produces bind values; wrap
// literals in Raw to inline them.
func CompileStatic(dialect Dialect, expr Expression) (string, error) {
	if expr == nil {
		return "", fmt.Errorf("sqlkit/sql: nil expression")
	}
	text, args, err := NewCompiler(dialect).CompileExpression(expr)
	if err != nil {
		return "", err
	}
	if len(args) > 0 {
		return "", fmt.Errorf("sqlkit/sql: static expression must not bind values; use Raw to inline literals")
	}
	return text, nil
}

// CompileExpression compiles an expression fragment.
func (c *Compiler) CompileExpression(expr Expression) (string, []any, error) {
	switch e := expr.(type) {
	case RawExpression:
		return string(e), nil, nil
	case ColumnExpression:
		return c.Dialect.QuoteIdent(e.Reference()), nil, nil
	case ValueExpression:
		if c.inlineValues {
			return inlineLiteral(e), nil, nil
		}
		return c.nextPlaceholder(), []any{e.Value}, nil
	case GroupingExpression:
		if !supportsGroupingSets(c.Dialect) {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support %s grouping", c.Dialect.Name(), e.Kind)
		}
		inner, args, err := c.compileExprList(e.Elements)
		if err != nil {
			return "", nil, err
		}
		return e.Kind + " (" + inner + ")", args, nil
	case GroupingSetsExpression:
		if !supportsGroupingSets(c.Dialect) {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support GROUPING SETS", c.Dialect.Name())
		}
		if len(e.Sets) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: GROUPING SETS requires at least one set")
		}
		var sb strings.Builder
		var args []any
		sb.WriteString("GROUPING SETS (")
		for i, set := range e.Sets {
			if i > 0 {
				sb.WriteString(", ")
			}
			inner, setArgs, err := c.compileExprList(set)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString("(" + inner + ")")
			args = append(args, setArgs...)
		}
		sb.WriteString(")")
		return sb.String(), args, nil
	case SubscriptExpression:
		return c.compileSubscript(e)
	case FieldExpression:
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		return "(" + operandSQL + ")." + c.Dialect.QuoteName(e.Field.String()), args, nil
	case CollateExpression:
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		return operandSQL + " COLLATE " + c.Dialect.QuoteIdent(e.Collation.String()), args, nil
	case BinaryExpression:
		leftSQL, leftArgs, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		rightSQL, rightArgs, err := c.CompileExpression(e.Right)
		if err != nil {
			return "", nil, err
		}
		args := append(leftArgs, rightArgs...)
		return "(" + leftSQL + " " + e.Operator + " " + rightSQL + ")", args, nil
	case UnaryExpression:
		if e.Operator == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: unary expression requires an operator")
		}
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		return "(" + e.Operator + operandSQL + ")", args, nil
	case RowExpression:
		if len(e.Elements) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: row expression requires at least one element")
		}
		parts := make([]string, len(e.Elements))
		var args []any
		for i, element := range e.Elements {
			elementSQL, elementArgs, err := c.CompileExpression(element)
			if err != nil {
				return "", nil, err
			}
			parts[i] = elementSQL
			args = append(args, elementArgs...)
		}
		// A multi-element row reads as a row from its commas: (a, b). A single
		// element needs the ROW keyword — (x) alone is a parenthesized scalar, not a
		// one-field row, and would lose its row-ness on a round-trip. MySQL, however,
		// has no one-field row (its ROW constructor requires at least two elements
		// and rejects ROW(x)), so a single-element row has no valid MySQL spelling.
		if len(parts) == 1 {
			if c.Dialect.Name() == "mysql" {
				return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support a single-element row", c.Dialect.Name())
			}
			return "ROW(" + parts[0] + ")", args, nil
		}
		return "(" + strings.Join(parts, ", ") + ")", args, nil
	case DistinctPredicate:
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		rightSQL, rightArgs, err := c.CompileExpression(e.Right)
		if err != nil {
			return "", nil, err
		}
		args = append(args, rightArgs...)
		if nse, ok := c.Dialect.(NullSafeEqualityDialect); ok {
			return nse.IsDistinctFrom(leftSQL, rightSQL, e.Negate), args, nil
		}
		// SQL standard IS [NOT] DISTINCT FROM (PostgreSQL, the standard
		// target); MySQL overrides via NullSafeEqualityDialect with <=>.
		operator := "IS DISTINCT FROM"
		if e.Negate {
			operator = "IS NOT DISTINCT FROM"
		}
		return "(" + leftSQL + " " + operator + " " + rightSQL + ")", args, nil
	case LogicalExpression:
		if len(e.Operands) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: %s requires at least one operand", e.Operator)
		}
		parts := make([]string, len(e.Operands))
		var args []any
		for i, operand := range e.Operands {
			operandSQL, operandArgs, err := c.CompileExpression(operand)
			if err != nil {
				return "", nil, err
			}
			parts[i] = operandSQL
			args = append(args, operandArgs...)
		}
		return "(" + strings.Join(parts, " "+e.Operator+" ") + ")", args, nil
	case NotExpression:
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		return "(NOT " + operandSQL + ")", args, nil
	case InExpression:
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		operator := "IN"
		if e.Negate {
			operator = "NOT IN"
		}
		if e.Subquery != nil {
			subSQL, subArgs, err := c.compileSubquery(e.Subquery)
			if err != nil {
				return "", nil, err
			}
			args = append(args, subArgs...)
			return "(" + leftSQL + " " + operator + " (" + subSQL + "))", args, nil
		}
		if len(e.Items) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: IN requires at least one item")
		}
		items := make([]string, len(e.Items))
		for i, item := range e.Items {
			itemSQL, itemArgs, err := c.CompileExpression(item)
			if err != nil {
				return "", nil, err
			}
			items[i] = itemSQL
			args = append(args, itemArgs...)
		}
		return "(" + leftSQL + " " + operator + " (" + strings.Join(items, ", ") + "))", args, nil
	case BetweenExpression:
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		lowSQL, lowArgs, err := c.CompileExpression(e.Low)
		if err != nil {
			return "", nil, err
		}
		highSQL, highArgs, err := c.CompileExpression(e.High)
		if err != nil {
			return "", nil, err
		}
		args = append(args, lowArgs...)
		args = append(args, highArgs...)
		operator := "BETWEEN"
		if e.Negate {
			operator = "NOT BETWEEN"
		}
		return "(" + operandSQL + " " + operator + " " + lowSQL + " AND " + highSQL + ")", args, nil
	case JSONObjectExpression:
		if len(e.Pairs)%2 != 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: JSON_OBJECT requires an even number of key/value arguments")
		}
		name := "JSON_OBJECT"
		if e.Agg {
			name = "JSON_OBJECTAGG"
		}
		compiled := make([]string, len(e.Pairs))
		var args []any
		for i, pair := range e.Pairs {
			pairSQL, pairArgs, err := c.CompileExpression(pair)
			if err != nil {
				return "", nil, err
			}
			compiled[i] = pairSQL
			args = append(args, pairArgs...)
		}
		pairs := make([][2]string, 0, len(compiled)/2)
		for i := 0; i+1 < len(compiled); i += 2 {
			pairs = append(pairs, [2]string{compiled[i], compiled[i+1]})
		}
		if r, ok := c.Dialect.(JSONObjectRenderer); ok {
			return r.JSONObject(name, pairs), args, nil
		}
		// SQL/JSON standard "key VALUE value" form (PostgreSQL); MySQL overrides
		// via JSONObjectRenderer with a flat comma list.
		parts := make([]string, len(pairs))
		for i, kv := range pairs {
			parts[i] = kv[0] + " VALUE " + kv[1]
		}
		return name + "(" + strings.Join(parts, ", ") + ")", args, nil
	case JSONAccessExpression:
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		accessor, ok := c.Dialect.(JSONAccessor)
		if !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support JSON access", c.Dialect.Name())
		}
		out, err := accessor.JSONAccess(operandSQL, e.Path, e.AsText)
		if err != nil {
			return "", nil, err
		}
		return out, args, nil
	case ExistsExpression:
		if e.Query == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: EXISTS requires a subquery")
		}
		subSQL, args, err := c.compileSubquery(e.Query)
		if err != nil {
			return "", nil, err
		}
		if e.Negate {
			return "(NOT EXISTS (" + subSQL + "))", args, nil
		}
		return "EXISTS (" + subSQL + ")", args, nil
	case ScalarSubqueryExpression:
		if e.Query == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: scalar subquery requires a query")
		}
		subSQL, args, err := c.compileSubquery(e.Query)
		if err != nil {
			return "", nil, err
		}
		return "(" + subSQL + ")", args, nil
	case QuantifiedExpression:
		if e.Query == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: %s comparison requires a subquery", e.Quantifier)
		}
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		subSQL, subArgs, err := c.compileSubquery(e.Query)
		if err != nil {
			return "", nil, err
		}
		args = append(args, subArgs...)
		return "(" + leftSQL + " " + e.Operator + " " + e.Quantifier + " (" + subSQL + "))", args, nil
	case ArrayExpression:
		arr, ok := c.Dialect.(ArrayDialect)
		if !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support arrays", c.Dialect.Name())
		}
		elements := make([]string, len(e.Elements))
		var args []any
		for i, element := range e.Elements {
			elementSQL, elementArgs, err := c.CompileExpression(element)
			if err != nil {
				return "", nil, err
			}
			elements[i] = elementSQL
			args = append(args, elementArgs...)
		}
		return arr.ArrayConstructor(elements), args, nil
	case QuantifiedArrayExpression:
		if e.Array == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: %s(array) requires an array operand", e.Quantifier)
		}
		if _, ok := c.Dialect.(ArrayDialect); !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support arrays", c.Dialect.Name())
		}
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		arraySQL, arrayArgs, err := c.CompileExpression(e.Array)
		if err != nil {
			return "", nil, err
		}
		args = append(args, arrayArgs...)
		return "(" + leftSQL + " " + e.Operator + " " + e.Quantifier + "(" + arraySQL + "))", args, nil
	case ExcludedExpression:
		if e.Column.IsZero() {
			return "", nil, fmt.Errorf("sqlkit/sql: Excluded requires a column name")
		}
		if r, ok := c.Dialect.(ExcludedRenderer); ok {
			return r.Excluded(e.Column.String()), nil, nil
		}
		return "excluded." + c.Dialect.QuoteName(e.Column.String()), nil, nil
	case ConcatExpression:
		if len(e.Operands) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: CONCAT requires at least one operand")
		}
		parts := make([]string, len(e.Operands))
		var args []any
		for i, operand := range e.Operands {
			operandSQL, operandArgs, err := c.CompileExpression(operand)
			if err != nil {
				return "", nil, err
			}
			parts[i] = operandSQL
			args = append(args, operandArgs...)
		}
		if con, ok := c.Dialect.(Concatenator); ok {
			return con.Concat(parts), args, nil
		}
		// Standard || operator (PostgreSQL); MySQL overrides via Concatenator.
		return "(" + strings.Join(parts, " || ") + ")", args, nil
	case ILikeExpression:
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		patternSQL, patternArgs, err := c.CompileExpression(e.Pattern)
		if err != nil {
			return "", nil, err
		}
		args = append(args, patternArgs...)
		if il, ok := c.Dialect.(CaseInsensitiveLiker); ok {
			return il.ILike(leftSQL, patternSQL), args, nil
		}
		// Portable fallback for engines without an ILIKE operator (MySQL).
		return "(LOWER(" + leftSQL + ") LIKE LOWER(" + patternSQL + "))", args, nil
	case RegexpExpression:
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		patternSQL, patternArgs, err := c.CompileExpression(e.Pattern)
		if err != nil {
			return "", nil, err
		}
		args = append(args, patternArgs...)
		matcher, ok := c.Dialect.(RegexpMatcher)
		if !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support regular-expression matching", c.Dialect.Name())
		}
		out, err := matcher.Regexp(leftSQL, patternSQL, e.Negate, e.CaseInsensitive)
		if err != nil {
			return "", nil, err
		}
		return out, args, nil
	case SimilarToExpression:
		leftSQL, args, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		patternSQL, patternArgs, err := c.CompileExpression(e.Pattern)
		if err != nil {
			return "", nil, err
		}
		args = append(args, patternArgs...)
		matcher, ok := c.Dialect.(SimilarToMatcher)
		if !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support SIMILAR TO", c.Dialect.Name())
		}
		return matcher.SimilarTo(leftSQL, patternSQL, e.Negate), args, nil
	case MatchExpression:
		if len(e.Columns) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: full-text search requires at least one column")
		}
		columns := make([]string, len(e.Columns))
		var args []any
		for i, col := range e.Columns {
			colSQL, colArgs, err := c.CompileExpression(col)
			if err != nil {
				return "", nil, err
			}
			columns[i] = colSQL
			args = append(args, colArgs...)
		}
		querySQL, queryArgs, err := c.CompileExpression(e.Query)
		if err != nil {
			return "", nil, err
		}
		args = append(args, queryArgs...)
		searcher, ok := c.Dialect.(FullTextSearcher)
		if !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support full-text search", c.Dialect.Name())
		}
		out, err := searcher.Match(columns, querySQL, e.Mode == FTSBoolean, e.Config)
		if err != nil {
			return "", nil, err
		}
		return out, args, nil
	case NullPredicate:
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		operator := "IS NULL"
		if e.Negate {
			operator = "IS NOT NULL"
		}
		return "(" + operandSQL + " " + operator + ")", args, nil
	case FuncExpression:
		compiled := make([]string, len(e.Args))
		var args []any
		for i, arg := range e.Args {
			argSQL, argArgs, err := c.CompileExpression(arg)
			if err != nil {
				return "", nil, err
			}
			compiled[i] = argSQL
			args = append(args, argArgs...)
		}
		// A FuncRenderer dialect may remap a canonical function name/form to its
		// native spelling; an unknown name (or a dialect without the capability)
		// falls through to the default name(args...) rendering unchanged. DISTINCT
		// aggregates skip the remap: the remapped names are scalar functions, not
		// aggregates, so DISTINCT never applies to them.
		if !e.Distinct {
			if fr, ok := c.Dialect.(FuncRenderer); ok {
				if out, handled := fr.RenderFunc(e.Name, compiled); handled {
					return out, args, nil
				}
			}
		}
		distinct := ""
		if e.Distinct {
			distinct = "DISTINCT "
		}
		return e.Name + "(" + distinct + strings.Join(compiled, ", ") + ")", args, nil
	case ExtractExpression:
		if e.Field == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: EXTRACT requires a field")
		}
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		return "EXTRACT(" + e.Field + " FROM " + operandSQL + ")", args, nil
	case PositionExpression:
		substringSQL, args, err := c.CompileExpression(e.Substring)
		if err != nil {
			return "", nil, err
		}
		stringSQL, stringArgs, err := c.CompileExpression(e.String)
		if err != nil {
			return "", nil, err
		}
		args = append(args, stringArgs...)
		return "POSITION(" + substringSQL + " IN " + stringSQL + ")", args, nil
	case StringAggExpression:
		if e.Arg == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: STRING_AGG requires an expression")
		}
		argSQL, args, err := c.CompileExpression(e.Arg)
		if err != nil {
			return "", nil, err
		}
		delimiter := literal(e.Delimiter)
		if sa, ok := c.Dialect.(StringAggregator); ok {
			return sa.StringAgg(argSQL, delimiter), args, nil
		}
		// SQL standard STRING_AGG(expr, 'delim') (PostgreSQL); MySQL overrides via
		// StringAggregator with GROUP_CONCAT(expr SEPARATOR 'delim').
		return "STRING_AGG(" + argSQL + ", " + delimiter + ")", args, nil
	case CastExpression:
		if e.Type == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: CAST requires a target type")
		}
		operandSQL, args, err := c.CompileExpression(e.Operand)
		if err != nil {
			return "", nil, err
		}
		return "CAST(" + operandSQL + " AS " + e.Type + ")", args, nil
	case CountExpression:
		if e.All || e.Target == nil {
			return "COUNT(*)", nil, nil
		}
		target, args, err := c.CompileSelection(e.Target)
		if err != nil {
			return "", nil, err
		}
		if e.Distinct {
			return "COUNT(DISTINCT " + target + ")", args, nil
		}
		return "COUNT(" + target + ")", args, nil
	case CaseExpression:
		if len(e.Whens) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: CASE requires at least one WHEN arm")
		}
		var sb strings.Builder
		var args []any
		sb.WriteString("CASE")
		if e.Operand != nil {
			operandSQL, operandArgs, err := c.CompileExpression(e.Operand)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(" ")
			sb.WriteString(operandSQL)
			args = append(args, operandArgs...)
		}
		for _, when := range e.Whens {
			conditionSQL, conditionArgs, err := c.CompileExpression(when.Condition)
			if err != nil {
				return "", nil, err
			}
			resultSQL, resultArgs, err := c.CompileExpression(when.Result)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(" WHEN ")
			sb.WriteString(conditionSQL)
			sb.WriteString(" THEN ")
			sb.WriteString(resultSQL)
			args = append(args, conditionArgs...)
			args = append(args, resultArgs...)
		}
		if e.ElseValue != nil {
			elseSQL, elseArgs, err := c.CompileExpression(e.ElseValue)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(" ELSE ")
			sb.WriteString(elseSQL)
			args = append(args, elseArgs...)
		}
		sb.WriteString(" END")
		return sb.String(), args, nil
	case WindowExpression:
		if e.Fn == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: window expression requires a function")
		}
		fnSQL, args, err := c.CompileExpression(e.Fn)
		if err != nil {
			return "", nil, err
		}
		body, bodyArgs, err := c.compileWindowParts(e.Partitions, e.OrderByTerms, e.FrameClause)
		if err != nil {
			return "", nil, err
		}
		args = append(args, bodyArgs...)
		// A bare reference to a named window: "fn OVER w".
		if !e.Name.IsZero() && body == "" {
			return fnSQL + " OVER " + c.Dialect.QuoteName(e.Name.String()), args, nil
		}
		inner := body
		if !e.Name.IsZero() {
			// Extending a named window with further clauses: "fn OVER (w ...)".
			inner = c.Dialect.QuoteName(e.Name.String())
			if body != "" {
				inner += " " + body
			}
		}
		return fnSQL + " OVER (" + inner + ")", args, nil
	case interface{ Reference() string }:
		return c.Dialect.QuoteIdent(e.Reference()), nil, nil
	case interface{ UnwrapExpression() Expression }:
		return c.CompileExpression(e.UnwrapExpression())
	default:
		return "", nil, fmt.Errorf("sqlkit/sql: unsupported expression %T", expr)
	}
}

// CompileSelection compiles a SELECT-list item.
func (c *Compiler) CompileSelection(selection Selection) (string, []any, error) {
	switch s := selection.(type) {
	case RawExpression:
		return string(s), nil, nil
	case ColumnExpression:
		return c.Dialect.QuoteIdent(s.Reference()), nil, nil
	case StarSelection:
		if s.Qualifier.IsZero() {
			return "*", nil, nil
		}
		return c.Dialect.QuoteIdent(s.Qualifier.String()) + ".*", nil, nil
	case AliasedSelection:
		inner, args, err := c.CompileSelection(s.Selection)
		if err != nil {
			return "", nil, err
		}
		return inner + " AS " + c.Dialect.QuoteName(s.Alias.String()), args, nil
	case interface{ Reference() string }:
		return c.Dialect.QuoteIdent(s.Reference()), nil, nil
	case interface{ UnwrapSelection() Selection }:
		return c.CompileSelection(s.UnwrapSelection())
	case Expression:
		return c.CompileExpression(s)
	default:
		return "", nil, fmt.Errorf("sqlkit/sql: unsupported selection %T", selection)
	}
}

// CompileSource compiles a FROM/JOIN source.
func (c *Compiler) CompileSource(source Source) (string, []any, error) {
	if source == nil {
		return "", nil, fmt.Errorf("sqlkit/sql: missing source")
	}
	s := source.SQLSource()
	lateral := ""
	if s.Lateral {
		lateral = "LATERAL "
	}
	if s.Values != nil {
		out, args, err := c.compileValuesSource(s)
		if err != nil {
			return "", nil, err
		}
		return lateral + out, args, nil
	}
	if s.Query != nil {
		sql, args, err := c.compileSubquery(s.Query)
		if err != nil {
			return "", nil, err
		}
		compiled := lateral + "(" + sql + ")"
		if !s.Alias.IsZero() {
			compiled += " AS " + c.Dialect.QuoteName(s.Alias.String())
		}
		return compiled, args, nil
	}
	if s.Name.IsZero() {
		return "", nil, fmt.Errorf("sqlkit/sql: missing source name")
	}
	compiled := lateral + c.Dialect.QuoteIdent(s.Name.String())
	if !s.Alias.IsZero() {
		compiled += " AS " + c.Dialect.QuoteName(s.Alias.String())
	}
	return compiled, nil, nil
}

// compileValuesSource compiles an inline VALUES row constructor used as a derived
// table. The standard form is (VALUES (...),(...)) AS alias("c1","c2"); a dialect
// that cannot alias VALUES columns (DerivedValuesUnionDialect, MySQL) gets an
// equivalent (SELECT v AS c1, … UNION ALL SELECT …) AS alias instead.
func (c *Compiler) compileValuesSource(s SourceExpression) (string, []any, error) {
	v := s.Values
	if len(v.Rows) == 0 || len(v.Rows[0]) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: VALUES source requires at least one non-empty row")
	}
	width := len(v.Rows[0])
	if len(v.Columns) > 0 && len(v.Columns) != width {
		return "", nil, fmt.Errorf("sqlkit/sql: VALUES source has %d column names for %d-wide rows", len(v.Columns), width)
	}
	union := false
	if d, ok := c.Dialect.(DerivedValuesUnionDialect); ok && d.UsesValuesUnion() {
		union = true
	}
	if union && len(v.Columns) != width {
		return "", nil, fmt.Errorf("sqlkit/sql: VALUES source needs a column name per value for this dialect")
	}

	var sb strings.Builder
	var args []any
	sb.WriteString("(")
	for ri, row := range v.Rows {
		if len(row) != width {
			return "", nil, fmt.Errorf("sqlkit/sql: VALUES source row %d has %d values, want %d", ri, len(row), width)
		}
		if union {
			if ri > 0 {
				sb.WriteString(" UNION ALL ")
			}
			sb.WriteString("SELECT ")
		} else {
			if ri == 0 {
				sb.WriteString("VALUES ")
			} else {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
		}
		for ci, value := range row {
			if ci > 0 {
				sb.WriteString(", ")
			}
			valueSQL, valueArgs, err := c.CompileExpression(value)
			if err != nil {
				return "", nil, err
			}
			// PostgreSQL infers an unannotated VALUES parameter as text, which
			// breaks a join/assignment against a non-text column. Type the first
			// row's values so the server infers each column's type; other rows
			// inherit it. (MySQL renders the union form and is not a ValuesCaster.)
			if !union && ri == 0 {
				if caster, ok := c.Dialect.(ValuesCaster); ok {
					if ve, ok := value.(ValueExpression); ok {
						if castType, ok := caster.ValuesCastType(ve.Value); ok {
							valueSQL = "CAST(" + valueSQL + " AS " + castType + ")"
						}
					}
				}
			}
			sb.WriteString(valueSQL)
			// Only the first UNION arm names the columns; the rest inherit them.
			if union && ri == 0 {
				sb.WriteString(" AS ")
				sb.WriteString(c.Dialect.QuoteIdent(v.Columns[ci].String()))
			}
			args = append(args, valueArgs...)
		}
		if !union {
			sb.WriteString(")")
		}
	}
	sb.WriteString(")")
	if !s.Alias.IsZero() {
		sb.WriteString(" AS ")
		sb.WriteString(c.Dialect.QuoteName(s.Alias.String()))
		if !union && len(v.Columns) == width {
			sb.WriteString("(")
			for ci, col := range v.Columns {
				if ci > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(c.Dialect.QuoteIdent(col.String()))
			}
			sb.WriteString(")")
		}
	}
	return sb.String(), args, nil
}

// compileSubquery compiles a nested query through this compiler when possible
// so placeholder numbering stays shared with the outer statement.
func (c *Compiler) compileSubquery(query Query) (string, []any, error) {
	if sub, ok := query.(compilable); ok {
		return sub.compile(c)
	}
	return query.ToSQL(c.Dialect)
}

// CompileJoin compiles a JOIN clause. A join's condition is mutually exclusive:
// NATURAL (no ON/USING), USING (a column list), or ON (a predicate).
func (c *Compiler) CompileJoin(join JoinClause) (string, []any, error) {
	if join.Natural && (join.On != nil || len(join.Using) > 0) {
		return "", nil, fmt.Errorf("sqlkit/sql: a NATURAL join cannot also carry ON or USING")
	}
	if join.On != nil && len(join.Using) > 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: a join cannot carry both ON and USING")
	}
	// MySQL has no FULL [OUTER] JOIN; emitting it verbatim produces SQL the engine
	// rejects. Surface it as a descriptive compile-time error naming the dialect,
	// the same stance the compiler takes for other dialect-unsupported constructs
	// (a single-element row, GROUPING SETS, …).
	if c.Dialect.Name() == "mysql" && strings.Contains(strings.ToUpper(join.Kind), "FULL") {
		return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support a FULL JOIN", c.Dialect.Name())
	}
	sourceSQL, args, err := c.CompileSource(join.Source)
	if err != nil {
		return "", nil, err
	}
	kind := join.Kind
	if join.Natural {
		kind = "NATURAL " + kind
	}
	compiled := kind + " " + sourceSQL
	switch {
	case len(join.Using) > 0:
		columns := make([]string, len(join.Using))
		for i, column := range join.Using {
			columns[i] = c.Dialect.QuoteName(column.String())
		}
		compiled += " USING (" + strings.Join(columns, ", ") + ")"
	case join.On != nil:
		onSQL, onArgs, err := c.CompileExpression(join.On)
		if err != nil {
			return "", nil, err
		}
		compiled += " ON " + onSQL
		args = append(args, onArgs...)
	}
	return compiled, args, nil
}

// compileWith emits the WITH [RECURSIVE] prefix for a list of CTEs, sharing this
// compiler so placeholder numbering stays consistent with the outer statement.
// The block is RECURSIVE when any CTE is. It returns "" when there are no CTEs,
// keeping non-CTE output byte-identical.
func (c *Compiler) compileWith(ctes []CTE) (string, []any, error) {
	if len(ctes) == 0 {
		return "", nil, nil
	}
	recursive := false
	for _, cte := range ctes {
		if cte.recursive {
			recursive = true
			break
		}
	}
	var sb strings.Builder
	var args []any
	sb.WriteString("WITH ")
	if recursive {
		sb.WriteString("RECURSIVE ")
	}
	for i, cte := range ctes {
		if i > 0 {
			sb.WriteString(", ")
		}
		if cte.name.IsZero() {
			return "", nil, fmt.Errorf("sqlkit/sql: CTE requires a name")
		}
		if cte.query == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: CTE %q requires a query", cte.name)
		}
		sb.WriteString(c.Dialect.QuoteName(cte.name.String()))
		if len(cte.columns) > 0 {
			sb.WriteString(" (")
			for j, col := range cte.columns {
				if j > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(c.Dialect.QuoteName(col.String()))
			}
			sb.WriteString(")")
		}
		sb.WriteString(" AS (")
		body, bodyArgs, err := c.compileSubquery(cte.query)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(body)
		sb.WriteString(")")
		args = append(args, bodyArgs...)
	}
	sb.WriteString(" ")
	return sb.String(), args, nil
}

// CompileSelect compiles a SELECT statement.
func (c *Compiler) CompileSelect(q SelectStmt) (string, []any, error) {
	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.With)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)
	sb.WriteString("SELECT ")
	if len(q.DistinctOn) > 0 {
		d, ok := c.Dialect.(DistinctOnDialect)
		if !ok || !d.SupportsDistinctOn() {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support DISTINCT ON", c.Dialect.Name())
		}
		sb.WriteString("DISTINCT ON (")
		for i, expr := range q.DistinctOn {
			if i > 0 {
				sb.WriteString(", ")
			}
			exprSQL, exprArgs, err := c.CompileExpression(expr)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(exprSQL)
			args = append(args, exprArgs...)
		}
		sb.WriteString(") ")
	} else if q.Distinct {
		sb.WriteString("DISTINCT ")
	}
	if len(q.Columns) == 0 {
		sb.WriteString("*")
	} else {
		for i, column := range q.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			selectionSQL, selectionArgs, err := c.CompileSelection(column)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(selectionSQL)
			args = append(args, selectionArgs...)
		}
	}
	// A source-less SELECT (e.g. SELECT now()) is legal as long as there is
	// an explicit select list and nothing that needs a FROM clause.
	if q.From == nil {
		if len(q.Columns) == 0 || len(q.Joins) > 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: missing source")
		}
	} else {
		fromSQL, fromArgs, err := c.CompileSource(q.From)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" FROM ")
		sb.WriteString(fromSQL)
		args = append(args, fromArgs...)
	}
	for _, join := range q.Joins {
		joinSQL, joinArgs, err := c.CompileJoin(join)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" ")
		sb.WriteString(joinSQL)
		args = append(args, joinArgs...)
	}
	if q.Where != nil {
		whereSQL, whereArgs, err := c.CompileExpression(q.Where)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" WHERE ")
		sb.WriteString(whereSQL)
		args = append(args, whereArgs...)
	}
	if len(q.GroupBy) > 0 {
		sb.WriteString(" GROUP BY ")
		for i, expr := range q.GroupBy {
			if i > 0 {
				sb.WriteString(", ")
			}
			exprSQL, exprArgs, err := c.CompileExpression(expr)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(exprSQL)
			args = append(args, exprArgs...)
		}
	}
	if q.Having != nil {
		havingSQL, havingArgs, err := c.CompileExpression(q.Having)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" HAVING ")
		sb.WriteString(havingSQL)
		args = append(args, havingArgs...)
	}
	if len(q.Windows) > 0 {
		sb.WriteString(" WINDOW ")
		for i, w := range q.Windows {
			if i > 0 {
				sb.WriteString(", ")
			}
			if w.Name.IsZero() {
				return "", nil, fmt.Errorf("sqlkit/sql: WINDOW definition requires a name")
			}
			body, bodyArgs, err := c.compileWindowParts(w.Partitions, w.OrderByTerms, w.FrameClause)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(c.Dialect.QuoteName(w.Name.String()))
			sb.WriteString(" AS (")
			sb.WriteString(body)
			sb.WriteString(")")
			args = append(args, bodyArgs...)
		}
	}
	if len(q.OrderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, term := range q.OrderBy {
			if i > 0 {
				sb.WriteString(", ")
			}
			termSQL, termArgs, err := c.CompileExpression(term.Expr)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(termSQL)
			if err := c.writeOrderModifiers(&sb, term); err != nil {
				return "", nil, err
			}
			args = append(args, termArgs...)
		}
	}
	if q.HasLimit {
		v, err := limitCount("LIMIT", q.Limit)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" LIMIT ")
		sb.WriteString(v)
	} else if q.HasOffset && c.Dialect.Name() == "mysql" {
		// MySQL has no bare OFFSET — OFFSET is only valid after a LIMIT. Emit
		// MySQL's documented "all rows" sentinel count so the OFFSET is accepted;
		// LIMIT <count> OFFSET <offset> is valid MySQL syntax.
		sb.WriteString(" LIMIT 18446744073709551615")
	}
	if q.HasOffset {
		v, err := limitCount("OFFSET", q.Offset)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" OFFSET ")
		sb.WriteString(v)
	}
	if q.Lock != nil {
		locker, ok := c.Dialect.(RowLocker)
		if !ok {
			return "", nil, fmt.Errorf("sqlkit/sql: dialect %q does not support row-locking clauses", c.Dialect.Name())
		}
		of := make([]string, len(q.Lock.of))
		for i, table := range q.Lock.of {
			of[i] = c.Dialect.QuoteIdent(table.String())
		}
		frag, err := locker.LockClause(q.Lock.strength, strings.Join(of, ", "), q.Lock.wait)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(frag)
	}
	return sb.String(), args, nil
}

// CompileInsert compiles an INSERT statement.
func (c *Compiler) CompileInsert(q InsertStmt) (string, []any, error) {
	if q.Table.Name.IsZero() {
		return "", nil, fmt.Errorf("sqlkit/sql: INSERT requires a table")
	}
	if q.Select != nil && len(q.Rows) > 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: INSERT cannot have both VALUES and a SELECT source")
	}
	if q.Select == nil {
		if len(q.Columns) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: INSERT requires columns")
		}
		if len(q.Rows) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: INSERT requires at least one row of values or a SELECT source")
		}
	}
	if q.Replace {
		if c.Dialect.Name() != "mysql" {
			return "", nil, fmt.Errorf("sqlkit/sql: REPLACE INTO is supported only on the mysql dialect")
		}
		if q.Conflict != nil {
			return "", nil, fmt.Errorf("sqlkit/sql: REPLACE INTO does not take an ON CONFLICT clause")
		}
		if len(q.Returning) > 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: REPLACE INTO does not support RETURNING")
		}
	}
	var sb strings.Builder
	var args []any
	if q.Replace {
		sb.WriteString("REPLACE INTO ")
	} else {
		sb.WriteString("INSERT INTO ")
	}
	sb.WriteString(c.Dialect.QuoteIdent(q.Table.QualifiedName()))
	if len(q.Columns) > 0 {
		sb.WriteString(" (")
		for i, column := range q.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c.Dialect.QuoteIdent(column.String()))
		}
		sb.WriteString(")")
	}
	if q.Select != nil {
		selectSQL, selectArgs, err := c.compileSubquery(q.Select)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" ")
		sb.WriteString(selectSQL)
		args = append(args, selectArgs...)
		if err := c.writeUpsert(&sb, &args, q.Conflict); err != nil {
			return "", nil, err
		}
		if err := c.writeReturning(&sb, &args, q.Returning); err != nil {
			return "", nil, err
		}
		return sb.String(), args, nil
	}
	sb.WriteString(" VALUES ")
	for i, row := range q.Rows {
		if len(row) != len(q.Columns) {
			return "", nil, fmt.Errorf("sqlkit/sql: INSERT row %d has %d values for %d columns", i, len(row), len(q.Columns))
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j, value := range row {
			if j > 0 {
				sb.WriteString(", ")
			}
			valueSQL, valueArgs, err := c.CompileExpression(value)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(valueSQL)
			args = append(args, valueArgs...)
		}
		sb.WriteString(")")
	}
	if err := c.writeUpsert(&sb, &args, q.Conflict); err != nil {
		return "", nil, err
	}
	if err := c.writeReturning(&sb, &args, q.Returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// writeUpsert appends the dialect's conflict clause (ON CONFLICT / ON DUPLICATE
// KEY UPDATE) to an INSERT. Assignment and WHERE placeholders are numbered after
// the VALUES/SELECT they follow, matching their textual position.
func (c *Compiler) writeUpsert(sb *strings.Builder, args *[]any, conflict *ConflictClause) error {
	if conflict == nil {
		return nil
	}
	renderer, ok := c.Dialect.(UpsertRenderer)
	if !ok {
		return fmt.Errorf("sqlkit/sql: dialect %s does not support upsert", c.Dialect.Name())
	}
	if conflict.DoUpdate && len(conflict.Sets) == 0 {
		return fmt.Errorf("sqlkit/sql: ON CONFLICT DO UPDATE requires at least one assignment")
	}
	targets := 0
	if len(conflict.Target) > 0 {
		targets++
	}
	if len(conflict.TargetExprs) > 0 {
		targets++
	}
	if !conflict.TargetConstraint.IsZero() {
		targets++
	}
	if targets > 1 {
		return fmt.Errorf("sqlkit/sql: ON CONFLICT can target only columns, expressions, or a named constraint")
	}
	var target []string
	if len(conflict.TargetExprs) > 0 {
		// An expression conflict target renders parenthesized, e.g.
		// ON CONFLICT ((lower(email))). Its placeholders precede the DO UPDATE
		// assignments, so compile it first.
		for _, expr := range conflict.TargetExprs {
			exprSQL, exprArgs, err := c.CompileExpression(expr)
			if err != nil {
				return err
			}
			target = append(target, "("+exprSQL+")")
			*args = append(*args, exprArgs...)
		}
	} else {
		target = make([]string, len(conflict.Target))
		for i, col := range conflict.Target {
			target[i] = c.Dialect.QuoteName(col.String())
		}
	}
	targetConstraint := ""
	if !conflict.TargetConstraint.IsZero() {
		targetConstraint = c.Dialect.QuoteName(conflict.TargetConstraint.String())
	}
	assignments := make([]string, 0, len(conflict.Sets))
	for _, assignment := range conflict.Sets {
		lhsSQL, lhsArgs, err := c.assignmentLHS(assignment)
		if err != nil {
			return err
		}
		valueSQL, valueArgs, err := c.CompileExpression(assignment.Value)
		if err != nil {
			return err
		}
		assignments = append(assignments, lhsSQL+" = "+valueSQL)
		*args = append(*args, lhsArgs...)
		*args = append(*args, valueArgs...)
	}
	// Only DO UPDATE carries a WHERE; on DO NOTHING the predicate is dropped, so
	// it must not contribute bind values either (which would desync the args).
	var whereSQL string
	if conflict.DoUpdate && conflict.Where != nil {
		w, whereArgs, err := c.CompileExpression(conflict.Where)
		if err != nil {
			return err
		}
		whereSQL = w
		*args = append(*args, whereArgs...)
	}
	clause, err := renderer.Upsert(UpsertClause{
		Target:           target,
		TargetConstraint: targetConstraint,
		DoUpdate:         conflict.DoUpdate,
		Assignments:      assignments,
		WhereSQL:         whereSQL,
	})
	if err != nil {
		return err
	}
	sb.WriteString(clause)
	return nil
}

// usesInlineJoins reports whether the dialect renders multi-table UPDATE/DELETE
// with MySQL-style inline joins rather than the standard FROM/USING form.
func (c *Compiler) usesInlineJoins() bool {
	jd, ok := c.Dialect.(JoinedUpdateDeleter)
	return ok && jd.UsesInlineJoins()
}

// writeCommaSources appends sources as a comma-separated list.
func (c *Compiler) writeCommaSources(sb *strings.Builder, args *[]any, sources []Source) error {
	for i, source := range sources {
		if i > 0 {
			sb.WriteString(", ")
		}
		sourceSQL, sourceArgs, err := c.CompileSource(source)
		if err != nil {
			return err
		}
		sb.WriteString(sourceSQL)
		*args = append(*args, sourceArgs...)
	}
	return nil
}

// CompileUpdate compiles an UPDATE statement. A multi-table UPDATE (From/Join)
// renders inline after the target on MySQL (UPDATE t JOIN j ON ... SET ...) and
// as a trailing FROM list on PostgreSQL (UPDATE t SET ... FROM j WHERE ...),
// where join ON predicates fold into the WHERE since FROM has no ON.
func (c *Compiler) CompileUpdate(q UpdateStmt) (string, []any, error) {
	if q.Table.Name.IsZero() {
		return "", nil, fmt.Errorf("sqlkit/sql: UPDATE requires a table")
	}
	if len(q.Sets) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: UPDATE requires at least one assignment")
	}
	inline := c.usesInlineJoins()
	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.With)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)
	sb.WriteString("UPDATE ")
	sb.WriteString(c.Dialect.QuoteIdent(q.Table.QualifiedName()))

	if inline {
		for _, source := range q.From {
			sb.WriteString(", ")
			sourceSQL, sourceArgs, err := c.CompileSource(source)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(sourceSQL)
			args = append(args, sourceArgs...)
		}
		for _, join := range q.Joins {
			joinSQL, joinArgs, err := c.CompileJoin(join)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(" ")
			sb.WriteString(joinSQL)
			args = append(args, joinArgs...)
		}
	}

	sb.WriteString(" SET ")
	for i, assignment := range q.Sets {
		if i > 0 {
			sb.WriteString(", ")
		}
		// The target's placeholders precede the value's, matching textual order
		// (col[idx] = value), so compile the left-hand side first.
		lhsSQL, lhsArgs, err := c.assignmentLHS(assignment)
		if err != nil {
			return "", nil, err
		}
		valueSQL, valueArgs, err := c.CompileExpression(assignment.Value)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(lhsSQL)
		sb.WriteString(" = ")
		sb.WriteString(valueSQL)
		args = append(args, lhsArgs...)
		args = append(args, valueArgs...)
	}

	where := q.Where
	if !inline {
		if err := rejectOuterJoins(q.Joins); err != nil {
			return "", nil, err
		}
		if sources := joinSources(q.From, q.Joins); len(sources) > 0 {
			sb.WriteString(" FROM ")
			if err := c.writeCommaSources(&sb, &args, sources); err != nil {
				return "", nil, err
			}
		}
		where = foldJoinOns(where, q.Joins)
	}
	if where != nil {
		whereSQL, whereArgs, err := c.CompileExpression(where)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" WHERE ")
		sb.WriteString(whereSQL)
		args = append(args, whereArgs...)
	}
	if err := c.writeRowLimit(&sb, &args, q.OrderBy, q.HasLimit, q.Limit, len(q.From) > 0 || len(q.Joins) > 0); err != nil {
		return "", nil, err
	}
	if err := c.writeReturning(&sb, &args, q.Returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// CompileDelete compiles a DELETE statement. A multi-table DELETE (Using/Join)
// renders as MySQL's DELETE t FROM t JOIN j ON ... and as PostgreSQL's
// DELETE FROM t USING j WHERE ..., where join ON predicates fold into the WHERE.
func (c *Compiler) CompileDelete(q DeleteStmt) (string, []any, error) {
	if q.Table.Name.IsZero() {
		return "", nil, fmt.Errorf("sqlkit/sql: DELETE requires a table")
	}
	inline := c.usesInlineJoins()
	hasSources := len(q.Using) > 0 || len(q.Joins) > 0
	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.With)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)
	target := c.Dialect.QuoteIdent(q.Table.QualifiedName())

	if inline && hasSources {
		sb.WriteString("DELETE ")
		sb.WriteString(target)
		sb.WriteString(" FROM ")
		sb.WriteString(target)
		for _, source := range q.Using {
			sb.WriteString(", ")
			sourceSQL, sourceArgs, err := c.CompileSource(source)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(sourceSQL)
			args = append(args, sourceArgs...)
		}
		for _, join := range q.Joins {
			joinSQL, joinArgs, err := c.CompileJoin(join)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(" ")
			sb.WriteString(joinSQL)
			args = append(args, joinArgs...)
		}
	} else {
		if err := rejectOuterJoins(q.Joins); err != nil {
			return "", nil, err
		}
		sb.WriteString("DELETE FROM ")
		sb.WriteString(target)
		if hasSources {
			sb.WriteString(" USING ")
			if err := c.writeCommaSources(&sb, &args, joinSources(q.Using, q.Joins)); err != nil {
				return "", nil, err
			}
		}
	}

	where := q.Where
	if !inline {
		where = foldJoinOns(where, q.Joins)
	}
	if where != nil {
		whereSQL, whereArgs, err := c.CompileExpression(where)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" WHERE ")
		sb.WriteString(whereSQL)
		args = append(args, whereArgs...)
	}
	if err := c.writeRowLimit(&sb, &args, q.OrderBy, q.HasLimit, q.Limit, hasSources); err != nil {
		return "", nil, err
	}
	if err := c.writeReturning(&sb, &args, q.Returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// joinSources flattens explicit sources and join sources into one list, for the
// standard FROM/USING form where joins have no ON clause of their own.
func joinSources(sources []Source, joins []JoinClause) []Source {
	out := append([]Source(nil), sources...)
	for _, join := range joins {
		out = append(out, join.Source)
	}
	return out
}

// rejectOuterJoins errors on any non-inner join, for the standard FROM/USING
// form (PostgreSQL) that folds join ON predicates into WHERE. That folding is
// correct only for inner joins: a LEFT JOIN flattened this way would silently
// become an inner join (e.g. an "outer row with no match" predicate turns
// unsatisfiable), so reject it rather than emit wrong-set-of-rows SQL.
func rejectOuterJoins(joins []JoinClause) error {
	for _, join := range joins {
		if join.Kind != "JOIN" {
			return fmt.Errorf("sqlkit/sql: %s in a multi-table UPDATE/DELETE is unsupported for this dialect "+
				"(its ON would fold into WHERE, collapsing the outer join to an inner one); use an inner Join", join.Kind)
		}
	}
	return nil
}

// foldJoinOns ANDs each join's ON predicate into where, used by the standard
// FROM/USING form (PostgreSQL), whose FROM/USING list carries no ON.
func foldJoinOns(where Expression, joins []JoinClause) Expression {
	for _, join := range joins {
		if join.On == nil {
			continue
		}
		if where == nil {
			where = join.On
		} else {
			where = And(where, join.On)
		}
	}
	return where
}

func (c *Compiler) writeReturning(sb *strings.Builder, args *[]any, returning []Selection) error {
	if len(returning) == 0 {
		return nil
	}
	if !c.Dialect.SupportsReturning() {
		return fmt.Errorf("sqlkit/sql: dialect %s does not support RETURNING; use Exec and Result.LastInsertId instead",
			c.Dialect.Name())
	}
	sb.WriteString(" RETURNING ")
	for i, selection := range returning {
		if i > 0 {
			sb.WriteString(", ")
		}
		selectionSQL, selectionArgs, err := c.CompileSelection(selection)
		if err != nil {
			return err
		}
		sb.WriteString(selectionSQL)
		*args = append(*args, selectionArgs...)
	}
	return nil
}

// assignmentLHS renders an assignment's left-hand side: the Target expression
// when set (e.g. a subscripted column), otherwise the quoted column name.
func (c *Compiler) assignmentLHS(a Assignment) (string, []any, error) {
	if a.Target != nil {
		return c.CompileExpression(a.Target)
	}
	return c.Dialect.QuoteIdent(a.Column.String()), nil, nil
}

// compileExprList compiles a comma-separated list of expressions.
// supportsGroupingSets reports whether the dialect can render the ROLLUP / CUBE /
// GROUPING SETS grouping elements. MySQL cannot, so the compiler rejects them
// rather than emit SQL it would reject (its ROLLUP is the trailing WITH ROLLUP).
func supportsGroupingSets(d Dialect) bool {
	g, ok := d.(GroupingSetsDialect)
	return ok && g.SupportsGroupingSets()
}

func (c *Compiler) compileExprList(exprs []Expression) (string, []any, error) {
	var sb strings.Builder
	var args []any
	for i, expr := range exprs {
		if i > 0 {
			sb.WriteString(", ")
		}
		exprSQL, exprArgs, err := c.CompileExpression(expr)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(exprSQL)
		args = append(args, exprArgs...)
	}
	return sb.String(), args, nil
}

// compileSubscript renders operand[...] for one or more subscript/slice steps.
func (c *Compiler) compileSubscript(e SubscriptExpression) (string, []any, error) {
	operandSQL, args, err := c.CompileExpression(e.Operand)
	if err != nil {
		return "", nil, err
	}
	// Only a column/field reference (or another subscript) can be subscripted
	// bare; any other operand — an array constructor, a function call, a cast —
	// must be parenthesized, or "ARRAY[1,2,3][1]" parses as a malformed array
	// type rather than an index into the constructed array.
	if !bareSubscriptable(e.Operand) {
		operandSQL = "(" + operandSQL + ")"
	}
	var sb strings.Builder
	sb.WriteString(operandSQL)
	for _, idx := range e.Indices {
		sb.WriteString("[")
		if idx.Slice {
			if idx.Lower != nil {
				lo, loArgs, err := c.CompileExpression(idx.Lower)
				if err != nil {
					return "", nil, err
				}
				sb.WriteString(lo)
				args = append(args, loArgs...)
			}
			sb.WriteString(":")
			if idx.Upper != nil {
				hi, hiArgs, err := c.CompileExpression(idx.Upper)
				if err != nil {
					return "", nil, err
				}
				sb.WriteString(hi)
				args = append(args, hiArgs...)
			}
		} else {
			if idx.Upper == nil {
				return "", nil, fmt.Errorf("sqlkit/sql: array subscript requires an index")
			}
			up, upArgs, err := c.CompileExpression(idx.Upper)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(up)
			args = append(args, upArgs...)
		}
		sb.WriteString("]")
	}
	return sb.String(), args, nil
}

// bareSubscriptable reports whether e can carry a [...] subscript without being
// wrapped in parentheses. A column reference renders bare ("tags"[1]) and a
// nested subscript chains (a[1][2]); every other operand — an array constructor,
// a function call, a cast, even a field access (which needs ((rec).f)[1], not
// (rec).f[1], or PostgreSQL no longer reads it as a subscript) — needs explicit
// parentheses.
func bareSubscriptable(e Expression) bool {
	switch e.(type) {
	case ColumnExpression, SubscriptExpression:
		return true
	default:
		return false
	}
}

func (c *Compiler) nextPlaceholder() string {
	c.ordinal++
	return c.Dialect.Placeholder(c.ordinal)
}

// compileWindowParts compiles the inner clauses of a window — PARTITION BY,
// ORDER BY, and a raw frame clause — into the space-joined body that sits inside
// an OVER (...) clause or a WINDOW "name AS (...)" definition. It returns "" when
// the window carries no clauses. It is shared by WindowExpression and the
// SELECT-level WINDOW clause so both render identically.
func (c *Compiler) compileWindowParts(partitions []Expression, order []OrderTerm, frame string) (string, []any, error) {
	var parts []string
	var args []any
	if len(partitions) > 0 {
		var b strings.Builder
		b.WriteString("PARTITION BY ")
		for i, expr := range partitions {
			if i > 0 {
				b.WriteString(", ")
			}
			exprSQL, exprArgs, err := c.CompileExpression(expr)
			if err != nil {
				return "", nil, err
			}
			b.WriteString(exprSQL)
			args = append(args, exprArgs...)
		}
		parts = append(parts, b.String())
	}
	if len(order) > 0 {
		var b strings.Builder
		b.WriteString("ORDER BY ")
		for i, term := range order {
			if i > 0 {
				b.WriteString(", ")
			}
			termSQL, termArgs, err := c.CompileExpression(term.Expr)
			if err != nil {
				return "", nil, err
			}
			b.WriteString(termSQL)
			if err := c.writeOrderModifiers(&b, term); err != nil {
				return "", nil, err
			}
			args = append(args, termArgs...)
		}
		parts = append(parts, b.String())
	}
	if frame != "" {
		parts = append(parts, frame)
	}
	return strings.Join(parts, " "), args, nil
}

// writeOrderModifiers appends the direction and NULLS ordering of an ORDER BY
// term (after its already-written expression) to sb. A NULLS FIRST/LAST modifier
// is rejected on a dialect that does not support it (MySQL) rather than emitted
// as SQL that dialect cannot parse.
func (c *Compiler) writeOrderModifiers(sb *strings.Builder, term OrderTerm) error {
	if term.Desc {
		sb.WriteString(" DESC")
	}
	if term.Nulls != "" {
		if d, ok := c.Dialect.(NullsOrderingDialect); !ok || !d.SupportsNullsOrdering() {
			return fmt.Errorf("sqlkit/sql: dialect %q does not support NULLS ordering", c.Dialect.Name())
		}
		sb.WriteString(" NULLS ")
		sb.WriteString(term.Nulls)
	}
	return nil
}

// writeOrderBy renders an " ORDER BY ..." clause, writing nothing when empty.
func (c *Compiler) writeOrderBy(sb *strings.Builder, args *[]any, terms []OrderTerm) error {
	if len(terms) == 0 {
		return nil
	}
	sb.WriteString(" ORDER BY ")
	for i, term := range terms {
		if i > 0 {
			sb.WriteString(", ")
		}
		termSQL, termArgs, err := c.CompileExpression(term.Expr)
		if err != nil {
			return err
		}
		sb.WriteString(termSQL)
		if err := c.writeOrderModifiers(sb, term); err != nil {
			return err
		}
		*args = append(*args, termArgs...)
	}
	return nil
}

// writeRowLimit renders the "ORDER BY ... LIMIT n" tail of an UPDATE or DELETE,
// which bounds the affected rows. Only MySQL accepts it on data-modifying
// statements, and only on the single-table form: MySQL rejects ORDER BY / LIMIT
// on a multi-table (joined) UPDATE or DELETE. Other dialects reject it outright.
// multiTable reports whether the statement carries join/extra sources.
func (c *Compiler) writeRowLimit(sb *strings.Builder, args *[]any, orderBy []OrderTerm, hasLimit bool, limit int, multiTable bool) error {
	if len(orderBy) == 0 && !hasLimit {
		return nil
	}
	if c.Dialect.Name() != "mysql" {
		return fmt.Errorf("sqlkit/sql: ORDER BY / LIMIT on UPDATE/DELETE is supported only on the mysql dialect")
	}
	if multiTable {
		return fmt.Errorf("sqlkit/sql: MySQL does not allow ORDER BY / LIMIT on a multi-table UPDATE/DELETE")
	}
	if err := c.writeOrderBy(sb, args, orderBy); err != nil {
		return err
	}
	if hasLimit {
		v, err := limitCount("LIMIT", limit)
		if err != nil {
			return err
		}
		sb.WriteString(" LIMIT ")
		sb.WriteString(v)
	}
	return nil
}

// limitCount renders a LIMIT/OFFSET count, rejecting a negative value. Both
// PostgreSQL ("LIMIT/OFFSET must not be negative") and MySQL (whose grammar
// accepts only a non-negative integer there) reject one, so emit a descriptive
// compile-time error rather than SQL the engine refuses. clause names the
// offending keyword for the message.
func limitCount(clause string, n int) (string, error) {
	if n < 0 {
		return "", fmt.Errorf("sqlkit/sql: %s must not be negative, got %d", clause, n)
	}
	return strconv.Itoa(n), nil
}
