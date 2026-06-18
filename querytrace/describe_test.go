package querytrace_test

import (
	"reflect"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sql"
)

func TestDescribe(t *testing.T) {
	users := sql.Table{Name: "users"}
	orders := sql.Table{Name: "orders"}

	tests := []struct {
		name  string
		query sql.Query
		want  querytrace.Shape
	}{
		{
			name:  "select with where",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  querytrace.Shape{Kind: querytrace.StmtSelect, Tables: []string{"users"}, HasWhere: true},
		},
		{
			name:  "select with join",
			query: sql.Select(sql.Col("id")).From(users).Join(orders, sql.Eq(sql.Col("users.id"), sql.Col("orders.user_id"))),
			want:  querytrace.Shape{Kind: querytrace.StmtSelect, Tables: []string{"users", "orders"}, HasJoin: true},
		},
		{
			name:  "select with limit",
			query: sql.Select(sql.Col("id")).From(users).Limit(10),
			want:  querytrace.Shape{Kind: querytrace.StmtSelect, Tables: []string{"users"}, HasLimit: true},
		},
		{
			name:  "insert returning",
			query: sql.InsertInto(users).Columns("name").Values("a").Returning(sql.Col("id")),
			want:  querytrace.Shape{Kind: querytrace.StmtInsert, Tables: []string{"users"}, HasReturning: true},
		},
		{
			name:  "update without where",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("a"))),
			want:  querytrace.Shape{Kind: querytrace.StmtUpdate, Tables: []string{"users"}},
		},
		{
			name:  "update with where",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("a"))).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  querytrace.Shape{Kind: querytrace.StmtUpdate, Tables: []string{"users"}, HasWhere: true},
		},
		{
			name:  "delete without where",
			query: sql.DeleteFrom(users),
			want:  querytrace.Shape{Kind: querytrace.StmtDelete, Tables: []string{"users"}},
		},
		{
			name:  "truncate is ddl",
			query: sql.Truncate(users, orders),
			want:  querytrace.Shape{Kind: querytrace.StmtDDL, Tables: []string{"users", "orders"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := querytrace.Describe(tt.query)
			if !ok {
				t.Fatalf("Describe(%s) ok = false, want true", tt.name)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Describe(%s) = %+v, want %+v", tt.name, got, tt.want)
			}
		})
	}
}

func TestDescribeSubqueriesAndCTEs(t *testing.T) {
	users := sql.Table{Name: "users"}
	orders := sql.Table{Name: "orders"}
	posts := sql.Table{Name: "posts"}

	// active is a CTE over users, referenced by name from the outer queries.
	active := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("active"), sql.Val(true))).CTE("active")

	tests := []struct {
		name       string
		query      sql.Query
		wantKind   querytrace.StmtKind
		wantTables []string
	}{
		{
			name:       "from subquery resolves inner table",
			query:      sql.Select(sql.Col("x.id")).From(sql.Alias(sql.Select(sql.Col("id")).From(users), "x")),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users"},
		},
		{
			name:       "join subquery resolves inner table",
			query:      sql.Select(sql.Col("u.id")).From(sql.SourceAlias(users, "u")).Join(sql.Alias(sql.Select(sql.Col("user_id")).From(posts), "p"), sql.Eq(sql.Col("u.id"), sql.Col("p.user_id"))),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users", "posts"},
		},
		{
			name:       "where IN subquery is collected",
			query:      sql.Select(sql.Col("id")).From(users).Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(posts))),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users", "posts"},
		},
		{
			name:       "correlated scalar subquery in select list is collected",
			query:      sql.Select(sql.Col("u.id"), sql.As(sql.ScalarSubquery(sql.Select(sql.CountAll()).From(sql.SourceAlias(orders, "o")).Where(sql.Eq(sql.Col("o.user_id"), sql.Col("u.id")))), "n")).From(sql.SourceAlias(users, "u")),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users", "orders"},
		},
		{
			name:       "exists subquery is collected",
			query:      sql.Select(sql.Col("id")).From(sql.SourceAlias(users, "u")).Where(sql.Exists(sql.Select(sql.Raw("1")).From(sql.SourceAlias(posts, "p")).Where(sql.Eq(sql.Col("p.user_id"), sql.Col("u.id"))))),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users", "posts"},
		},
		{
			name:       "cte body resolves underlying table, cte name excluded",
			query:      sql.Select(active.Col("id")).From(active).WithCTE(active),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users"},
		},
		{
			name:       "cte plus join keeps real tables only",
			query:      sql.Select(active.Col("id")).From(sql.SourceAlias(active, "a")).Join(sql.SourceAlias(orders, "o"), sql.Eq(sql.Col("a.id"), sql.Col("o.user_id"))).WithCTE(active),
			wantKind:   querytrace.StmtSelect,
			wantTables: []string{"users", "orders"},
		},
		{
			name:       "delete with subquery collects both tables",
			query:      sql.DeleteFrom(sql.Table{Name: "sessions"}).Where(sql.InQuery(sql.Col("user_id"), sql.Select(sql.Col("id")).From(users))),
			wantKind:   querytrace.StmtDelete,
			wantTables: []string{"sessions", "users"},
		},
		{
			name: "insert on conflict do update with subqueries",
			query: sql.InsertInto(users).Columns("id", "rank").Values(1, 2).
				OnConflict("id").
				DoUpdate(sql.Set("rank", sql.ScalarSubquery(sql.Select(sql.CountAll()).From(orders)))).
				Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(posts))),
			wantKind:   querytrace.StmtInsert,
			wantTables: []string{"users", "orders", "posts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := querytrace.Describe(tt.query)
			if !ok {
				t.Fatalf("Describe ok = false, want true")
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, tt.wantKind)
			}
			if !reflect.DeepEqual(got.Tables, tt.wantTables) {
				t.Errorf("Tables = %v, want %v", got.Tables, tt.wantTables)
			}
		})
	}
}

