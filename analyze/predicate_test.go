package analyze_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

func TestFilterColumns(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	tests := []struct {
		name  string
		query sql.Query
		want  []string
	}{
		{
			name:  "single equality",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("tenant_id"), sql.Expr(1))),
			want:  []string{"tenant_id"},
		},
		{
			name: "and of predicates, qualified names folded",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.And(
				sql.Eq(sql.Col("users.tenant_id"), sql.Expr(1)),
				sql.Eq(sql.Col("DELETED_AT"), sql.Expr(nil)),
			)),
			want: []string{"tenant_id", "deleted_at"},
		},
		{
			name:  "in list",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.In(sql.Col("status"), 1, 2, 3)),
			want:  []string{"status"},
		},
		{
			name:  "no where",
			query: sql.Select(sql.Col("id")).From(users),
			want:  nil,
		},
		{
			name:  "update where",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("x"))).Where(sql.Eq(sql.Col("org_id"), sql.Expr(7))),
			want:  []string{"org_id"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := analyze.FilterColumns(tc.query)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("filter columns = %v, want %v", got, want)
			}
		})
	}
}

// TestWherePredicates checks the single-pass WherePredicates returns exactly what
// FilterColumns and MaxInList report separately.
func TestWherePredicates(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	queries := []sql.Query{
		sql.Select(sql.Col("id")).From(users).Where(sql.And(
			sql.Eq(sql.Col("tenant_id"), sql.Expr(1)),
			sql.In(sql.Col("status"), 1, 2, 3, 4),
		)),
		sql.Select(sql.Col("id")).From(users), // no WHERE
		sql.Update(users).Set(sql.Set("name", sql.Expr("x"))).Where(sql.Eq(sql.Col("org_id"), sql.Expr(7))),
	}
	for _, q := range queries {
		gotCols, gotMax := analyze.WherePredicates(q)
		wantCols, wantMax := analyze.FilterColumns(q), analyze.MaxInList(q)
		if !reflect.DeepEqual(gotCols, wantCols) {
			t.Errorf("WherePredicates columns = %v, want %v (FilterColumns)", gotCols, wantCols)
		}
		if gotMax != wantMax {
			t.Errorf("WherePredicates maxInList = %d, want %d (MaxInList)", gotMax, wantMax)
		}
	}
}

func TestMaxInList(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	sub := sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("banned")})
	tests := []struct {
		name  string
		query sql.Query
		want  int
	}{
		{
			name:  "in of three",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.In(sql.Col("id"), 1, 2, 3)),
			want:  3,
		},
		{
			name: "largest of several",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.And(
				sql.In(sql.Col("a"), 1, 2),
				sql.In(sql.Col("b"), 1, 2, 3, 4, 5),
			)),
			want: 5,
		},
		{
			name:  "subquery in does not count",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.InQuery(sql.Col("id"), sub)),
			want:  0,
		},
		{
			name:  "no in",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := analyze.MaxInList(tc.query); got != tc.want {
				t.Errorf("MaxInList = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNullEqualityPredicate(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	tests := []struct {
		name  string
		query sql.Query
		want  bool
	}{
		{
			name:  "= NULL",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("deleted_at"), sql.Expr(nil))),
			want:  true,
		},
		{
			name: "<> NULL nested under AND",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.And(
				sql.Eq(sql.Col("a"), sql.Expr(1)),
				sql.Ne(sql.Col("b"), sql.Expr(nil)),
			)),
			want: true,
		},
		{
			name:  "IS NULL is fine",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.IsNull(sql.Col("deleted_at"))),
			want:  false,
		},
		{
			name:  "= value is fine",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  false,
		},
		{
			name:  "no where",
			query: sql.Select(sql.Col("id")).From(users),
			want:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := analyze.NullEqualityPredicate(tc.query); got != tc.want {
				t.Errorf("NullEqualityPredicate = %v, want %v", got, tc.want)
			}
		})
	}
}
