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

func TestAccessString(t *testing.T) {
	if got := analyze.Read.String(); got != "read" {
		t.Errorf("Read.String() = %q, want %q", got, "read")
	}
	if got := analyze.Write.String(); got != "write" {
		t.Errorf("Write.String() = %q, want %q", got, "write")
	}
}

func TestClauseString(t *testing.T) {
	tests := []struct {
		clause analyze.Clause
		want   string
	}{
		{analyze.ClauseSelect, "select"},
		{analyze.ClauseJoinOn, "join_on"},
		{analyze.ClauseWhere, "where"},
		{analyze.ClauseGroupBy, "group_by"},
		{analyze.ClauseHaving, "having"},
		{analyze.ClauseOrderBy, "order_by"},
		{analyze.ClauseWindow, "window"},
		{analyze.ClauseSet, "set"},
		{analyze.ClauseInsert, "insert"},
		{analyze.ClauseValues, "values"},
		{analyze.ClauseConflict, "conflict"},
		{analyze.ClauseMergeOn, "merge_on"},
		{analyze.ClauseReturning, "returning"},
		{analyze.ClauseUnknown, "unknown"},
		{analyze.Clause(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.clause.String(); got != tt.want {
			t.Errorf("Clause(%d).String() = %q, want %q", tt.clause, got, tt.want)
		}
	}
}

func TestTargetScope(t *testing.T) {
	got := analyze.TargetScope(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")})
	want := []analyze.SourceRef{{Schema: sql.NewIdent("public"), Table: sql.NewIdent("users")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TargetScope = %+v, want %+v", got, want)
	}
}

func TestColumnsCompoundOrderBy(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}
	// A CompoundStmt reports only its own ORDER BY columns (the arms are subqueries).
	q := sql.Select(sql.Col("id")).From(users).
		Union(sql.Select(sql.Col("id")).From(orders)).
		OrderBy(sql.Desc(sql.Col("id")))
	got := analyze.Columns(q)
	want := []analyze.ColumnUse{
		{Column: analyze.ColumnRef{Name: sql.NewIdent("id")}, Clause: analyze.ClauseOrderBy, Access: analyze.Read},
	}
	if !reflect.DeepEqual(withoutPos(got), want) {
		t.Errorf("Columns(union) = %+v, want %+v", withoutPos(got), want)
	}
}

func TestColumnsOrderByAndWindow(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	// A named WINDOW clause's PARTITION BY / ORDER BY exercise the window walk in
	// WalkColumns; the top-level ORDER BY exercises orderTerms.
	q := sql.Select(sql.Col("id")).From(users).
		Window(sql.WindowDef("w").PartitionBy(sql.Col("dept")).OrderBy(sql.Asc(sql.Col("hired")))).
		OrderBy(sql.Desc(sql.Col("salary")))

	got := withoutPos(analyze.Columns(q))
	wantContains := []analyze.ColumnUse{
		{Column: analyze.ColumnRef{Name: sql.NewIdent("dept")}, Clause: analyze.ClauseWindow, Access: analyze.Read},
		{Column: analyze.ColumnRef{Name: sql.NewIdent("hired")}, Clause: analyze.ClauseWindow, Access: analyze.Read},
		{Column: analyze.ColumnRef{Name: sql.NewIdent("salary")}, Clause: analyze.ClauseOrderBy, Access: analyze.Read},
	}
	for _, w := range wantContains {
		if !containsUse(got, w) {
			t.Errorf("Columns missing %+v; got %+v", w, got)
		}
	}
}

func containsUse(uses []analyze.ColumnUse, want analyze.ColumnUse) bool {
	for _, u := range uses {
		if reflect.DeepEqual(u, want) {
			return true
		}
	}
	return false
}

func TestMergeColumnsAndSources(t *testing.T) {
	target := sql.Table{Name: sql.NewIdent("accounts")}
	src := sql.Table{Name: sql.NewIdent("staging")}

	q := sql.MergeInto(target).As("t").
		Using(sql.SourceAlias(src, "s")).
		On(sql.Eq(sql.Col("t.id"), sql.Col("s.id"))).
		When(sql.WhenMatched().ThenUpdate(sql.Set("balance", sql.Col("s.balance")))).
		When(sql.WhenNotMatched().ThenInsert([]string{"id", "balance"}, 1, 2)).
		Returning(sql.Col("t.id"))

	// SourcesOf reports the target (aliased) and the USING source.
	srcs := analyze.SourcesOf(q)
	want := []analyze.SourceRef{
		{Table: sql.NewIdent("accounts"), Alias: sql.NewIdent("t")},
		{Table: sql.NewIdent("staging"), Alias: sql.NewIdent("s")},
	}
	if !reflect.DeepEqual(srcs, want) {
		t.Errorf("SourcesOf(merge) = %+v, want %+v", srcs, want)
	}

	// KindOf classifies a MERGE as unknown (not one of the tracked kinds).
	if _, ok := analyze.KindOf(q); ok {
		t.Error("KindOf(merge) ok = true, want false")
	}

	// WalkColumns reaches the ON condition (merge_on), the SET assignment (set), the
	// INSERT column list (insert/write), and RETURNING (returning).
	uses := withoutPos(analyze.Columns(q))
	expect := []analyze.ColumnUse{
		{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("t"), Name: sql.NewIdent("id")}, Clause: analyze.ClauseMergeOn, Access: analyze.Read},
		{Column: analyze.ColumnRef{Name: sql.NewIdent("balance")}, Clause: analyze.ClauseSet, Access: analyze.Write},
		{Column: analyze.ColumnRef{Name: sql.NewIdent("id")}, Clause: analyze.ClauseInsert, Access: analyze.Write},
		{Column: analyze.ColumnRef{Qualifier: sql.NewIdent("t"), Name: sql.NewIdent("id")}, Clause: analyze.ClauseReturning, Access: analyze.Read},
	}
	for _, w := range expect {
		if !containsUse(uses, w) {
			t.Errorf("Columns(merge) missing %+v; got %+v", w, uses)
		}
	}
}

func TestSourcesOfValuesSource(t *testing.T) {
	// A VALUES source names no base table: Table is empty, Subquery nil, Alias kept.
	vs := sql.ValuesSource([][]sql.Expression{{sql.Expr(1)}}, "v", "n")
	q := sql.Select(sql.Col("v.n")).From(vs)
	got := analyze.SourcesOf(q)
	if len(got) != 1 || !got[0].Table.IsZero() || got[0].Subquery != nil || got[0].Alias.String() != "v" {
		t.Errorf("SourcesOf(values) = %+v, want one derived source aliased v", got)
	}
}
