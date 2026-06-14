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

// StarSelection is a "*" select-list item, optionally qualified by a table name
// or alias to project every column of one source ("users.*"). Unlike a column
// reference the qualifier is quoted but the "*" is emitted raw, so it never
// becomes "users"."*". It is only a Selection, not an Expression: a "*" stands
// alone in the select list and is not a value operand.
type StarSelection struct{ Qualifier string }

func (StarSelection) SQLSelection() {}

// Star creates an unqualified "*" select-list item, projecting every column of
// the query's sources.
func Star() StarSelection { return StarSelection{} }

// StarOf creates a qualified "qualifier.*" select-list item, projecting every
// column of the table or alias named by qualifier — for selecting one source
// whole alongside individual columns of others (SELECT users.*, profiles.bio).
func StarOf(qualifier string) StarSelection { return StarSelection{Qualifier: qualifier} }

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

// UnaryExpression applies a prefix unary operator to an operand, such as the
// arithmetic negation -x or unary plus +x. It renders as the operator
// immediately followed by the parenthesized operand, e.g. (-"x").
type UnaryExpression struct {
	Operator string
	Operand  Expression
}

func (UnaryExpression) SQLExpression() {}
func (UnaryExpression) SQLSelection()  {}

// Unary creates a prefix unary operator expression. A non-expression operand
// becomes a bind value.
func Unary(operator string, operand any) UnaryExpression {
	return UnaryExpression{Operator: operator, Operand: Expr(operand)}
}

// Neg creates a "-operand" arithmetic negation. A non-expression operand becomes
// a bind value.
func Neg(operand any) UnaryExpression { return Unary("-", operand) }

// Pos creates a "+operand" unary plus. A non-expression operand becomes a bind
// value.
func Pos(operand any) UnaryExpression { return Unary("+", operand) }

// RowExpression is a row value constructor: a parenthesized list of expressions
// such as (a, b). It backs row-wise comparisons ((a, b) = (c, d)) and row-wise
// membership ((a, b) IN ((1, 2), (3, 4))), where it can stand on either side of
// the operator and as each IN item.
type RowExpression struct{ Elements []Expression }

func (RowExpression) SQLExpression() {}

// Row creates a row value constructor (a, b, ...). Non-expression elements
// become bind values.
func Row(elements ...any) RowExpression {
	exprs := make([]Expression, len(elements))
	for i, element := range elements {
		exprs[i] = Expr(element)
	}
	return RowExpression{Elements: exprs}
}

// DistinctPredicate compares two operands with IS DISTINCT FROM / IS NOT DISTINCT
// FROM, the null-safe counterparts of <> and =: two NULLs are not distinct, and a
// NULL is distinct from any non-NULL value, so the result is never itself NULL.
// It renders per dialect: the SQL-standard IS [NOT] DISTINCT FROM (PostgreSQL,
// SQLite, the standard target) and, through a NullSafeEqualityDialect, MySQL's
// null-safe equality operator <=> (a <=> b for IS NOT DISTINCT FROM, NOT (a <=>
// b) for IS DISTINCT FROM).
type DistinctPredicate struct {
	Left   Expression
	Right  Expression
	Negate bool // true selects IS NOT DISTINCT FROM
}

func (DistinctPredicate) SQLExpression() {}

// IsDistinctFrom creates "left IS DISTINCT FROM right", a null-safe inequality.
// Non-expression operands become bind values.
func IsDistinctFrom(left, right any) DistinctPredicate {
	return DistinctPredicate{Left: Expr(left), Right: Expr(right)}
}

