// Package sql contains SQL query builders, expression nodes, and the
// compiler that turns them into SQL text plus bind arguments.
//
// This package is the untyped core of sqlkit. Most applications should use
// the typed builders in package sqlkit (backed by code generated with
// sqlkit), which compile down to the nodes defined here.
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
func (BinaryExpression) SQLSelection()  {}

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

// Add creates an "a + b" arithmetic expression. Non-expression operands become
// bind values.
func Add(left, right any) BinaryExpression { return binary(left, "+", right) }

// Sub creates an "a - b" arithmetic expression.
func Sub(left, right any) BinaryExpression { return binary(left, "-", right) }

// Mul creates an "a * b" arithmetic expression.
func Mul(left, right any) BinaryExpression { return binary(left, "*", right) }

// Div creates an "a / b" arithmetic expression.
func Div(left, right any) BinaryExpression { return binary(left, "/", right) }

// Mod creates an "a % b" arithmetic expression.
func Mod(left, right any) BinaryExpression { return binary(left, "%", right) }

// Like creates a LIKE expression.
func Like(left, pattern any) BinaryExpression { return binary(left, "LIKE", pattern) }

// NotLike creates a NOT LIKE expression.
func NotLike(left, pattern any) BinaryExpression { return binary(left, "NOT LIKE", pattern) }

func binary(left any, operator string, right any) BinaryExpression {
	return BinaryExpression{Left: Expr(left), Operator: operator, Right: Expr(right)}
}

// ConcatExpression concatenates strings. It renders per dialect: the standard
// || operator on PostgreSQL, and CONCAT(...) on MySQL, whose || means logical
// OR instead.
type ConcatExpression struct {
	Operands []Expression
}

func (ConcatExpression) SQLExpression() {}
func (ConcatExpression) SQLSelection()  {}

// Concat creates a string concatenation expression. Non-expression operands
// become bind values.
func Concat(operands ...any) ConcatExpression {
	expressions := make([]Expression, len(operands))
	for i, operand := range operands {
		expressions[i] = Expr(operand)
	}
	return ConcatExpression{Operands: expressions}
}

// ILikeExpression is a case-insensitive LIKE. It renders per dialect: native
// ILIKE on PostgreSQL, and the portable LOWER(left) LIKE LOWER(pattern) on
// engines without an ILIKE operator (MySQL).
type ILikeExpression struct {
	Left    Expression
	Pattern Expression
}

func (ILikeExpression) SQLExpression() {}

// ILike creates a case-insensitive LIKE expression. Non-expression operands
// become bind values.
func ILike(left, pattern any) ILikeExpression {
	return ILikeExpression{Left: Expr(left), Pattern: Expr(pattern)}
}

// BetweenExpression tests whether an operand falls within an inclusive range.
type BetweenExpression struct {
	Operand Expression
	Low     Expression
	High    Expression
	Negate  bool
}

func (BetweenExpression) SQLExpression() {}

// Between creates an "operand BETWEEN low AND high" expression (inclusive).
// Non-expression bounds become bind values.
func Between(operand, low, high any) BetweenExpression {
	return BetweenExpression{Operand: Expr(operand), Low: Expr(low), High: Expr(high)}
}

