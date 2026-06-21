package analysis_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

func TestTables(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	tests := []struct {
		name  string
		query sql.Query
		want  []string
	}{
		{
			name:  "select with where",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  []string{"users"},
		},
		{
			name:  "select with join",
			query: sql.Select(sql.Col("id")).From(users).Join(orders, sql.Eq(sql.Col("users.id"), sql.Col("orders.user_id"))),
			want:  []string{"users", "orders"},
		},
		{
			name:  "insert",
			query: sql.InsertInto(users).Columns("name").Values("a").Returning(sql.Col("id")),
			want:  []string{"users"},
		},
		{
			name:  "update",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("a"))),
			want:  []string{"users"},
		},
		{
			name:  "delete",
			query: sql.DeleteFrom(users),
			want:  []string{"users"},
		},
		{
			name:  "truncate is ddl",
			query: sql.Truncate(users, orders),
			want:  []string{"users", "orders"},
		},
		{
			name:  "nil has no tables",
			query: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analysis.Tables(tt.query); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Tables = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTablesSubqueriesAndCTEs(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}
	posts := sql.Table{Name: sql.NewIdent("posts")}

	// active is a CTE over users, referenced by name from the outer queries.
	active := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("active"), sql.Val(true))).CTE("active")

	tests := []struct {
		name  string
		query sql.Query
		want  []string
	}{
		{
			name:  "from subquery resolves inner table",
			query: sql.Select(sql.Col("x.id")).From(sql.Alias(sql.Select(sql.Col("id")).From(users), "x")),
			want:  []string{"users"},
		},
		{
			name:  "join subquery resolves inner table",
			query: sql.Select(sql.Col("u.id")).From(sql.SourceAlias(users, "u")).Join(sql.Alias(sql.Select(sql.Col("user_id")).From(posts), "p"), sql.Eq(sql.Col("u.id"), sql.Col("p.user_id"))),
			want:  []string{"users", "posts"},
		},
		{
			name:  "where IN subquery is collected",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(posts))),
			want:  []string{"users", "posts"},
		},
		{
			name:  "correlated scalar subquery in select list is collected",
			query: sql.Select(sql.Col("u.id"), sql.As(sql.ScalarSubquery(sql.Select(sql.CountAll()).From(sql.SourceAlias(orders, "o")).Where(sql.Eq(sql.Col("o.user_id"), sql.Col("u.id")))), "n")).From(sql.SourceAlias(users, "u")),
			want:  []string{"users", "orders"},
		},
		{
			name:  "exists subquery is collected",
			query: sql.Select(sql.Col("id")).From(sql.SourceAlias(users, "u")).Where(sql.Exists(sql.Select(sql.Raw("1")).From(sql.SourceAlias(posts, "p")).Where(sql.Eq(sql.Col("p.user_id"), sql.Col("u.id"))))),
			want:  []string{"users", "posts"},
		},
		{
			name:  "cte body resolves underlying table, cte name excluded",
			query: sql.Select(active.Col("id")).From(active).WithCTE(active),
			want:  []string{"users"},
		},
		{
			name:  "cte plus join keeps real tables only",
			query: sql.Select(active.Col("id")).From(sql.SourceAlias(active, "a")).Join(sql.SourceAlias(orders, "o"), sql.Eq(sql.Col("a.id"), sql.Col("o.user_id"))).WithCTE(active),
			want:  []string{"users", "orders"},
		},
		{
			name:  "delete with subquery collects both tables",
			query: sql.DeleteFrom(sql.Table{Name: sql.NewIdent("sessions")}).Where(sql.InQuery(sql.Col("user_id"), sql.Select(sql.Col("id")).From(users))),
			want:  []string{"sessions", "users"},
		},
		{
			name: "insert on conflict do update with subqueries",
			query: sql.InsertInto(users).Columns("id", "rank").Values(1, 2).
				OnConflict("id").
				DoUpdate(sql.Set("rank", sql.ScalarSubquery(sql.Select(sql.CountAll()).From(orders)))).
				Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(posts))),
			want: []string{"users", "orders", "posts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analysis.Tables(tt.query); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Tables = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTablesCompoundWithOrderBy(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}
	// A UNION (CompoundStmt) with a top-level ORDER BY: tables come from both arms,
	// and the ORDER BY term is walked for embedded subqueries.
	q := sql.Select(sql.Col("id")).From(users).
		Union(sql.Select(sql.Col("user_id")).From(orders)).
		OrderBy(sql.Asc(sql.Col("id")))
	if got := analysis.Tables(q); !reflect.DeepEqual(got, []string{"users", "orders"}) {
		t.Errorf("Tables(union) = %v, want [users orders]", got)
	}
}
