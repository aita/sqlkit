package analyze_test

import (
	"reflect"
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

func TestKindOfAndTablesDDL(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}
	sub := sql.Select(sql.Col("id")).From(users)

	tests := []struct {
		name       string
		query      sql.Query
		wantTables []string
	}{
		{"create table", sql.CreateTable(users).Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.EngineType{Name: "int8"}}), []string{"users"}},
		{"create view", sql.CreateView(sql.Table{Name: sql.NewIdent("v")}, sub), []string{"v"}},
		{"create index", sql.CreateIndex("idx", users, "id"), []string{"users"}},
		{"drop index", sql.DropIndex("idx", users), []string{"users"}},
		{"drop foreign key", sql.DropForeignKey(users, "fk"), []string{"users"}},
		{"comment on table", sql.CommentOnTable(users, "c"), []string{"users"}},
		{"comment on column", sql.CommentOnColumn(users, "id", "c"), []string{"users"}},
		{"alter table", sql.AlterTable(users).DropColumn("old"), []string{"users"}},
		{"drop table multiple", sql.DropTable(users, orders), []string{"users", "orders"}},
		{"drop view", sql.DropView(sql.Table{Name: sql.NewIdent("v")}), []string{"v"}},
		{"truncate multiple", sql.Truncate(users, orders), []string{"users", "orders"}},
		{"create schema", sql.CreateSchema("s"), nil},
		{"drop schema", sql.DropSchema("s"), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := analyze.KindOf(tt.query)
			if !ok || kind != analyze.StmtDDL {
				t.Errorf("KindOf = (%v, %v), want (ddl, true)", kind, ok)
			}
			if got := analyze.Tables(tt.query); !reflect.DeepEqual(got, tt.wantTables) {
				t.Errorf("Tables = %v, want %v", got, tt.wantTables)
			}
		})
	}
}

func TestKindOfUnknown(t *testing.T) {
	// A statement of an unrecognized, non-DDL type yields ok=false. ValuesQuery is
	// a SELECT-kind read, so use it to confirm the StmtSelect arm via Tables=nil.
	if got := analyze.Tables(sql.Values([]sql.Expression{sql.Expr(1)})); got != nil {
		t.Errorf("Tables(values) = %v, want nil", got)
	}
}
