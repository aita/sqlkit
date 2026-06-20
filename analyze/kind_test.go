package analyze_test

import (
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

func TestKindOf(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	tests := []struct {
		name     string
		query    sql.Query
		wantKind analyze.StmtKind
		wantOK   bool
	}{
		{"select", sql.Select(sql.Col("id")).From(users), analyze.StmtSelect, true},
		{"union is select", sql.Select(sql.Col("id")).From(users).Union(sql.Select(sql.Col("id")).From(orders)), analyze.StmtSelect, true},
		{"values is select", sql.Values([]sql.Expression{sql.Expr(1), sql.Expr(2)}), analyze.StmtSelect, true},
		{"insert", sql.InsertInto(users).Columns("name").Values("a"), analyze.StmtInsert, true},
		{"update", sql.Update(users).Set(sql.Set("name", sql.Expr("a"))), analyze.StmtUpdate, true},
		{"delete", sql.DeleteFrom(users), analyze.StmtDelete, true},
		{"truncate is ddl", sql.Truncate(users), analyze.StmtDDL, true},
		{"nil is unknown", nil, analyze.StmtUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKind, gotOK := analyze.KindOf(tt.query)
			if gotKind != tt.wantKind || gotOK != tt.wantOK {
				t.Errorf("KindOf = (%v, %v), want (%v, %v)", gotKind, gotOK, tt.wantKind, tt.wantOK)
			}
		})
	}
}

func TestKindHelpers(t *testing.T) {
	if !analyze.StmtSelect.IsRead() {
		t.Errorf("StmtSelect.IsRead() = false, want true")
	}
	if analyze.StmtSelect.IsWrite() {
		t.Errorf("StmtSelect.IsWrite() = true, want false")
	}
	for _, k := range []analyze.StmtKind{analyze.StmtInsert, analyze.StmtUpdate, analyze.StmtDelete} {
		if !k.IsWrite() {
			t.Errorf("%s.IsWrite() = false, want true", k)
		}
		if k.IsRead() {
			t.Errorf("%s.IsRead() = true, want false", k)
		}
	}
}
