package sql

// SubscriptExpression is an array subscript or slice applied to an operand, e.g.
// a[1], a[1:3], or a chain a[1][2]. Each SubscriptIndex is one [...] step.
type SubscriptExpression struct {
	posField
	Operand Expression
	Indices []SubscriptIndex
}

// SubscriptIndex is one subscript step. A non-slice index uses Upper as the
// single index (a[Upper]); a slice uses Lower:Upper (a[Lower:Upper]), either of
// which may be nil for an open bound.
type SubscriptIndex struct {
	Slice bool
	Lower Expression
	Upper Expression
}

func (SubscriptExpression) SQLExpression() {}
func (SubscriptExpression) SQLSelection()  {}

// Subscript builds a single array-index expression operand[index].
func Subscript(operand Expression, index any) SubscriptExpression {
	return SubscriptExpression{Operand: operand, Indices: []SubscriptIndex{{Upper: Expr(index)}}}
}

// Slice builds an array-slice expression operand[lower:upper]; pass nil for an
// open bound.
func Slice(operand Expression, lower, upper Expression) SubscriptExpression {
	return SubscriptExpression{Operand: operand, Indices: []SubscriptIndex{{Slice: true, Lower: lower, Upper: upper}}}
}

// FieldExpression selects a field of a composite-typed operand, rendered
// parenthesized as (operand).field.
type FieldExpression struct {
	posField
	Operand Expression
	Field   string
}

func (FieldExpression) SQLExpression() {}
func (FieldExpression) SQLSelection()  {}

// Field builds a composite field access (operand).field.
func Field(operand Expression, field string) FieldExpression {
	return FieldExpression{Operand: operand, Field: field}
}

// CollateExpression applies a collation to an operand (operand COLLATE "name").
type CollateExpression struct {
	posField
	Operand   Expression
	Collation string
}

func (CollateExpression) SQLExpression() {}
func (CollateExpression) SQLSelection()  {}

// Collate builds a COLLATE expression operand COLLATE collation.
func Collate(operand Expression, collation string) CollateExpression {
	return CollateExpression{Operand: operand, Collation: collation}
}
