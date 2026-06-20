package analyze_test

import (
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

func TestHasWhere(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	tests := []struct {
		name  string
		query sql.Query
		want  bool
	}{
		{"select with where", sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))), true},
		{"select without where", sql.Select(sql.Col("id")).From(users), false},
		{"update with where", sql.Update(users).Set(sql.Set("name", sql.Expr("a"))).Where(sql.Eq(sql.Col("id"), sql.Expr(1))), true},
		{"delete without where", sql.DeleteFrom(users), false},
		{"insert has no where", sql.InsertInto(users).Columns("name").Values("a"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analyze.HasWhere(tt.query); got != tt.want {
				t.Errorf("HasWhere = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHasWhereIsNodeLocal pins that HasWhere describes only the top-level node: a
// WHERE inside a CTE body or a UNION arm is that nested query's, not the outer's.
func TestHasWhereIsNodeLocal(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	body := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("active"), sql.Val(true)))
	cte := body.CTE("c")
	withQuery := sql.Select(cte.Col("id")).From(cte).WithCTE(cte)
	if analyze.HasWhere(withQuery) {
		t.Error("CTE outer: HasWhere = true, want false (the WHERE is the CTE body's)")
	}

	union := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Val(1))).
		Union(sql.Select(sql.Col("id")).From(orders))
	if analyze.HasWhere(union) {
		t.Error("union: HasWhere = true, want false (the WHERE belongs to an arm)")
	}
}

func TestHasJoin(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}
	tests := []struct {
		name  string
		query sql.Query
		want  bool
	}{
		{"select with join", sql.Select(sql.Col("id")).From(users).Join(orders, sql.Eq(sql.Col("users.id"), sql.Col("orders.user_id"))), true},
		{"select without join", sql.Select(sql.Col("id")).From(users), false},
		{"insert has no join", sql.InsertInto(users).Columns("name").Values("a"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analyze.HasJoin(tt.query); got != tt.want {
				t.Errorf("HasJoin = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectsStar(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	tests := []struct {
		name  string
		query sql.Query
		want  bool
	}{
		{"select star", sql.Select(sql.Star()).From(users), true},
		{"qualified star in list", sql.Select(sql.StarOf("users"), sql.Col("id")).From(users), true},
		{"explicit columns", sql.Select(sql.Col("id")).From(users), false},
		{"insert is not a star select", sql.InsertInto(users).Columns("name").Values("a"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analyze.SelectsStar(tt.query); got != tt.want {
				t.Errorf("SelectsStar = %v, want %v", got, tt.want)
			}
		})
	}
}
