package sql

import "testing"

func TestWalk(t *testing.T) {
	users := Table{Name: "users"}
	orders := Table{Name: "orders"}
	posts := Table{Name: "posts"}

	// A query with a FROM subquery, a correlated scalar subquery in the SELECT
	// list, and a WHERE IN subquery — four queries in total.
	fromSub := Select(Col("id")).From(users)
	scalarSub := Select(CountAll()).From(SourceAlias(orders, "o")).Where(Eq(Col("o.uid"), Col("u.id")))
	inSub := Select(Col("uid")).From(posts)
	root := Select(Col("u.id"), As(ScalarSubquery(scalarSub), "n")).
		From(Alias(fromSub, "u")).
		Where(InQuery(Col("u.id"), inSub))

	var visited int
	Walk(root, func(q Query) bool {
		visited++
		return true
	})
	if visited != 4 {
		t.Errorf("Walk visited %d queries, want 4 (root + 3 subqueries)", visited)
	}

	// Pruning: returning false at the root visits only the root.
	var pruned int
	Walk(root, func(q Query) bool {
		pruned++
		return false
	})
	if pruned != 1 {
		t.Errorf("Walk with pruning visited %d queries, want 1", pruned)
	}

	// Walk descends into CTE bodies: a WITH query visits the outer query and the
	// body it wraps.
	active := Select(Col("id")).From(users).CTE("active")
	withCTE := Select(active.Col("id")).From(active).WithCTE(active).Limit(5)
	var withCount int
	Walk(withCTE, func(q Query) bool {
		withCount++
		return true
	})
	if withCount != 2 {
		t.Errorf("Walk over a WITH query visited %d queries, want 2 (outer + CTE body)", withCount)
	}

	// nil is a safe no-op.
	Walk(nil, func(Query) bool {
		t.Errorf("Walk(nil) called visit")
		return true
	})
}
