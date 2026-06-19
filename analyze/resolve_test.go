package analyze_test

import (
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

func TestResolveColumn(t *testing.T) {
	joined := []analyze.SourceRef{
		{Schema: "public", Table: "users", Alias: "u"},
		{Schema: "public", Table: "orders", Alias: "o"},
	}
	single := []analyze.SourceRef{{Schema: "public", Table: "users"}}
	selfJoin := []analyze.SourceRef{
		{Table: "employees", Alias: "e"},
		{Table: "employees", Alias: "m"},
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
			col:        analyze.ColumnRef{Qualifier: "u", Name: "name"},
			wantTable:  "users",
			wantStatus: analyze.Resolved,
		},
		{
			name:       "qualified by alias, other side",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: "o", Name: "amount"},
			wantTable:  "orders",
			wantStatus: analyze.Resolved,
		},
		{
			name:       "qualified by table name when unaliased",
			scope:      single,
			col:        analyze.ColumnRef{Qualifier: "users", Name: "id"},
			wantTable:  "users",
			wantStatus: analyze.Resolved,
		},
		{
			name:       "unqualified in single-table scope resolves",
			scope:      single,
			col:        analyze.ColumnRef{Name: "email"},
			wantTable:  "users",
			wantStatus: analyze.Resolved,
		},
		{
			name:       "unqualified across distinct tables is ambiguous",
			scope:      joined,
			col:        analyze.ColumnRef{Name: "id"},
			wantStatus: analyze.Ambiguous,
		},
		{
			name:       "unqualified self-join resolves to the shared table",
			scope:      selfJoin,
			col:        analyze.ColumnRef{Name: "id"},
			wantTable:  "employees",
			wantStatus: analyze.Resolved,
		},
		{
			name:       "qualifier matching no source is unknown",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: "x", Name: "id"},
			wantStatus: analyze.Unknown,
		},
		{
			name:       "no base-table source is unknown",
			scope:      []analyze.SourceRef{{Alias: "sub"}}, // derived source only
			col:        analyze.ColumnRef{Name: "id"},
			wantStatus: analyze.Unknown,
		},
		{
			name:       "case-insensitive alias",
			scope:      joined,
			col:        analyze.ColumnRef{Qualifier: "U", Name: "name"},
			wantTable:  "users",
			wantStatus: analyze.Resolved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze.ResolveColumn(tt.scope, tt.col)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
			if got.Table != tt.wantTable {
				t.Errorf("Table = %q, want %q", got.Table, tt.wantTable)
			}
		})
	}
}

// TestResolveReportsClauseAndAccess checks Resolve tags each reference with where
// it appears and whether it is read or written.
func TestResolveReportsClauseAndAccess(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	// UPDATE users SET name = 'x' WHERE id = 1
	q := sql.Update(users).Set(sql.Set("name", sql.Val("x"))).Where(sql.Eq(sql.Col("id"), sql.Val(1)))

	var sawWrite, sawWhere bool
	for _, r := range analyze.Resolve(q) {
		switch r.Column.Name {
		case "name":
			if r.Clause == analyze.ClauseSet && r.Access == analyze.Write && r.Table == "users" {
				sawWrite = true
			}
		case "id":
			if r.Clause == analyze.ClauseWhere && r.Access == analyze.Read && r.Table == "users" {
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
	users := sql.Table{Schema: "public", Name: "users"}
	orders := sql.Table{Schema: "public", Name: "orders"}

	// SELECT id FROM users WHERE id IN (SELECT user_id FROM orders)
	q := sql.Select(sql.Col("id")).From(users).
		Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(orders)))

	var inUser, inOrders bool
	for _, res := range analyze.Resolve(q) {
		switch res.Column.Name {
		case "id":
			if res.Table == "users" {
				inUser = true
			}
		case "user_id":
			if res.Table == "orders" {
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
