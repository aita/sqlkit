package sql

// GroupingExpression is a ROLLUP or CUBE grouping element in a GROUP BY clause,
// e.g. ROLLUP (a, b). Kind is "ROLLUP" or "CUBE".
type GroupingExpression struct {
	posField
	Kind     string
	Elements []Expression
}

func (GroupingExpression) SQLExpression() {}

// Rollup builds a ROLLUP (elements...) grouping element.
func Rollup(elements ...Expression) GroupingExpression {
	return GroupingExpression{Kind: "ROLLUP", Elements: elements}
}

// Cube builds a CUBE (elements...) grouping element.
func Cube(elements ...Expression) GroupingExpression {
	return GroupingExpression{Kind: "CUBE", Elements: elements}
}

// GroupingSetsExpression is a GROUPING SETS (...) grouping element. Each set is a
// list of expressions; an empty (nil) set renders as (), the grand total. At least
// one set is required: zero sets would render the invalid GROUPING SETS ().
type GroupingSetsExpression struct {
	posField
	Sets [][]Expression
}

func (GroupingSetsExpression) SQLExpression() {}

// GroupingSets builds a GROUPING SETS element from the given sets.
func GroupingSets(sets ...[]Expression) GroupingSetsExpression {
	return GroupingSetsExpression{Sets: sets}
}
