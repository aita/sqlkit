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
		var sb strings.Builder
		var args []any
		sb.WriteString(e.Name)
		sb.WriteString("(")
		for i, arg := range e.Args {
			if i > 0 {
				sb.WriteString(", ")
			}
			argSQL, argArgs, err := c.CompileExpression(arg)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(argSQL)
			args = append(args, argArgs...)
		}
		sb.WriteString(")")
		return sb.String(), args, nil
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
				if term.Desc {
					order.WriteString(" DESC")
				}
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
	if s.Query != nil {
		sql, args, err := c.compileSubquery(s.Query)
		if err != nil {
			return "", nil, err
		}
		compiled := "(" + sql + ")"
		if s.Alias != "" {
			compiled += " AS " + c.Dialect.QuoteIdent(s.Alias)
		}
		return compiled, args, nil
	}
	if s.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: missing source name")
	}
	compiled := c.Dialect.QuoteIdent(s.Name)
	if s.Alias != "" {
		compiled += " AS " + c.Dialect.QuoteIdent(s.Alias)
	}
	return compiled, nil, nil
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

// CompileSelect compiles a SELECT statement.
func (c *Compiler) CompileSelect(q SelectQuery) (string, []any, error) {
	var sb strings.Builder
	var args []any
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
			if term.Desc {
				sb.WriteString(" DESC")
			}
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
	if err := c.writeReturning(&sb, &args, q.returning); err != nil {
		return "", nil, err
	}
	return sb.String(), args, nil
}

// CompileUpdate compiles an UPDATE statement.
func (c *Compiler) CompileUpdate(q UpdateQuery) (string, []any, error) {
	if q.table.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: UPDATE requires a table")
	}
	if len(q.sets) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: UPDATE requires at least one assignment")
	}
	var sb strings.Builder
	var args []any
	sb.WriteString("UPDATE ")
	sb.WriteString(c.Dialect.QuoteIdent(q.table.QualifiedName()))
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
	if q.where != nil {
		whereSQL, whereArgs, err := c.CompileExpression(q.where)
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

// CompileDelete compiles a DELETE statement.
func (c *Compiler) CompileDelete(q DeleteQuery) (string, []any, error) {
	if q.table.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: DELETE requires a table")
	}
	var sb strings.Builder
	var args []any
	sb.WriteString("DELETE FROM ")
	sb.WriteString(c.Dialect.QuoteIdent(q.table.QualifiedName()))
	if q.where != nil {
		whereSQL, whereArgs, err := c.CompileExpression(q.where)
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
