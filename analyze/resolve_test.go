package analyze_test

import (
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

func TestResolveColumn(t *testing.T) {
	joined := []analyze.SourceRef{
		{Schema: sql.NewIdent("public"), Table: sql.NewIdent("users"), Alias: sql.NewIdent("u")},
		{Schema: sql.NewIdent("public"), Table: sql.NewIdent("orders"), Alias: sql.NewIdent("o")},
	}
	single := []analyze.SourceRef{{Schema: sql.NewIdent("public"), Table: sql.NewIdent("users")}}
	selfJoin := []analyze.SourceRef{
		{Table: sql.NewIdent("employees"), Alias: sql.NewIdent("e")},
		{Table: sql.NewIdent("employees"), Alias: sql.NewIdent("m")},
	}

	tests := []struct {
		name       string
		scope      []analyze.SourceRef
		col        analyze.ColumnRef
		wantTable  sql.Ident
		wantStatus analyze.Status
	}{
		{
			name:       "qualified by alias",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: sql.NewIdent("u"), Name: sql.NewIdent("name")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analyze.Resolved,
		},
		{
			name:       "qualified by alias, other side",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: sql.NewIdent("o"), Name: sql.NewIdent("amount")},
			wantTable:  sql.NewIdent("orders"),
			wantStatus: analyze.Resolved,
		},
		{
			name:       "qualified by table name when unaliased",
			scope:      single,
			col:        analyze.ColumnRef{Qualifier: sql.NewIdent("users"), Name: sql.NewIdent("id")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analyze.Resolved,
		},
		{
			name:       "unqualified in single-table scope resolves",
			scope:      single,
			col:        analyze.ColumnRef{Name: sql.NewIdent("email")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analyze.Resolved,
		},
		{
			name:       "unqualified across distinct tables is ambiguous",
			scope:      joined,
			col:        analyze.ColumnRef{Name: sql.NewIdent("id")},
			wantStatus: analyze.Ambiguous,
		},
		{
			name:       "unqualified self-join resolves to the shared table",
			scope:      selfJoin,
			col:        analyze.ColumnRef{Name: sql.NewIdent("id")},
			wantTable:  sql.NewIdent("employees"),
			wantStatus: analyze.Resolved,
		},
		{
			name:       "qualifier matching no source is unknown",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: sql.NewIdent("x"), Name: sql.NewIdent("id")},
			wantStatus: analyze.Unknown,
		},
		{
			name:       "no base-table source is unknown",
			scope:      []analyze.SourceRef{{Alias: sql.NewIdent("sub")}}, // derived source only
			col:        analyze.ColumnRef{Name: sql.NewIdent("id")},
			wantStatus: analyze.Unknown,
		},
		{
			name:       "case-insensitive alias",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: sql.NewIdent("U"), Name: sql.NewIdent("name")},
			wantTable:  sql.NewIdent("users"),
			wantStatus: analyze.Resolved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze.ResolveColumn(tt.scope, tt.col)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
			if !got.Table.Equal(tt.wantTable) {
				t.Errorf("Table = %q, want %q", got.Table, tt.wantTable)
			}
		})
	}
}

// TestResolveReportsClauseAndAccess checks Resolve tags each reference with where
// it appears and whether it is read or written.
func TestResolveReportsClauseAndAccess(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	// UPDATE users SET name = 'x' WHERE id = 1
	q := sql.Update(users).Set(sql.Set("name", sql.Val("x"))).Where(sql.Eq(sql.Col("id"), sql.Val(1)))

	var sawWrite, sawWhere bool
	for _, r := range analyze.Resolve(q) {
		switch r.Column.Name.String() {
		case "name":
			if r.Clause == analyze.ClauseSet && r.Access == analyze.Write && r.Table.String() == "users" {
				sawWrite = true
			}
		case "id":
			if r.Clause == analyze.ClauseWhere && r.Access == analyze.Read && r.Table.String() == "users" {
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

// TestResolveSubqueryScope checks each query node resolves against its own
// sources: an inner reference binds to the subquery's table, not the outer one.
func TestResolveSubqueryScope(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	orders := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("orders")}

	// SELECT id FROM users WHERE id IN (SELECT user_id FROM orders)
	q := sql.Select(sql.Col("id")).From(users).
		Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(orders)))

	var inUser, inOrders bool
	for _, res := range analyze.Resolve(q) {
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
