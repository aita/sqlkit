package sql

import "testing"

// countingVisitor records how many nodes Walk visits (ignoring the post-children
// nil call) and keeps walking the whole tree.
type countingVisitor struct{ n *int }

func (c countingVisitor) Visit(node Node) Visitor {
	if node == nil {
		return c
	}
	*c.n++
	return c
}

func TestWalk(t *testing.T) {
	users := Table{Name: NewIdent("users")}
	orders := Table{Name: NewIdent("orders")}
	posts := Table{Name: NewIdent("posts")}

	// A query with a FROM subquery, a correlated scalar subquery in the SELECT
	// list, and a WHERE IN subquery — four queries in total.
	fromSub := Select(Col("id")).From(users)
	scalarSub := Select(CountAll()).From(SourceAlias(orders, "o")).Where(Eq(Col("o.uid"), Col("u.id")))
	inSub := Select(Col("uid")).From(posts)
	root := Select(Col("u.id"), As(ScalarSubquery(scalarSub), "n")).
		From(Alias(fromSub, "u")).
		Where(InQuery(Col("u.id"), inSub))

	// Walk reaches every kind of node, not just queries: count the Query nodes
	// among them with Inspect.
	queries := 0
	Inspect(Unwrap(root).(Node), func(n Node) bool {
		if _, ok := n.(Query); ok {
			queries++
		}
		return true
	})
	if queries != 4 {
		t.Errorf("Walk visited %d queries, want 4 (root + 3 subqueries)", queries)
	}

	// Walk descends into expressions too: it reaches the column references the
	// query mentions (u.id appears in the SELECT list, the scalar subquery, and
	// the WHERE).
	var columns []string
	Inspect(Unwrap(root).(Node), func(n Node) bool {
		if c, ok := n.(ColumnExpression); ok {
			columns = append(columns, c.Name.String())
		}
		return true
	})
	if len(columns) == 0 {
		t.Errorf("Walk visited no column expressions, want several")
	}

	// Pruning: a visitor that returns nil at the root visits only the root.
	pruned := 0
	Walk(VisitorFunc(func(node Node) bool {
		if node == nil {
			return false
		}
		pruned++
		return false // prune every node's children
	}), Unwrap(root).(Node))
	if pruned != 1 {
		t.Errorf("pruning visitor visited %d nodes, want 1 (root only)", pruned)
	}

	// Walk descends into CTE bodies: a WITH query reaches the outer query and the
	// body it wraps.
	active := Select(Col("id")).From(users).CTE("active")
	withCTE := Select(active.Col("id")).From(active).WithCTE(active).Limit(5)
	cteQueries := 0
	Inspect(Unwrap(withCTE).(Node), func(n Node) bool {
		if _, ok := n.(Query); ok {
			cteQueries++
		}
		return true
	})
	if cteQueries != 2 {
		t.Errorf("Walk over a WITH query visited %d queries, want 2 (outer + CTE body)", cteQueries)
	}

	// nil is a safe no-op.
	n := 0
	Walk(countingVisitor{&n}, nil)
	if n != 0 {
		t.Errorf("Walk(v, nil) visited %d nodes, want 0", n)
	}
}

// TestDecompose checks the single-level structural view: a query's own CTE
// names and referenced table names, plus its immediate child queries (the CTE
// body and a WHERE subquery), without descending into those children.
func TestDecompose(t *testing.T) {
	active := Select(Col("id")).From(Table{Name: NewIdent("users")}).CTE("active")
	sub := Select(Col("uid")).From(Table{Name: NewIdent("posts")})
	q := Select(Col("a.id")).
		From(active).
		Where(InQuery(Col("a.id"), sub)).
		WithCTE(active)

	var cteNames, names []string
	var children int
	Decompose(q, func(s string) { cteNames = append(cteNames, s) },
		func(s string) { names = append(names, s) },
		func(Query) { children++ })

	if len(cteNames) != 1 || cteNames[0] != "active" {
		t.Errorf("CTE names = %v, want [active]", cteNames)
	}
	if len(names) != 1 || names[0] != "active" {
		t.Errorf("referenced names = %v, want [active] (the FROM reference)", names)
	}
	if children != 2 {
		t.Errorf("child queries = %d, want 2 (CTE body + WHERE subquery)", children)
	}

	// Decompose stays one level deep: neither child's own tables leak out.
	for _, n := range names {
		if n == "users" || n == "posts" {
			t.Errorf("name %q leaked from a child query; Decompose should not descend", n)
		}
	}
}

// VisitorFunc adapts a func to a Visitor: the func decides whether to descend
// into a node's children (true) or prune them (false). It is the test's small
// stand-in for a hand-written Visitor.
type VisitorFunc func(Node) bool

func (f VisitorFunc) Visit(node Node) Visitor {
	if f(node) {
		return f
	}
	return nil
}

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
