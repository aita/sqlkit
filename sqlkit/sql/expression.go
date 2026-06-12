// Package sql contains SQL query builders, expression nodes, and the
// compiler that turns them into SQL text plus bind arguments.
//
// This package is the untyped core of sqlkit. Most applications should use
// the typed builders in package sqlkit (backed by code generated with
// sqlkitgen), which compile down to the nodes defined here.
package sql

// Expression is a composable SQL AST node.
type Expression interface {
	SQLExpression()
}

// Selection is a SELECT-list item such as a column or aggregate expression.
type Selection interface {
	SQLSelection()
}

// RawExpression is an escape hatch for static SQL fragments.
type RawExpression string

func (RawExpression) SQLExpression() {}
func (RawExpression) SQLSelection()  {}

// Raw creates a raw SQL fragment expression.
func Raw(fragment string) RawExpression { return RawExpression(fragment) }

// ColumnExpression references a column name.
type ColumnExpression struct{ Name string }

func (ColumnExpression) SQLExpression() {}
func (ColumnExpression) SQLSelection()  {}

// Reference returns the SQL column reference.
func (e ColumnExpression) Reference() string { return e.Name }

// Col creates a column reference expression.
func Col(name string) ColumnExpression { return ColumnExpression{Name: name} }

// ValueExpression binds a value as a SQL argument.
type ValueExpression struct{ Value any }

func (ValueExpression) SQLExpression() {}

// Val creates a bind parameter expression.
func Val(value any) ValueExpression { return ValueExpression{Value: value} }

// Int creates an integer bind parameter expression.
func Int(value int) ValueExpression { return Val(value) }

// BinaryExpression combines two expressions with an operator.
type BinaryExpression struct {
	Left     Expression
	Operator string
	Right    Expression
}

func (BinaryExpression) SQLExpression() {}

// Eq creates an equality expression. Non-expression operands are converted to
// bind values so callers can write Eq(Col("id"), 123).
func Eq(left, right any) BinaryExpression { return binary(left, "=", right) }

// Ne creates a "not equal" expression.
func Ne(left, right any) BinaryExpression { return binary(left, "<>", right) }

// Lt creates a "less than" expression.
func Lt(left, right any) BinaryExpression { return binary(left, "<", right) }

// Le creates a "less than or equal" expression.
func Le(left, right any) BinaryExpression { return binary(left, "<=", right) }

// Gt creates a "greater than" expression.
func Gt(left, right any) BinaryExpression { return binary(left, ">", right) }

// Ge creates a "greater than or equal" expression.
func Ge(left, right any) BinaryExpression { return binary(left, ">=", right) }

// Like creates a LIKE expression.
func Like(left, pattern any) BinaryExpression { return binary(left, "LIKE", pattern) }

func binary(left any, operator string, right any) BinaryExpression {
	return BinaryExpression{Left: Expr(left), Operator: operator, Right: Expr(right)}
}

// LogicalExpression combines predicates with AND or OR.
type LogicalExpression struct {
	Operator string
	Operands []Expression
}

func (LogicalExpression) SQLExpression() {}

// And combines predicates with AND. With a single operand it is the identity.
func And(operands ...Expression) Expression { return logical("AND", operands) }

// Or combines predicates with OR. With a single operand it is the identity.
func Or(operands ...Expression) Expression { return logical("OR", operands) }

func logical(operator string, operands []Expression) Expression {
	if len(operands) == 1 {
		return operands[0]
	}
	return LogicalExpression{Operator: operator, Operands: operands}
}

// NotExpression negates a predicate.
type NotExpression struct{ Operand Expression }

func (NotExpression) SQLExpression() {}

// Not negates a predicate.
func Not(operand Expression) NotExpression { return NotExpression{Operand: operand} }

// InExpression tests membership in a list of expressions.
type InExpression struct {
	Left   Expression
	Items  []Expression
	Negate bool
}

func (InExpression) SQLExpression() {}

// In creates an IN expression. Non-expression items become bind values.
func In(left any, items ...any) InExpression {
	expressions := make([]Expression, len(items))
	for i, item := range items {
		expressions[i] = Expr(item)
	}
	return InExpression{Left: Expr(left), Items: expressions}
}

// NotIn creates a NOT IN expression.
func NotIn(left any, items ...any) InExpression {
	expr := In(left, items...)
	expr.Negate = true
	return expr
}

// NullPredicate tests an expression for IS NULL / IS NOT NULL.
type NullPredicate struct {
	Operand Expression
	Negate  bool
}

func (NullPredicate) SQLExpression() {}

// IsNull creates an IS NULL predicate.
func IsNull(operand any) NullPredicate { return NullPredicate{Operand: Expr(operand)} }

// IsNotNull creates an IS NOT NULL predicate.
func IsNotNull(operand any) NullPredicate {
	return NullPredicate{Operand: Expr(operand), Negate: true}
}

// CountExpression represents COUNT(expr) or COUNT(*).
type CountExpression struct {
	Target Selection
	All    bool
}

func (CountExpression) SQLExpression() {}
func (CountExpression) SQLSelection()  {}

// Over turns the aggregate into a window expression.
func (e CountExpression) Over() WindowExpression { return WindowExpression{Fn: e} }

// Count creates a COUNT(selection) aggregate.
func Count(target Selection) CountExpression { return CountExpression{Target: target} }

// CountAll creates a COUNT(*) aggregate.
func CountAll() CountExpression { return CountExpression{All: true} }

// AliasedSelection attaches an AS alias to a SELECT-list item.
type AliasedSelection struct {
	Selection Selection
	Alias     string
}

func (AliasedSelection) SQLSelection() {}

