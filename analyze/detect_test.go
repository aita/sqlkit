package analyze_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

// withoutPos zeroes ColumnRef.SrcPos in each use so the column references can be
// compared without it: the queries here are built (not parsed), so every
// reference carries sql.NoPos. Position wiring is covered where positions exist —
// at the parser level.
func withoutPos(uses []analyze.ColumnUse) []analyze.ColumnUse {
	out := make([]analyze.ColumnUse, len(uses))
	for i, u := range uses {
		u.Column.SrcPos = 0
		out[i] = u
	}
	return out
}

func TestSourcesOf(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	tests := []struct {
		name  string
		query sql.Query
		want  []analyze.SourceRef
	}{
		{
			name:  "select from and join with aliases",
			query: sql.Select(sql.Col("u.id")).From(sql.SourceAlias(users, "u")).Join(sql.SourceAlias(orders, "o"), sql.Eq(sql.Col("u.id"), sql.Col("o.user_id"))),
			want: []analyze.SourceRef{
				{Table: sql.NewIdent("users"), Alias: sql.NewIdent("u")},
				{Table: sql.NewIdent("orders"), Alias: sql.NewIdent("o")},
			},
		},
		{
			name:  "schema-qualified table",
			query: sql.Select(sql.Col("id")).From(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}),
			want:  []analyze.SourceRef{{Schema: sql.NewIdent("public"), Table: sql.NewIdent("users")}},
		},
		{
			name:  "update target plus from",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("x"))).From(sql.SourceAlias(orders, "o")).Where(sql.Eq(sql.Col("users.id"), sql.Col("o.user_id"))),
			want: []analyze.SourceRef{
				{Table: sql.NewIdent("users")},
				{Table: sql.NewIdent("orders"), Alias: sql.NewIdent("o")},
			},
		},
		{
			name:  "delete target",
			query: sql.DeleteFrom(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  []analyze.SourceRef{{Table: sql.NewIdent("users")}},
		},
		{
			name:  "insert target",
			query: sql.InsertInto(users).Columns("name").Values("a"),
			want:  []analyze.SourceRef{{Table: sql.NewIdent("users")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze.SourcesOf(tt.query)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SourcesOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSourcesOfSubquery(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	sub := sql.Select(sql.Col("id")).From(users)
	q := sql.Select(sql.Col("x.id")).From(sql.Alias(sub, "x"))

	got := analyze.SourcesOf(q)
	if len(got) != 1 {
		t.Fatalf("SourcesOf len = %d, want 1", len(got))
	}
	if !got[0].Table.IsZero() || got[0].Alias.String() != "x" {
		t.Errorf("derived source = %+v, want Table empty, Alias x", got[0])
	}
	if got[0].Subquery == nil {
		t.Error("derived source Subquery = nil, want the inner query")
	}
}

func TestColumns(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	tests := []struct {
		name  string
		query sql.Query
		want  []analyze.ColumnUse
	}{
		{
			name:  "select list and where, qualifier preserved",
			query: sql.Select(sql.Col("u.id"), sql.Col("name")).From(sql.SourceAlias(users, "u")).Where(sql.Eq(sql.Col("u.tenant_id"), sql.Expr(1))),
			want: []analyze.ColumnUse{
				{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("u"), Name: sql.NewIdent("id")}, Clause: analyze.ClauseSelect, Access: analyze.Read},
				{Column: analyze.ColumnRef{Name: sql.NewIdent("name")}, Clause: analyze.ClauseSelect, Access: analyze.Read},
				{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("u"), Name: sql.NewIdent("tenant_id")}, Clause: analyze.ClauseWhere, Access: analyze.Read},
			},
		},
		{
			name:  "join on reads from both sides",
			query: sql.Select(sql.Col("id")).From(users).Join(orders, sql.Eq(sql.Col("users.id"), sql.Col("orders.user_id"))),
			want: []analyze.ColumnUse{
				{Column: analyze.ColumnRef{Name: sql.NewIdent("id")}, Clause: analyze.ClauseSelect, Access: analyze.Read},
				{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("users"), Name: sql.NewIdent("id")}, Clause: analyze.ClauseJoinOn, Access: analyze.Read},
				{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("orders"), Name: sql.NewIdent("user_id")}, Clause: analyze.ClauseJoinOn, Access: analyze.Read},
			},
		},
		{
			name:  "insert columns are writes, values are reads",
			query: sql.InsertInto(users).Columns("name", "email").Values("a", "b"),
			want: []analyze.ColumnUse{
				{Column: analyze.ColumnRef{Name: sql.NewIdent("name")}, Clause: analyze.ClauseInsert, Access: analyze.Write},
				{Column: analyze.ColumnRef{Name: sql.NewIdent("email")}, Clause: analyze.ClauseInsert, Access: analyze.Write},
			},
		},
		{
			name:  "update set column is a write, where is a read",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("x"))).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want: []analyze.ColumnUse{
				{Column: analyze.ColumnRef{Name: sql.NewIdent("name")}, Clause: analyze.ClauseSet, Access: analyze.Write},
				{Column: analyze.ColumnRef{Name: sql.NewIdent("id")}, Clause: analyze.ClauseWhere, Access: analyze.Read},
			},
		},
		{
			name:  "schema.table.column keeps full qualifier",
			query: sql.Select(sql.Col("public.users.id")).From(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}),
			want: []analyze.ColumnUse{
				{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("public.users"), Name: sql.NewIdent("id")}, Clause: analyze.ClauseSelect, Access: analyze.Read},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze.Columns(tt.query)
			if !reflect.DeepEqual(withoutPos(got), tt.want) {
				t.Errorf("Columns = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestColumnsDoesNotDescendIntoSubqueries pins that Columns reports only the
// outer scope's references; the subquery's columns belong to its own scope.
func TestColumnsDoesNotDescendIntoSubqueries(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	posts := sql.Table{Name: sql.NewIdent("posts")}
	q := sql.Select(sql.Col("id")).From(users).
		Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("user_id")).From(posts)))

	got := analyze.Columns(q)
	want := []analyze.ColumnUse{
		{Column: analyze.ColumnRef{Name: sql.NewIdent("id")}, Clause: analyze.ClauseSelect, Access: analyze.Read},
		// the IN's left operand "id" in WHERE is a distinct use (different clause);
		// "user_id" lives in the subquery scope and is not reported here.
		{Column: analyze.ColumnRef{Name: sql.NewIdent("id")}, Clause: analyze.ClauseWhere, Access: analyze.Read},
	}
	if !reflect.DeepEqual(withoutPos(got), want) {
		t.Errorf("Columns = %+v, want %+v (subquery columns excluded)", got, want)
	}
}
