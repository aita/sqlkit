package sql

import "testing"

// TestWalkCoversEveryNodeKind drives walkChildren's dispatch arm for every node
// type. A rich SELECT covers the structural arms and their loop bodies (joins,
// group by, having, order by, window, CTE); a projection of every expression
// kind (each wrapped in an aliased selection) covers the expression arms via
// walkSelection -> walkExpr; and zero-value statements cover the remaining arms
// (Walk dispatches on the dynamic type, then walks the node's empty children).
func TestWalkCoversEveryNodeKind(t *testing.T) {
	count := 0
	v := countingVisitor{&count}
	walk := func(n Node) { Walk(v, n) }

	cte := Select(Col("id")).From(orgs).CTE("c")
	rich := Select(Col("a"), As(Over(Func("rank")).Named("w"), "rn")).
		From(SourceAlias(orgs, "o")).
		Join(users, Eq(Col("o.id"), Col("u.id"))).
		WithCTE(cte).
		Where(And(Gt(Col("a"), Val(1)), In(Col("a"), 1, 2))).
		GroupBy(Col("a")).
		Having(Gt(CountAll(), Val(0))).
		Window(WindowDef("w").PartitionBy(Col("a")).OrderBy(Asc(Col("b")))).
		OrderBy(Asc(Col("a")), Desc(Col("b")))
	walk(Unwrap(rich).(Node))

	// Writes: conflict clause, assignments, USING/FROM sources.
	walk(Unwrap(InsertInto(users).Columns("a").Values(1).OnConflict("a").DoUpdate(Set("a", Excluded("a")))).(Node))
	walk(Unwrap(Update(users).Set(Set("a", Val(1))).From(SourceAlias(orgs, "o")).Where(Eq(Col("a"), Val(1)))).(Node))
	walk(Unwrap(DeleteFrom(users).Using(orgs).Where(Eq(Col("a"), Val(1)))).(Node))

	// Every expression kind, each wrapped in an aliased selection so
	// walkSelection -> walkExpr reaches its arm. Zero values suffice for the
	// arms whose bodies are simple; richer ones get children for the loop paths.
	exprs := []Expression{
		BinaryExpression{Left: Col("a"), Operator: "=", Right: Col("b")},
		UnaryExpression{Operator: "-", Operand: Col("a")},
		LogicalExpression{Operator: "AND", Operands: []Expression{Col("a"), Col("b")}},
		NotExpression{Operand: Col("a")},
		NullPredicate{Operand: Col("a")},
		DistinctPredicate{Left: Col("a"), Right: Col("b")},
		BetweenExpression{Operand: Col("a"), Low: Col("b"), High: Col("c")},
		ConcatExpression{Operands: []Expression{Col("a"), Col("b")}},
		ILikeExpression{},
		RegexpExpression{},
		SimilarToExpression{},
		RowExpression{Elements: []Expression{Col("a")}},
		Array(Val(1), Val(2)),
		AnyArray(Col("a"), "=", Array(Val(1))),
		GroupingExpression{Kind: "ROLLUP", Elements: []Expression{Col("a")}},
		GroupingSetsExpression{Sets: [][]Expression{{Col("a")}}},
		Case().When(Col("a"), Col("b")).Else(Col("c")),
		Over(Func("rank")).PartitionBy(Col("a")).OrderBy(Asc(Col("b"))),
		Func("f", Col("a")),
		CountAll(),
		Count(Col("a")),
		StringAggExpression{Arg: Col("a")},
		ExtractExpression{Field: "YEAR", Operand: Col("a")},
		PositionExpression{Substring: Col("a"), String: Col("b")},
		CastExpression{Type: "int", Operand: Col("a")},
		CollateExpression{Operand: Col("a"), Collation: NewIdent("C")},
		FieldExpression{Operand: Col("a"), Field: NewIdent("f")},
		SubscriptExpression{Operand: Col("a"), Indices: []SubscriptIndex{{Upper: Val(1)}, {Slice: true, Lower: Val(1), Upper: Val(2)}}},
		JSONObjectExpression{Pairs: []Expression{Val("k"), Val("v")}},
		JSONGet(Col("a"), "k"),
		In(Col("a"), 1, 2),
		Exists(Select(Col("id")).From(orgs)),
		ScalarSubquery(Select(Col("id")).From(orgs)),
		QuantifiedExpression{Left: Col("a"), Operator: "=", Quantifier: "ANY", Query: Select(Col("id")).From(orgs)},
		MatchExpression{},
	}
	// Pack every expression as arguments of one function call in a WHERE, so the
	// SELECT arm walks the predicate, the FuncExpression arm walks its args, and
	// walkExpr reaches each expression's arm.
	args := make([]any, len(exprs))
	for i, e := range exprs {
		args[i] = e
	}
	walk(Unwrap(Select(Col("a")).From(orgs).Where(Func("noop", args...))).(Node))

	// Remaining statement arms via zero-value nodes.
	for _, n := range []Node{
		CompoundStmt{}, MergeStmt{}, ValuesQuery{}, CreateTableStmt{}, AlterTableStmt{},
		CreateViewStmt{}, CreateMaterializedViewStmt{}, CreateIndexStmt{}, DropTableStmt{},
		TruncateStmt{}, SetStmt{}, LockTablesStmt{},
	} {
		walk(n)
	}

	if count == 0 {
		t.Fatal("Walk visited no nodes")
	}

	// Scoped public helpers: subquery- and expression-collectors over a single
	// expression / selection scope (they must not descend into nested queries).
	exprWithSub := Exists(Select(Col("id")).From(orgs))
	WalkSubqueries(exprWithSub, func(Query) {})
	WalkExpr(exprWithSub, func(Expression) {})
	selWithSub := As(ScalarSubquery(Select(Col("id")).From(orgs)), "n")
	WalkSelectionSubqueries(selWithSub, func(Query) {})
	WalkSelectionExpr(selWithSub, func(Expression) {})

	// Decompose with nil callbacks exercises the orNoop substitution.
	Decompose(Unwrap(rich), nil, nil, nil)
}