// TestDescribeFlagsAreNodeLocal pins the rule that HasWhere/HasJoin/HasLimit
// describe only the top-level statement: a clause inside a CTE body or a UNION
// arm belongs to that nested query, not the outer statement.
func TestDescribeFlagsAreNodeLocal(t *testing.T) {
	users := sql.Table{Name: "users"}
	orders := sql.Table{Name: "orders"}

	// CTE body has WHERE+LIMIT; the outer SELECT has neither.
	body := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("active"), sql.Val(true))).Limit(3)
	cte := body.CTE("c")
	withQuery := sql.Select(cte.Col("id")).From(cte).WithCTE(cte)
	if s, _ := querytrace.Describe(withQuery); s.HasWhere || s.HasLimit {
		t.Errorf("CTE outer: HasWhere=%v HasLimit=%v, want both false (clauses are the CTE body's)", s.HasWhere, s.HasLimit)
	}

	// UNION: each arm filters/limits, but the compound itself does not.
	left := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Val(1)))
	right := sql.Select(sql.Col("id")).From(orders).Limit(5)
	union := left.Union(right)
	s, ok := querytrace.Describe(union)
	if !ok || s.Kind != querytrace.StmtSelect {
		t.Fatalf("union Describe ok=%v kind=%v", ok, s.Kind)
	}
	if s.HasWhere || s.HasJoin || s.HasLimit {
		t.Errorf("union: HasWhere=%v HasJoin=%v HasLimit=%v, want all false (clauses belong to the arms)", s.HasWhere, s.HasJoin, s.HasLimit)
	}
	// Tables, by contrast, aggregates across both arms.
	if !reflect.DeepEqual(s.Tables, []string{"users", "orders"}) {
		t.Errorf("union Tables = %v, want [users orders]", s.Tables)
	}

	// A LIMIT on the UNION itself is reflected.
	if s2, _ := querytrace.Describe(union.Limit(10)); !s2.HasLimit {
		t.Errorf("union with own LIMIT: HasLimit=false, want true")
	}
}

func TestDescribeKindHelpers(t *testing.T) {
	if !querytrace.StmtSelect.IsRead() {
		t.Errorf("StmtSelect.IsRead() = false, want true")
	}
	if querytrace.StmtSelect.IsWrite() {
		t.Errorf("StmtSelect.IsWrite() = true, want false")
	}
	for _, k := range []querytrace.StmtKind{querytrace.StmtInsert, querytrace.StmtUpdate, querytrace.StmtDelete} {
		if !k.IsWrite() {
			t.Errorf("%s.IsWrite() = false, want true", k)
		}
		if k.IsRead() {
			t.Errorf("%s.IsRead() = true, want false", k)
		}
	}
}

func TestDescribeUnknown(t *testing.T) {
	if _, ok := querytrace.Describe(nil); ok {
		t.Errorf("Describe(nil) ok = true, want false")
	}
}