// NotBetween creates an "operand NOT BETWEEN low AND high" expression.
func NotBetween(operand, low, high any) BetweenExpression {
	expr := Between(operand, low, high)
	expr.Negate = true
	return expr
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

// InExpression tests membership in a list of expressions or, when Subquery is
// set, in the rows a subquery returns.
type InExpression struct {
	Left     Expression
	Items    []Expression
	Subquery Query
	Negate   bool
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

// InQuery creates a "left IN (subquery)" expression. The subquery must project
// a single column.
func InQuery(left any, subquery Query) InExpression {
	return InExpression{Left: Expr(left), Subquery: subquery}
}

// NotInQuery creates a "left NOT IN (subquery)" expression.
func NotInQuery(left any, subquery Query) InExpression {
	expr := InQuery(left, subquery)
	expr.Negate = true
	return expr
}

// ExistsExpression tests whether a subquery returns any rows.
type ExistsExpression struct {
	Query  Query
	Negate bool
}

func (ExistsExpression) SQLExpression() {}

// Exists creates an "EXISTS (subquery)" expression.
func Exists(query Query) ExistsExpression { return ExistsExpression{Query: query} }

// NotExists creates a "NOT EXISTS (subquery)" expression.
func NotExists(query Query) ExistsExpression { return ExistsExpression{Query: query, Negate: true} }

// ScalarSubqueryExpression wraps a subquery so it can stand in for a single
// value — in a SELECT-list projection, a comparison, or anywhere else an
// Expression is expected. The subquery must project a single column and return
// at most one row; it may be correlated, referencing columns of the enclosing
// query. It renders as the parenthesized subquery and shares the outer
// statement's bind placeholder numbering.
type ScalarSubqueryExpression struct{ Query Query }

func (ScalarSubqueryExpression) SQLExpression() {}
func (ScalarSubqueryExpression) SQLSelection()  {}

// ScalarSubquery wraps a subquery as a scalar expression, the form behind a
// correlated scalar subquery such as
// SELECT u.id, (SELECT COUNT(*) FROM orders o WHERE o.user_id = u.id) FROM users u.
func ScalarSubquery(query Query) ScalarSubqueryExpression {
	return ScalarSubqueryExpression{Query: query}
}

// QuantifiedExpression compares an operand against the rows of a subquery with a
// quantifier — ANY (a synonym for SOME) or ALL — as in "x > ALL (subquery)". It
// generalizes IN/NOT IN, which are "= ANY"/"<> ALL", to the ordering operators.
type QuantifiedExpression struct {
	Left       Expression
	Operator   string // comparison operator, e.g. "=", ">", "<>"
	Quantifier string // "ANY" or "ALL"
	Query      Query
}

func (QuantifiedExpression) SQLExpression() {}

// Any creates a "left <operator> ANY (subquery)" expression: true when the
// comparison holds for at least one row the subquery returns. A non-expression
// left operand becomes a bind value.
func Any(left any, operator string, query Query) QuantifiedExpression {
	return QuantifiedExpression{Left: Expr(left), Operator: operator, Quantifier: "ANY", Query: query}
}

// All creates a "left <operator> ALL (subquery)" expression: true when the
// comparison holds for every row the subquery returns (and, vacuously, when it
// returns none). A non-expression left operand becomes a bind value.
func All(left any, operator string, query Query) QuantifiedExpression {
	return QuantifiedExpression{Left: Expr(left), Operator: operator, Quantifier: "ALL", Query: query}
}

// ExcludedExpression refers, inside an upsert's DO UPDATE assignments, to the
// value the conflicting INSERT proposed for a column. It renders per dialect:
// PostgreSQL's excluded.<col> and MySQL's VALUES(<col>).
type ExcludedExpression struct{ Column string }

func (ExcludedExpression) SQLExpression() {}

// Excluded references the proposed value for column in an OnConflictDoUpdate
// assignment, e.g. Set("name", Excluded("name")) to overwrite with the new row.
func Excluded(column string) ExcludedExpression { return ExcludedExpression{Column: column} }

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
	Expr  Expression
	Desc  bool
	Nulls string // "", "FIRST", or "LAST" (PostgreSQL NULLS ordering)
}

// Asc creates an ascending ORDER BY term.
func Asc(expr Expression) OrderTerm { return OrderTerm{Expr: expr} }

// Desc creates a descending ORDER BY term.
func Desc(expr Expression) OrderTerm { return OrderTerm{Expr: expr, Desc: true} }

// NullsFirst orders NULLs before non-NULLs (PostgreSQL NULLS FIRST). It is a
// PostgreSQL extension; MySQL does not accept the NULLS clause.
func (t OrderTerm) NullsFirst() OrderTerm { t.Nulls = "FIRST"; return t }

// NullsLast orders NULLs after non-NULLs (PostgreSQL NULLS LAST).
func (t OrderTerm) NullsLast() OrderTerm { t.Nulls = "LAST"; return t }

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
