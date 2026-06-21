package analysis_test

import (
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

func TestHasTopLevelWhere(t *testing.T) {
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
			if got := analysis.HasTopLevelWhere(tt.query); got != tt.want {
				t.Errorf("HasTopLevelWhere = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHasTopLevelWhereIsNodeLocal pins that HasTopLevelWhere describes only the
// top-level node: a WHERE inside a CTE body or a UNION arm is that nested query's,
// not the outer's.
func TestHasTopLevelWhereIsNodeLocal(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	body := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("active"), sql.Val(true)))
	cte := body.CTE("c")
	withQuery := sql.Select(cte.Col("id")).From(cte).WithCTE(cte)
	if analysis.HasTopLevelWhere(withQuery) {
		t.Error("CTE outer: HasTopLevelWhere = true, want false (the WHERE is the CTE body's)")
	}

	union := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Val(1))).
		Union(sql.Select(sql.Col("id")).From(orders))
	if analysis.HasTopLevelWhere(union) {
		t.Error("union: HasTopLevelWhere = true, want false (the WHERE belongs to an arm)")
	}
}

func TestHasTopLevelJoin(t *testing.T) {
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
			if got := analysis.HasTopLevelJoin(tt.query); got != tt.want {
				t.Errorf("HasTopLevelJoin = %v, want %v", got, tt.want)
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
			if got := analysis.SelectsStar(tt.query); got != tt.want {
				t.Errorf("SelectsStar = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasTopLevelJoinWriteStatements(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	upd := sql.Update(users).Set(sql.Set("name", sql.Expr("x"))).
		From(sql.SourceAlias(orders, "o")).Join(sql.SourceAlias(orders, "o2"), sql.Eq(sql.Col("o.id"), sql.Col("o2.id")))
	if !analysis.HasTopLevelJoin(upd) {
		t.Error("HasTopLevelJoin(update with join) = false, want true")
	}

	del := sql.DeleteFrom(users).Join(sql.SourceAlias(orders, "o"), sql.Eq(sql.Col("users.id"), sql.Col("o.user_id")))
	if !analysis.HasTopLevelJoin(del) {
		t.Error("HasTopLevelJoin(delete with join) = false, want true")
	}

	// INSERT has no top-level JOIN.
	if analysis.HasTopLevelJoin(sql.InsertInto(users).Columns("name").Values("a")) {
		t.Error("HasTopLevelJoin(insert) = true, want false")
	}
}

func TestSelectsStarAliased(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	// "SELECT users.* AS everything": isStar must look through the AS alias.
	q := sql.Select(sql.As(sql.Star(), "everything"), sql.Col("id")).From(users)
	if !analysis.SelectsStar(q) {
		t.Error("SelectsStar(aliased star) = false, want true")
	}
}
