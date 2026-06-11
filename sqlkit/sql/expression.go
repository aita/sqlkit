// Package sql contains SQL query builders and expression nodes.
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
// bind values so callers can write Eq(Users.ID, 123).
func Eq(left, right any) BinaryExpression {
	return BinaryExpression{Left: Expr(left), Operator: "=", Right: Expr(right)}
}

// CountExpression represents COUNT(expr) or COUNT(*).
type CountExpression struct {
	Target Selection
	All    bool
}

func (CountExpression) SQLSelection() {}

// Count creates a COUNT(selection) aggregate.
func Count(target Selection) CountExpression { return CountExpression{Target: target} }

// CountAll creates a COUNT(*) aggregate.
func CountAll() CountExpression { return CountExpression{All: true} }

// Expr converts common values into AST expressions.
func Expr(value any) Expression {
	if expr, ok := value.(Expression); ok {
		return expr
	}
	return Val(value)
}
