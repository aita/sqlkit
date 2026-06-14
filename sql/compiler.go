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
		return c.nextPlaceholder(), []any{e.Value}, nil
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
	case ExcludedExpression:
		if e.Column == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: Excluded requires a column name")
		}
		if r, ok := c.Dialect.(ExcludedRenderer); ok {
			return r.Excluded(e.Column), nil, nil
		}
		return "excluded." + c.Dialect.QuoteName(e.Column), nil, nil
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
		// falls through to the default name(args...) rendering unchanged.
		if fr, ok := c.Dialect.(FuncRenderer); ok {
			if out, handled := fr.RenderFunc(e.Name, compiled); handled {
				return out, args, nil
			}
		}
		return e.Name + "(" + strings.Join(compiled, ", ") + ")", args, nil
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
		return "COUNT(" + target + ")", args, nil
	case CaseExpression:
		if len(e.Whens) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: CASE requires at least one WHEN arm")
		}
		var sb strings.Builder
		var args []any
		sb.WriteString("CASE")
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
		var sb strings.Builder
		sb.WriteString(fnSQL)
		sb.WriteString(" OVER (")
		var parts []string
		if len(e.Partitions) > 0 {
			var partition strings.Builder
			partition.WriteString("PARTITION BY ")
			for i, expr := range e.Partitions {
				if i > 0 {
					partition.WriteString(", ")
				}
				exprSQL, exprArgs, err := c.CompileExpression(expr)
				if err != nil {
					return "", nil, err
				}
				partition.WriteString(exprSQL)
				args = append(args, exprArgs...)
			}
			parts = append(parts, partition.String())
		}
		if len(e.OrderByTerms) > 0 {
			var order strings.Builder
			order.WriteString("ORDER BY ")
			for i, term := range e.OrderByTerms {
				if i > 0 {
					order.WriteString(", ")
				}
				termSQL, termArgs, err := c.CompileExpression(term.Expr)
				if err != nil {
					return "", nil, err
				}
				order.WriteString(termSQL)
				writeOrderModifiers(&order, term)
				args = append(args, termArgs...)
			}
			parts = append(parts, order.String())
		}
		if e.FrameClause != "" {
			parts = append(parts, e.FrameClause)
		}
		sb.WriteString(strings.Join(parts, " "))
		sb.WriteString(")")
		return sb.String(), args, nil
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
		if s.Qualifier == "" {
			return "*", nil, nil
		}
		return c.Dialect.QuoteIdent(s.Qualifier) + ".*", nil, nil
	case AliasedSelection:
		inner, args, err := c.CompileSelection(s.Selection)
		if err != nil {
			return "", nil, err
		}
		return inner + " AS " + c.Dialect.QuoteIdent(s.Alias), args, nil
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
		if s.Alias != "" {
			compiled += " AS " + c.Dialect.QuoteIdent(s.Alias)
		}
		return compiled, args, nil
	}
	if s.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: missing source name")
	}
	compiled := lateral + c.Dialect.QuoteIdent(s.Name)
	if s.Alias != "" {
		compiled += " AS " + c.Dialect.QuoteIdent(s.Alias)
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
				sb.WriteString(c.Dialect.QuoteIdent(v.Columns[ci]))
			}
			args = append(args, valueArgs...)
		}
		if !union {
			sb.WriteString(")")
		}
	}
	sb.WriteString(")")
	if s.Alias != "" {
		sb.WriteString(" AS ")
		sb.WriteString(c.Dialect.QuoteIdent(s.Alias))
		if !union && len(v.Columns) == width {
			sb.WriteString("(")
			for ci, col := range v.Columns {
				if ci > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(c.Dialect.QuoteIdent(col))
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

// CompileJoin compiles a JOIN clause.
func (c *Compiler) CompileJoin(join JoinClause) (string, []any, error) {
	sourceSQL, args, err := c.CompileSource(join.Source)
	if err != nil {
		return "", nil, err
	}
	compiled := join.Kind + " " + sourceSQL
	if join.On != nil {
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
		if cte.name == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: CTE requires a name")
		}
		if cte.query == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: CTE %q requires a query", cte.name)
		}
		sb.WriteString(c.Dialect.QuoteName(cte.name))
		if len(cte.columns) > 0 {
			sb.WriteString(" (")
			for j, col := range cte.columns {
				if j > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(c.Dialect.QuoteName(col))
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
func (c *Compiler) CompileSelect(q SelectQuery) (string, []any, error) {
	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.with)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)
	sb.WriteString("SELECT ")
	if q.distinct {
		sb.WriteString("DISTINCT ")
	}
	if len(q.columns) == 0 {
		sb.WriteString("*")
	} else {
		for i, column := range q.columns {
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
	if q.from == nil {
		if len(q.columns) == 0 || len(q.joins) > 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: missing source")
		}
	} else {
		fromSQL, fromArgs, err := c.CompileSource(q.from)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" FROM ")
		sb.WriteString(fromSQL)
		args = append(args, fromArgs...)
	}
	for _, join := range q.joins {
		joinSQL, joinArgs, err := c.CompileJoin(join)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" ")
		sb.WriteString(joinSQL)
		args = append(args, joinArgs...)
	}
	if q.where != nil {
		whereSQL, whereArgs, err := c.CompileExpression(q.where)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" WHERE ")
		sb.WriteString(whereSQL)
		args = append(args, whereArgs...)
	}
	if len(q.groupBy) > 0 {
		sb.WriteString(" GROUP BY ")
		for i, expr := range q.groupBy {
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
	if q.having != nil {
		havingSQL, havingArgs, err := c.CompileExpression(q.having)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" HAVING ")
		sb.WriteString(havingSQL)
		args = append(args, havingArgs...)
	}
	if len(q.orderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, term := range q.orderBy {
			if i > 0 {
				sb.WriteString(", ")
			}
			termSQL, termArgs, err := c.CompileExpression(term.Expr)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(termSQL)
			writeOrderModifiers(&sb, term)
			args = append(args, termArgs...)
		}
	}
	if q.hasLimit {
		sb.WriteString(" LIMIT ")
		sb.WriteString(strconv.Itoa(q.limit))
	}
	if q.hasOffset {
		sb.WriteString(" OFFSET ")
		sb.WriteString(strconv.Itoa(q.offset))
	}
	return sb.String(), args, nil
}

// CompileInsert compiles an INSERT statement.
func (c *Compiler) CompileInsert(q InsertQuery) (string, []any, error) {
	if q.table.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: INSERT requires a table")
	}
	if q.selectSrc != nil && len(q.rows) > 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: INSERT cannot have both VALUES and a SELECT source")
	}
	if q.selectSrc == nil {
		if len(q.columns) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: INSERT requires columns")
		}
		if len(q.rows) == 0 {
			return "", nil, fmt.Errorf("sqlkit/sql: INSERT requires at least one row of values or a SELECT source")
		}
	}
	var sb strings.Builder
	var args []any
	sb.WriteString("INSERT INTO ")
	sb.WriteString(c.Dialect.QuoteIdent(q.table.QualifiedName()))
	if len(q.columns) > 0 {
		sb.WriteString(" (")
		for i, column := range q.columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c.Dialect.QuoteIdent(column))
		}
		sb.WriteString(")")
	}
	if q.selectSrc != nil {
		selectSQL, selectArgs, err := c.compileSubquery(q.selectSrc)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(" ")
		sb.WriteString(selectSQL)
		args = append(args, selectArgs...)
		if err := c.writeUpsert(&sb, &args, q.conflict); err != nil {
			return "", nil, err
		}
		if err := c.writeReturning(&sb, &args, q.returning); err != nil {
			return "", nil, err
		}
		return sb.String(), args, nil
	}
	sb.WriteString(" VALUES ")
	for i, row := range q.rows {
		if len(row) != len(q.columns) {
			return "", nil, fmt.Errorf("sqlkit/sql: INSERT row %d has %d values for %d columns", i, len(row), len(q.columns))
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
	if err := c.writeUpsert(&sb, &args, q.conflict); err != nil {
		return "", nil, err
	}
	if err := c.writeReturning(&sb, &args, q.returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// writeUpsert appends the dialect's conflict clause (ON CONFLICT / ON DUPLICATE
// KEY UPDATE) to an INSERT. Assignment and WHERE placeholders are numbered after
// the VALUES/SELECT they follow, matching their textual position.
func (c *Compiler) writeUpsert(sb *strings.Builder, args *[]any, conflict *conflictClause) error {
	if conflict == nil {
		return nil
	}
	renderer, ok := c.Dialect.(UpsertRenderer)
	if !ok {
		return fmt.Errorf("sqlkit/sql: dialect %s does not support upsert", c.Dialect.Name())
	}
	if conflict.doUpdate && len(conflict.sets) == 0 {
		return fmt.Errorf("sqlkit/sql: ON CONFLICT DO UPDATE requires at least one assignment")
	}
	target := make([]string, len(conflict.target))
	for i, col := range conflict.target {
		target[i] = c.Dialect.QuoteName(col)
	}
	assignments := make([]string, 0, len(conflict.sets))
	for _, assignment := range conflict.sets {
		valueSQL, valueArgs, err := c.CompileExpression(assignment.Value)
		if err != nil {
			return err
		}
		assignments = append(assignments, c.Dialect.QuoteIdent(assignment.Column)+" = "+valueSQL)
		*args = append(*args, valueArgs...)
	}
	// Only DO UPDATE carries a WHERE; on DO NOTHING the predicate is dropped, so
	// it must not contribute bind values either (which would desync the args).
	var whereSQL string
	if conflict.doUpdate && conflict.where != nil {
		w, whereArgs, err := c.CompileExpression(conflict.where)
		if err != nil {
			return err
		}
		whereSQL = w
		*args = append(*args, whereArgs...)
	}
	clause, err := renderer.Upsert(UpsertClause{
		Target:      target,
		DoUpdate:    conflict.doUpdate,
		Assignments: assignments,
		WhereSQL:    whereSQL,
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
func (c *Compiler) CompileUpdate(q UpdateQuery) (string, []any, error) {
	if q.table.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: UPDATE requires a table")
	}
	if len(q.sets) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: UPDATE requires at least one assignment")
	}
	inline := c.usesInlineJoins()
	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.with)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)
	sb.WriteString("UPDATE ")
	sb.WriteString(c.Dialect.QuoteIdent(q.table.QualifiedName()))

	if inline {
		for _, source := range q.from {
			sb.WriteString(", ")
			sourceSQL, sourceArgs, err := c.CompileSource(source)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(sourceSQL)
			args = append(args, sourceArgs...)
		}
		for _, join := range q.joins {
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
	for i, assignment := range q.sets {
		if i > 0 {
			sb.WriteString(", ")
		}
		valueSQL, valueArgs, err := c.CompileExpression(assignment.Value)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(c.Dialect.QuoteIdent(assignment.Column))
		sb.WriteString(" = ")
		sb.WriteString(valueSQL)
		args = append(args, valueArgs...)
	}

	where := q.where
	if !inline {
		if err := rejectOuterJoins(q.joins); err != nil {
			return "", nil, err
		}
		if sources := joinSources(q.from, q.joins); len(sources) > 0 {
			sb.WriteString(" FROM ")
			if err := c.writeCommaSources(&sb, &args, sources); err != nil {
				return "", nil, err
			}
		}
		where = foldJoinOns(where, q.joins)
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
	if err := c.writeReturning(&sb, &args, q.returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// CompileDelete compiles a DELETE statement. A multi-table DELETE (Using/Join)
// renders as MySQL's DELETE t FROM t JOIN j ON ... and as PostgreSQL's
// DELETE FROM t USING j WHERE ..., where join ON predicates fold into the WHERE.
func (c *Compiler) CompileDelete(q DeleteQuery) (string, []any, error) {
	if q.table.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: DELETE requires a table")
	}
	inline := c.usesInlineJoins()
	hasSources := len(q.using) > 0 || len(q.joins) > 0
	var sb strings.Builder
	var args []any
	withSQL, withArgs, err := c.compileWith(q.with)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(withSQL)
	args = append(args, withArgs...)
	target := c.Dialect.QuoteIdent(q.table.QualifiedName())

	if inline && hasSources {
		sb.WriteString("DELETE ")
		sb.WriteString(target)
		sb.WriteString(" FROM ")
		sb.WriteString(target)
		for _, source := range q.using {
			sb.WriteString(", ")
			sourceSQL, sourceArgs, err := c.CompileSource(source)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(sourceSQL)
			args = append(args, sourceArgs...)
		}
		for _, join := range q.joins {
			joinSQL, joinArgs, err := c.CompileJoin(join)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(" ")
			sb.WriteString(joinSQL)
			args = append(args, joinArgs...)
		}
	} else {
		if err := rejectOuterJoins(q.joins); err != nil {
			return "", nil, err
		}
		sb.WriteString("DELETE FROM ")
		sb.WriteString(target)
		if hasSources {
			sb.WriteString(" USING ")
			if err := c.writeCommaSources(&sb, &args, joinSources(q.using, q.joins)); err != nil {
				return "", nil, err
			}
		}
	}

	where := q.where
	if !inline {
		where = foldJoinOns(where, q.joins)
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
	if err := c.writeReturning(&sb, &args, q.returning); err != nil {
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

func (c *Compiler) nextPlaceholder() string {
	c.ordinal++
	return c.Dialect.Placeholder(c.ordinal)
}

// writeOrderModifiers appends the direction and NULLS ordering of an ORDER BY
// term (after its already-written expression) to sb.
func writeOrderModifiers(sb *strings.Builder, term OrderTerm) {
	if term.Desc {
		sb.WriteString(" DESC")
	}
	if term.Nulls != "" {
		sb.WriteString(" NULLS ")
		sb.WriteString(term.Nulls)
	}
}
