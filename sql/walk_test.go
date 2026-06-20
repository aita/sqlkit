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
