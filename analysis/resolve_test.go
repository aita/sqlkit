package analysis_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

func TestResolveColumn(t *testing.T) {
	joined := []analysis.SourceRef{
		{Schema: sql.NewIdent("public"), Table: sql.NewIdent("users"), Alias: sql.NewIdent("u")},
		{Schema: sql.NewIdent("public"), Table: sql.NewIdent("orders"), Alias: sql.NewIdent("o")},
	}
	single := []analysis.SourceRef{{Schema: sql.NewIdent("public"), Table: sql.NewIdent("users")}}
	selfJoin := []analysis.SourceRef{
		{Table: sql.NewIdent("employees"), Alias: sql.NewIdent("e")},
		{Table: sql.NewIdent("employees"), Alias: sql.NewIdent("m")},
	}

	tests := []struct {
		name       string
		scope      []analysis.SourceRef
		col        analysis.ColumnRef
		wantTable  sql.Ident
		wantStatus analysis.Status
	}{
		{
			name:       "qualified by alias",
			scope:      joined,
			col:        analysis.ColumnRef{Qualifier: sql.NewIdent("u"), Name: sql.NewIdent("name")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analysis.Resolved,
		},
		{
			name:       "qualified by alias, other side",
			scope:      joined,
			col:        analysis.ColumnRef{Qualifier: sql.NewIdent("o"), Name: sql.NewIdent("amount")},
			wantTable:  sql.NewIdent("orders"),
			wantStatus: analysis.Resolved,
		},
		{
			name:       "qualified by table name when unaliased",
			scope:      single,
			col:        analysis.ColumnRef{Qualifier: sql.NewIdent("users"), Name: sql.NewIdent("id")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analysis.Resolved,
		},
		{
			name:       "unqualified in single-table scope resolves",
			scope:      single,
			col:        analysis.ColumnRef{Name: sql.NewIdent("email")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analysis.Resolved,
		},
		{
			name:       "unqualified across distinct tables is ambiguous",
			scope:      joined,
			col:        analysis.ColumnRef{Name: sql.NewIdent("id")},
			wantStatus: analysis.Ambiguous,
		},
		{
			name:       "unqualified self-join resolves to the shared table",
			scope:      selfJoin,
			col:        analysis.ColumnRef{Name: sql.NewIdent("id")},
			wantTable:  sql.NewIdent("employees"),
			wantStatus: analysis.Resolved,
		},
		{
			name:       "qualifier matching no source is unknown",
			scope:      joined,
			col:        analysis.ColumnRef{Qualifier: sql.NewIdent("x"), Name: sql.NewIdent("id")},
			wantStatus: analysis.Unknown,
		},
		{
			name:       "no base-table source is unknown",
			scope:      []analysis.SourceRef{{Alias: sql.NewIdent("sub")}}, // derived source only
			col:        analysis.ColumnRef{Name: sql.NewIdent("id")},
			wantStatus: analysis.Unknown,
		},
		{
			name:       "case-insensitive alias",
			scope:      joined,
			col:        analysis.ColumnRef{Qualifier: sql.NewIdent("U"), Name: sql.NewIdent("name")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analysis.Resolved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analysis.ResolveColumn(tt.scope, tt.col)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
			if !got.Table.Equal(tt.wantTable) {
				t.Errorf("Table = %q, want %q", got.Table, tt.wantTable)
			}
		})
	}
}

// TestResolveColumnAll covers the fan-out resolver: it returns a candidate per
// base table a reference may map to, tracing through CTEs and the arms of a UNION,
// and dedupes candidates that name the same table.
func TestResolveColumnAll(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	admins := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("admins")}

	tableNames := func(rs []analysis.Resolution) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.Table.String()
		}
		return out
	}

	t.Run("union arms fan out", func(t *testing.T) {
		// WITH u AS (SELECT name FROM users UNION SELECT name FROM admins)
		// SELECT * FROM u  -> u.name resolves to both users and admins.
		cte := sql.Select(sql.Col("name")).From(users).
			Union(sql.Select(sql.Col("name")).From(admins)).CTE("u")
		scope := analysis.SourcesOf(sql.Select(sql.Star()).From(cte).WithCTE(cte))
		got := analysis.ResolveColumnAll(scope, analysis.ColumnRef{Qualifier: sql.NewIdent("u"), Name: sql.NewIdent("name")})
		if names := tableNames(got); !reflect.DeepEqual(names, []string{"users", "admins"}) {
			t.Errorf("candidates = %v, want [users admins]", names)
		}
	})

	t.Run("self union dedupes", func(t *testing.T) {
		cte := sql.Select(sql.Col("name")).From(users).
			Union(sql.Select(sql.Col("name")).From(users)).CTE("u")
		scope := analysis.SourcesOf(sql.Select(sql.Star()).From(cte).WithCTE(cte))
		got := analysis.ResolveColumnAll(scope, analysis.ColumnRef{Qualifier: sql.NewIdent("u"), Name: sql.NewIdent("name")})
		if len(got) != 1 || got[0].Table.String() != "users" {
			t.Errorf("candidates = %v, want exactly [users]", tableNames(got))
		}
	})
}