// As attaches an alias to a SELECT-list item.
func As(selection Selection, alias string) AliasedSelection {
	return AliasedSelection{Selection: selection, Alias: alias}
}

// OrderTerm is one ORDER BY term.
type OrderTerm struct {
	Expr Expression
	Desc bool
}

// Asc creates an ascending ORDER BY term.
func Asc(expr Expression) OrderTerm { return OrderTerm{Expr: expr} }

// Desc creates a descending ORDER BY term.
func Desc(expr Expression) OrderTerm { return OrderTerm{Expr: expr, Desc: true} }

// Assignment is one SET clause item in UPDATE, or a column/value pair in INSERT.
type Assignment struct {
	Column string
	Value  Expression
}

// Set creates an assignment. A non-expression value becomes a bind value.
func Set(column string, value any) Assignment {
	return Assignment{Column: column, Value: Expr(value)}
}

// Expr converts common values into AST expressions.
func Expr(value any) Expression {
	if expr, ok := value.(Expression); ok {
		return expr
	}
	return Val(value)
}

// FuncExpression is a function call such as COALESCE(a, b) or SUM(x).
type FuncExpression struct {
	Name string
	Args []Expression
}

func (FuncExpression) SQLExpression() {}
func (FuncExpression) SQLSelection()  {}

// Func creates a function call expression. Non-expression arguments become
// bind values.
func Func(name string, args ...any) FuncExpression {
	expressions := make([]Expression, len(args))
	for i, arg := range args {
		expressions[i] = Expr(arg)
	}
	return FuncExpression{Name: name, Args: expressions}
}

// Over turns the function into a window expression, e.g.
// RowNumber().Over().PartitionBy(col).OrderBy(Desc(other)).
func (f FuncExpression) Over() WindowExpression { return WindowExpression{Fn: f} }

// Coalesce creates COALESCE(args...).
func Coalesce(args ...any) FuncExpression { return Func("COALESCE", args...) }

// NullIf creates NULLIF(a, b).
func NullIf(a, b any) FuncExpression { return Func("NULLIF", a, b) }

// Greatest creates GREATEST(args...).
func Greatest(args ...any) FuncExpression { return Func("GREATEST", args...) }

// Least creates LEAST(args...).
func Least(args ...any) FuncExpression { return Func("LEAST", args...) }

// Sum creates a SUM(expr) aggregate.
func Sum(arg any) FuncExpression { return Func("SUM", arg) }

// Avg creates an AVG(expr) aggregate.
func Avg(arg any) FuncExpression { return Func("AVG", arg) }

// Min creates a MIN(expr) aggregate.
func Min(arg any) FuncExpression { return Func("MIN", arg) }

// Max creates a MAX(expr) aggregate.
func Max(arg any) FuncExpression { return Func("MAX", arg) }

// RowNumber creates the ROW_NUMBER() window function.
func RowNumber() FuncExpression { return Func("ROW_NUMBER") }

// Rank creates the RANK() window function.
func Rank() FuncExpression { return Func("RANK") }

// DenseRank creates the DENSE_RANK() window function.
func DenseRank() FuncExpression { return Func("DENSE_RANK") }

// Lag creates the LAG(expr) window function.
func Lag(arg any) FuncExpression { return Func("LAG", arg) }

// Lead creates the LEAD(expr) window function.
func Lead(arg any) FuncExpression { return Func("LEAD", arg) }

// WhenClause is one WHEN ... THEN ... arm of a CASE expression.
type WhenClause struct {
	Condition Expression
	Result    Expression
}

// CaseExpression is a searched CASE expression:
// CASE WHEN cond THEN result [...] [ELSE result] END.
type CaseExpression struct {
	Whens     []WhenClause
	ElseValue Expression
}

func (CaseExpression) SQLExpression() {}
func (CaseExpression) SQLSelection()  {}

// Case starts a CASE expression; chain When and Else to build it.
func Case() CaseExpression { return CaseExpression{} }

// When appends a WHEN condition THEN result arm.
func (e CaseExpression) When(condition, result any) CaseExpression {
	e.Whens = append(append([]WhenClause(nil), e.Whens...),
		WhenClause{Condition: Expr(condition), Result: Expr(result)})
	return e
}

// Else sets the ELSE result.
func (e CaseExpression) Else(result any) CaseExpression {
	e.ElseValue = Expr(result)
	return e
}

// If creates the two-armed CASE expression
// CASE WHEN condition THEN then ELSE els END, like SQL IF().
func If(condition, then, els any) CaseExpression {
	return Case().When(condition, then).Else(els)
}

// WindowExpression applies a window (OVER clause) to a function or aggregate.
type WindowExpression struct {
	Fn           Expression
	Partitions   []Expression
	OrderByTerms []OrderTerm
	FrameClause  string
}

func (WindowExpression) SQLExpression() {}
func (WindowExpression) SQLSelection()  {}

// Over creates a window expression over a function or aggregate.
func Over(fn Expression) WindowExpression { return WindowExpression{Fn: fn} }

// PartitionBy adds PARTITION BY expressions to the window.
func (e WindowExpression) PartitionBy(exprs ...Expression) WindowExpression {
	e.Partitions = append(append([]Expression(nil), e.Partitions...), exprs...)
	return e
}

// OrderBy adds ORDER BY terms to the window.
func (e WindowExpression) OrderBy(terms ...OrderTerm) WindowExpression {
	e.OrderByTerms = append(append([]OrderTerm(nil), e.OrderByTerms...), terms...)
	return e
}

// Frame sets a raw frame clause such as "ROWS BETWEEN 1 PRECEDING AND CURRENT ROW".
func (e WindowExpression) Frame(frame string) WindowExpression {
	e.FrameClause = frame
	return e
}
