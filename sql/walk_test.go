package sql_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestWalk(t *testing.T) {
	users := sql.Table{Name: "users"}
	orders := sql.Table{Name: "orders"}
	posts := sql.Table{Name: "posts"}

	// A query with a FROM subquery, a correlated scalar subquery in the SELECT
	// list, and a WHERE IN subquery — four queries in total.
	fromSub := sql.Select(sql.Col("id")).From(users)
	scalarSub := sql.Select(sql.CountAll()).From(sql.SourceAlias(orders, "o")).Where(sql.Eq(sql.Col("o.uid"), sql.Col("u.id")))
	inSub := sql.Select(sql.Col("uid")).From(posts)
	root := sql.Select(sql.Col("u.id"), sql.As(sql.ScalarSubquery(scalarSub), "n")).
		From(sql.Alias(fromSub, "u")).
		Where(sql.InQuery(sql.Col("u.id"), inSub))

	var visited int
	sql.Walk(root, func(q sql.Query) bool {
		visited++
		return true
	})
	if visited != 4 {
		t.Errorf("Walk visited %d queries, want 4 (root + 3 subqueries)", visited)
	}

	// Pruning: returning false at the root visits only the root.
	var pruned int
	sql.Walk(root, func(q sql.Query) bool {
		pruned++
		return false
	})
	if pruned != 1 {
		t.Errorf("Walk with pruning visited %d queries, want 1", pruned)
	}

	// Walk descends into CTE bodies: a WITH query visits the outer query and the
	// body it wraps.
	active := sql.Select(sql.Col("id")).From(users).CTE("active")
	withCTE := sql.Select(active.Col("id")).From(active).WithCTE(active).Limit(5)
	var withCount int
	sql.Walk(withCTE, func(q sql.Query) bool {
		withCount++
		return true
	})
	if withCount != 2 {
		t.Errorf("Walk over a WITH query visited %d queries, want 2 (outer + CTE body)", withCount)
	}

	// nil is a safe no-op.
	sql.Walk(nil, func(sql.Query) bool {
		t.Errorf("Walk(nil) called visit")
		return true
	})
}

func TestSelectStmtFields(t *testing.T) {
	users := sql.Table{Name: "users"}
	orders := sql.Table{Name: "orders"}

	// Build with the fluent builder, then read the pure AST directly — the
	// statement's exported fields are the public, zero-copy introspection seam.
	c := sql.Select(sql.Col("id")).
		From(users).
		Join(orders, sql.Eq(sql.Col("users.id"), sql.Col("orders.user_id"))).
		Where(sql.Eq(sql.Col("id"), sql.Val(1))).
		Limit(10).
		Stmt()

	if c.From == nil || c.From.SQLSource().Name != "users" {
		t.Errorf("From = %v, want users", c.From)
	}
	if len(c.Joins) != 1 {
		t.Fatalf("Joins = %d, want 1", len(c.Joins))
	}
	if c.Where == nil {
		t.Error("Where = nil, want non-nil")
	}
	if !c.HasLimit || c.Limit != 10 {
		t.Errorf("Limit = %d (has=%v), want 10", c.Limit, c.HasLimit)
	}
	if c.HasOffset {
		t.Errorf("HasOffset = true, want false")
	}
}

func TestInsertStmtConflict(t *testing.T) {
	users := sql.Table{Name: "users"}
	c := sql.InsertInto(users).Columns("id").Values(1).
		OnConflict("id").DoUpdate(sql.Set("id", sql.Val(2))).
		Stmt()

	if c.Table.Name != "users" {
		t.Errorf("Table = %q, want users", c.Table.Name)
	}
	if c.Conflict == nil || !c.Conflict.DoUpdate || len(c.Conflict.Sets) != 1 {
		t.Errorf("Conflict = %+v, want a DO UPDATE with one assignment", c.Conflict)
	}
}

func TestCTEAccessors(t *testing.T) {
	users := sql.Table{Name: "users"}
	cte := sql.Select(sql.Col("id")).From(users).CTE("active", sql.Columns("id"))
	if cte.Name() != "active" {
		t.Errorf("Name() = %q, want active", cte.Name())
	}
	if cte.Body() == nil {
		t.Error("Body() = nil, want the wrapped query")
	}
	if got := cte.OutputColumns(); !reflect.DeepEqual(got, []string{"id"}) {
		t.Errorf("OutputColumns() = %v, want [id]", got)
	}
	if cte.IsRecursive() {
		t.Error("IsRecursive() = true, want false")
	}
}

func TestDDLParts(t *testing.T) {
	users := sql.Table{Name: "users"}
	orders := sql.Table{Name: "orders"}

	alter := sql.AlterTable(users).DropColumn("x").RenameColumn("a", "b").Parts()
	if alter.Table.Name != "users" {
		t.Errorf("AlterTable Table = %q, want users", alter.Table.Name)
	}
	if len(alter.Alterations) != 2 {
		t.Fatalf("Alterations = %d, want 2", len(alter.Alterations))
	}
	if alter.Alterations[0].Kind != sql.AlterDropColumn || alter.Alterations[0].Name != "x" {
		t.Errorf("first alteration = %+v, want DROP COLUMN x", alter.Alterations[0])
	}
	if a := alter.Alterations[1]; a.Kind != sql.AlterRenameColumn || a.Name != "a" || a.NewName != "b" {
		t.Errorf("second alteration = %+v, want RENAME COLUMN a -> b", a)
	}

	dropped := sql.DropTable(users, orders).Parts().Tables
	if len(dropped) != 2 || dropped[0].Name != "users" || dropped[1].Name != "orders" {
		t.Errorf("DropTable Tables = %v, want [users orders]", dropped)
	}

	created := sql.CreateTable(users).
		Column(sql.ColumnDef{Name: "id", Type: sql.RawType("int")}).
		PrimaryKey("id").Parts()
	if created.Table.Name != "users" || len(created.Columns) != 1 || len(created.PrimaryKey) != 1 {
		t.Errorf("CreateTable parts = %+v, want one column and a primary key", created)
	}
}
