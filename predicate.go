package sqlkit

import "github.com/aita/sqlkit/sql"

// Predicate is a boolean SQL expression usable in WHERE, HAVING, and JOIN ON
// clauses. Predicates are built from typed columns, so a type mismatch is a
// compile error rather than a runtime SQL error.
type Predicate struct {
	expr sql.Expression
	// batch is set on a column IN predicate, carrying its value list so a
	// builder can split it across statements when the bind-parameter limit would
	// otherwise be exceeded. It is ignored once the predicate is combined with
	// And/Or/Not, which clear it.
	batch *batchIn
}

func newPredicate(expr sql.Expression) Predicate { return Predicate{expr: expr} }

// batchIn carries the pieces of a positive column IN needed to rebuild it for an
// arbitrary subset of its values: the left-hand column and the full value list.
// Only positive IN is splittable; NOT IN cannot be partitioned across
// statements, so it never produces a batchIn.
type batchIn struct {
	col    sql.Expression
	values []any
}

// exprFor renders the IN expression for the given subset of values.
func (b *batchIn) exprFor(values []any) sql.Expression {
	return sql.In(b.col, values...)
}

// Expression returns the underlying untyped SQL expression.
func (p Predicate) Expression() sql.Expression { return p.expr }

// And combines this predicate with others using AND.
func (p Predicate) And(others ...Predicate) Predicate {
	operands := make([]sql.Expression, 0, len(others)+1)
	operands = append(operands, p.expr)
	for _, other := range others {
		operands = append(operands, other.expr)
	}
	return newPredicate(sql.And(operands...))
}

// Or combines this predicate with others using OR.
func (p Predicate) Or(others ...Predicate) Predicate {
	operands := make([]sql.Expression, 0, len(others)+1)
	operands = append(operands, p.expr)
	for _, other := range others {
		operands = append(operands, other.expr)
	}
	return newPredicate(sql.Or(operands...))
}

// And combines predicates with AND.
func And(predicates ...Predicate) Predicate {
	operands := make([]sql.Expression, len(predicates))
	for i, predicate := range predicates {
		operands[i] = predicate.expr
	}
	return newPredicate(sql.And(operands...))
}

// Or combines predicates with OR.
func Or(predicates ...Predicate) Predicate {
	operands := make([]sql.Expression, len(predicates))
	for i, predicate := range predicates {
		operands[i] = predicate.expr
	}
	return newPredicate(sql.Or(operands...))
}

// Not negates a predicate.
func Not(predicate Predicate) Predicate {
	return newPredicate(sql.Not(predicate.expr))
}

func combineWhere(predicates []Predicate) sql.Expression {
	if len(predicates) == 0 {
		return nil
	}
	return And(predicates...).expr
}

// resolveWhere folds a Where call's predicates while keeping a single splittable
// IN deferred so it can later be chunked across statements. current is the batch
// a builder already holds back (or nil).
//
// A lone batchable IN added when nothing is deferred becomes the new deferred
// batch (folding nothing now). Otherwise everything folds normally: flush is the
// previously deferred batch, which must be ANDed in first to keep its original
// WHERE position (and is no longer splittable), and fold is the new predicates'
// combined expression. The builder ANDs flush then fold into its query, in that
// order, and stores deferred.
func resolveWhere(predicates []Predicate, current *batchIn) (flush, fold sql.Expression, deferred *batchIn) {
	if current == nil && len(predicates) == 1 && predicates[0].batch != nil {
		return nil, nil, predicates[0].batch
	}
	if current != nil {
		flush = current.exprFor(current.values)
	}
	return flush, combineWhere(predicates), nil
}

// Exists creates an "EXISTS (subquery)" predicate, true when the subquery
// returns at least one row — the typed way to express a semi-join:
//
//	db.Select().From(Users).Where(sqlkit.Exists(
//		sqlkit.Select(Orders.ID).From(Orders).Where(Orders.UserID.EqExpr(Users.ID)),
//	))
func Exists(subquery Query) Predicate { return newPredicate(sql.Exists(unwrapQuery(subquery))) }

// NotExists creates a "NOT EXISTS (subquery)" predicate.
func NotExists(subquery Query) Predicate {
	return newPredicate(sql.NotExists(unwrapQuery(subquery)))
}

// Cond wraps a raw SQL expression as a predicate. It is the escape hatch for
// conditions the typed layer does not cover, such as CASE expressions or
// window function comparisons built with package sql.
func Cond(expr sql.Expression) Predicate { return newPredicate(expr) }