// TestEnrichSources checks ambient CTEs annotate a matching base-table source with
// the CTE body, so a reference through it resolves to the underlying table.
func TestEnrichSources(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	cte := sql.Select(sql.Col("id"), sql.Col("name")).From(users).CTE("a")

	// Scope of "SELECT ... FROM a" where a is only known as an ambient CTE.
	scope := analysis.SourcesOf(sql.Select(sql.Star()).From(sql.Tbl("a")))
	if got := analysis.ResolveColumn(scope, analysis.ColumnRef{Qualifier: sql.NewIdent("a"), Name: sql.NewIdent("name")}); got.Status != analysis.Resolved || got.Table.String() != "a" {
		// Before enrichment, "a" is just a bare table named "a".
		t.Logf("pre-enrich status=%v table=%q (a treated as base table)", got.Status, got.Table)
	}
	analysis.EnrichSources(scope, []sql.CTE{cte})
	got := analysis.ResolveColumn(scope, analysis.ColumnRef{Qualifier: sql.NewIdent("a"), Name: sql.NewIdent("name")})
	if got.Status != analysis.Resolved || got.Table.String() != "users" {
		t.Errorf("after enrich: status=%v table=%q, want Resolved users", got.Status, got.Table)
	}
}

// TestResolveReportsClauseAndAccess checks Resolve tags each reference with where
// it appears and whether it is read or written.
func TestResolveReportsClauseAndAccess(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	// UPDATE users SET name = 'x' WHERE id = 1
	q := sql.Update(users).Set(sql.Set("name", sql.Val("x"))).Where(sql.Eq(sql.Col("id"), sql.Val(1)))

	var sawWrite, sawWhere bool
	for _, r := range analysis.Resolve(q) {
		switch r.Column.Name.String() {
		case "name":
			if r.Clause == analysis.ClauseSet && r.Access == analysis.Write && r.Table.String() == "users" {
				sawWrite = true
			}
		case "id":
			if r.Clause == analysis.ClauseWhere && r.Access == analysis.Read && r.Table.String() == "users" {
				sawWhere = true
			}
		}
	}
	if !sawWrite {
		t.Error("SET name not reported as a write in the set clause")
	}
	if !sawWhere {
		t.Error("WHERE id not reported as a read in the where clause")
	}
}

// TestResolveColumnThroughSubquery checks that a column qualified by a
// derived-table alias resolves through the subquery's SELECT list to the
// underlying base column.
func TestResolveColumnThroughSubquery(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}

	// (SELECT id, name FROM users) AS t → t.name resolves to users.name
	sub := sql.Select(sql.Col("id"), sql.Col("name")).From(users)
	scope := analysis.SourcesOf(
		sql.Select(sql.Star()).From(sql.Alias(sub, "t")),
	)

	col := analysis.ColumnRef{Qualifier: sql.NewIdent("t"), Name: sql.NewIdent("name")}
	got := analysis.ResolveColumn(scope, col)
	if got.Status != analysis.Resolved {
		t.Fatalf("Status = %v, want Resolved", got.Status)
	}
	if got.Table.String() != "users" {
		t.Errorf("Table = %q, want %q", got.Table, "users")
	}
}

// TestResolveColumnThroughCTE checks that a column qualified by a CTE name
// resolves through the CTE's SELECT list to the underlying base column.
func TestResolveColumnThroughCTE(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}

	// WITH filtered AS (SELECT id, name FROM users) SELECT * FROM filtered
	// → filtered.name resolves to users.name
	cte := sql.Select(sql.Col("id"), sql.Col("name")).From(users).CTE("filtered")
	q := sql.Select(sql.Star()).From(cte).WithCTE(cte)
	scope := analysis.SourcesOf(q)

	col := analysis.ColumnRef{Qualifier: sql.NewIdent("filtered"), Name: sql.NewIdent("name")}
	got := analysis.ResolveColumn(scope, col)
	if got.Status != analysis.Resolved {
		t.Fatalf("Status = %v, want Resolved", got.Status)
	}
	if got.Table.String() != "users" {
		t.Errorf("Table = %q, want %q", got.Table, "users")
	}
}

// TestResolveColumnThroughSubqueryWildcard checks SELECT * in a subquery lets
// the outer scope resolve columns through it.
func TestResolveColumnThroughSubqueryWildcard(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}

	sub := sql.Select(sql.Star()).From(users)
	scope := analysis.SourcesOf(
		sql.Select(sql.Star()).From(sql.Alias(sub, "t")),
	)

	col := analysis.ColumnRef{Qualifier: sql.NewIdent("t"), Name: sql.NewIdent("email")}
	got := analysis.ResolveColumn(scope, col)
	if got.Status != analysis.Resolved {
		t.Fatalf("Status = %v, want Resolved", got.Status)
	}
	if got.Table.String() != "users" {
		t.Errorf("Table = %q, want %q", got.Table, "users")
	}
}

// TestResolveSubqueryScope checks each query node resolves against its own
// sources: an inner reference binds to the subquery's table, not the outer one.
func TestResolveSubqueryScope(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	orders := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("orders")}

	// SELECT id FROM users WHERE id IN (SELECT user_id FROM orders)
	q := sql.Select(sql.Col("id")).From(users).
		Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(orders)))

	var inUser, inOrders bool
	for _, res := range analysis.Resolve(q) {
		switch res.Column.Name.String() {
		case "id":
			if res.Table.String() == "users" {
				inUser = true
			}
		case "user_id":
			if res.Table.String() == "orders" {
				inOrders = true
			}
		}
	}
	if !inUser {
		t.Error("outer id did not resolve to users")
	}
	if !inOrders {
		t.Error("inner user_id did not resolve to orders")
	}
}