// IsNotDistinctFrom creates "left IS NOT DISTINCT FROM right", a null-safe
// equality. Non-expression operands become bind values.
func IsNotDistinctFrom(left, right any) DistinctPredicate {
	return DistinctPredicate{Left: Expr(left), Right: Expr(right), Negate: true}
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

// RegexpExpression is a regular-expression match. Regex syntax and operators
// are genuinely non-portable, so it renders per dialect through a RegexpMatcher
// (PostgreSQL ~ / ~* / !~ / !~*, MySQL and SQLite REGEXP / NOT REGEXP, the SQL
// standard LIKE_REGEX [FLAG 'i']); a dialect without that capability rejects it
// at compile time. CaseInsensitive selects a case-insensitive match where the
// dialect can express one inline (it errors on dialects that cannot).
type RegexpExpression struct {
	Left            Expression
	Pattern         Expression
	Negate          bool
	CaseInsensitive bool
}

func (RegexpExpression) SQLExpression() {}

// Regexp creates a "left ~ pattern" regular-expression match. Non-expression
// operands become bind values.
func Regexp(left, pattern any) RegexpExpression {
	return RegexpExpression{Left: Expr(left), Pattern: Expr(pattern)}
}

// NotRegexp creates a negated regular-expression match.
func NotRegexp(left, pattern any) RegexpExpression {
	e := Regexp(left, pattern)
	e.Negate = true
	return e
}

// IRegexp creates a case-insensitive regular-expression match.
func IRegexp(left, pattern any) RegexpExpression {
	e := Regexp(left, pattern)
	e.CaseInsensitive = true
	return e
}

// NotIRegexp creates a negated case-insensitive regular-expression match.
func NotIRegexp(left, pattern any) RegexpExpression {
	e := Regexp(left, pattern)
	e.Negate = true
	e.CaseInsensitive = true
	return e
}

// SimilarToExpression matches a string against a SQL regular expression with the
// SQL-standard SIMILAR TO operator, whose pattern language is LIKE's (% and _)
// extended with the regex metacharacters | * + ? () [] {}. It is genuinely
// non-portable — PostgreSQL and the SQL standard have it, MySQL and SQLite do
// not — so it renders through a SimilarToMatcher and a dialect without that
// capability rejects it at compile time.
type SimilarToExpression struct {
	Left    Expression
	Pattern Expression
	Negate  bool
}

func (SimilarToExpression) SQLExpression() {}

// SimilarTo creates a "left SIMILAR TO pattern" match. Non-expression operands
// become bind values.
func SimilarTo(left, pattern any) SimilarToExpression {
	return SimilarToExpression{Left: Expr(left), Pattern: Expr(pattern)}
}

// NotSimilarTo creates a negated "left NOT SIMILAR TO pattern" match.
func NotSimilarTo(left, pattern any) SimilarToExpression {
	e := SimilarTo(left, pattern)
	e.Negate = true
	return e
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

// ArrayExpression is an ARRAY[...] constructor. Arrays are non-portable —
// PostgreSQL has a first-class array type, MySQL and SQLite do not — so it
// renders through an ArrayDialect and a dialect without that capability rejects
// it at compile time.
type ArrayExpression struct{ Elements []Expression }

func (ArrayExpression) SQLExpression() {}
func (ArrayExpression) SQLSelection()  {}

// Array creates an ARRAY[...] constructor. Non-expression elements become bind
// values.
func Array(elements ...any) ArrayExpression {
	exprs := make([]Expression, len(elements))
	for i, element := range elements {
		exprs[i] = Expr(element)
	}
	return ArrayExpression{Elements: exprs}
}

// QuantifiedArrayExpression compares an operand against the elements of an array
// with a quantifier — ANY (a synonym for SOME) or ALL — as in "x = ANY(array)".
// Unlike QuantifiedExpression, which quantifies over a subquery's rows, this
// quantifies over an array value: an ARRAY[...] constructor or a single bound
// array parameter. "x = ANY($1)" is the idiomatic PostgreSQL form of "x IN
// (...)" that binds the whole list as one parameter, and "x <> ALL($1)" of "x
// NOT IN (...)". Arrays are non-portable, so it is gated by ArrayDialect.
type QuantifiedArrayExpression struct {
	Left       Expression
	Operator   string // comparison operator, e.g. "=", ">", "<>"
	Quantifier string // "ANY" or "ALL"
	Array      Expression
}

func (QuantifiedArrayExpression) SQLExpression() {}

// AnyArray creates a "left <operator> ANY(array)" expression: true when the
// comparison holds for at least one array element. A non-expression left operand
// becomes a bind value; array is an ArrayExpression or a bound array value.
func AnyArray(left any, operator string, array Expression) QuantifiedArrayExpression {
	return QuantifiedArrayExpression{Left: Expr(left), Operator: operator, Quantifier: "ANY", Array: array}
}

// AllArray creates a "left <operator> ALL(array)" expression: true when the
// comparison holds for every array element (and, vacuously, for an empty array).
func AllArray(left any, operator string, array Expression) QuantifiedArrayExpression {
	return QuantifiedArrayExpression{Left: Expr(left), Operator: operator, Quantifier: "ALL", Array: array}
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

// Excluded references the proposed value for column in an OnConflict DoUpdate
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

// CaseExpression is a CASE expression in either form. With Operand nil it is a
// searched CASE — CASE WHEN cond THEN result [...] [ELSE result] END — whose When
// arms hold boolean conditions. With Operand set it is a simple CASE — CASE
// operand WHEN value THEN result [...] [ELSE result] END — whose When arms hold
// values compared for equality against the operand.
type CaseExpression struct {
	Operand   Expression
	Whens     []WhenClause
	ElseValue Expression
}

func (CaseExpression) SQLExpression() {}
func (CaseExpression) SQLSelection()  {}

// Case starts a searched CASE expression; chain When and Else to build it.
func Case() CaseExpression { return CaseExpression{} }

// CaseOf starts a simple CASE expression that compares operand for equality
// against each When value: CASE operand WHEN value THEN result ... END. A
// non-expression operand becomes a bind value.
func CaseOf(operand any) CaseExpression { return CaseExpression{Operand: Expr(operand)} }

// When appends a WHEN condition THEN result arm. In a simple CASE (CaseOf) the
// condition is the value compared against the operand.
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
// With Name set it references a window defined in the SELECT's WINDOW clause:
// "fn OVER name" when it adds no clauses of its own, or "fn OVER (name ...)"
// when it extends that base window with further ORDER BY / frame clauses.
type WindowExpression struct {
	Fn           Expression
	Name         string
	Partitions   []Expression
	OrderByTerms []OrderTerm
	FrameClause  string
}

func (WindowExpression) SQLExpression() {}
func (WindowExpression) SQLSelection()  {}

// Over creates a window expression over a function or aggregate.
func Over(fn Expression) WindowExpression { return WindowExpression{Fn: fn} }

// Named references a window defined in the SELECT's WINDOW clause by name, as in
// Sum(Col("x")).Over().Named("w") for "SUM(x) OVER w". Adding PartitionBy /
// OrderBy / Frame on top extends that base window (PostgreSQL "OVER (w ...)").
func (e WindowExpression) Named(name string) WindowExpression {
	e.Name = name
	return e
}

// NamedWindow defines a reusable window in a SELECT's WINDOW clause, referenced
// from an OVER clause by name (WindowExpression.Named). It renders as
// "name AS (PARTITION BY ... ORDER BY ... frame)".
type NamedWindow struct {
	Name         string
	Partitions   []Expression
	OrderByTerms []OrderTerm
	FrameClause  string
}

// WindowDef starts a named window definition for a SELECT's WINDOW clause; chain
// PartitionBy / OrderBy / Frame to build it.
func WindowDef(name string) NamedWindow { return NamedWindow{Name: name} }

// PartitionBy adds PARTITION BY expressions to the window definition.
func (w NamedWindow) PartitionBy(exprs ...Expression) NamedWindow {
	w.Partitions = append(append([]Expression(nil), w.Partitions...), exprs...)
	return w
}

// OrderBy adds ORDER BY terms to the window definition.
func (w NamedWindow) OrderBy(terms ...OrderTerm) NamedWindow {
	w.OrderByTerms = append(append([]OrderTerm(nil), w.OrderByTerms...), terms...)
	return w
}

// Frame sets a raw frame clause such as "ROWS BETWEEN 1 PRECEDING AND CURRENT ROW".
func (w NamedWindow) Frame(frame string) NamedWindow {
	w.FrameClause = frame
	return w
}

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
