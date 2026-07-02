package analysis_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

func TestKindOf(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}

	tests := []struct {
		name     string
		query    sql.Query
		wantKind analysis.StmtKind
		wantOK   bool
	}{
		{"select", sql.Select(sql.Col("id")).From(users), analysis.StmtSelect, true},
		{"union is select", sql.Select(sql.Col("id")).From(users).Union(sql.Select(sql.Col("id")).From(orders)), analysis.StmtSelect, true},
		{"values is select", sql.Values([]sql.Expression{sql.Expr(1), sql.Expr(2)}), analysis.StmtSelect, true},
		{"insert", sql.InsertInto(users).Columns("name").Values("a"), analysis.StmtInsert, true},
		{"update", sql.Update(users).Set(sql.Set("name", sql.Expr("a"))), analysis.StmtUpdate, true},
		{"delete", sql.DeleteFrom(users), analysis.StmtDelete, true},
		{"truncate is ddl", sql.Truncate(users), analysis.StmtDDL, true},
		{"nil is unknown", nil, analysis.StmtUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKind, gotOK := analysis.KindOf(tt.query)
			if gotKind != tt.wantKind || gotOK != tt.wantOK {
				t.Errorf("KindOf = (%v, %v), want (%v, %v)", gotKind, gotOK, tt.wantKind, tt.wantOK)
			}
		})
	}
}

func TestKindHelpers(t *testing.T) {
	if !analysis.StmtSelect.IsRead() {
		t.Errorf("StmtSelect.IsRead() = false, want true")
	}
	if analysis.StmtSelect.IsWrite() {
		t.Errorf("StmtSelect.IsWrite() = true, want false")
	}
	for _, k := range []analysis.StmtKind{analysis.StmtInsert, analysis.StmtUpdate, analysis.StmtDelete} {
		if !k.IsWrite() {
			t.Errorf("%s.IsWrite() = false, want true", k)
		}
		if k.IsRead() {
			t.Errorf("%s.IsRead() = true, want false", k)
		}
	}
}

// REPLACE INTO is MySQL's insert-or-replace: a write. The parsed-AST path
// (KindOf, via sql.InsertStmt's Replace flag) already classified it correctly;
// DetectKind's raw-text fallback fell through to StmtUnknown (IsWrite false),
// so a REPLACE INTO analyzed without a parser (or after a parse failure)
// silently bypassed a ForbidWrites/ReadOnly guard.
func TestReplaceIntoIsAWrite(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q := sql.ReplaceInto(users).Columns("id", "name").Values(1, "a")

	if k, ok := analysis.KindOf(q); !ok || k != analysis.StmtInsert || !k.IsWrite() {
		t.Errorf("KindOf = (%v, %v); want (insert, true) with IsWrite true", k, ok)
	}
	if k := analysis.DetectKind("REPLACE INTO users (id, name) VALUES (1, 'a')"); k != analysis.StmtInsert || !k.IsWrite() {
		t.Errorf("DetectKind = %v; want insert (a write)", k)
	}
	an := analysis.New().Analyze("REPLACE INTO users (id, name) VALUES (1, 'a')", q)
	if !an.IsWrite() {
		t.Errorf("Analyze.IsWrite() = false; want true for a REPLACE INTO")
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
			kind, ok := analysis.KindOf(tt.query)
			if !ok || kind != analysis.StmtDDL {
				t.Errorf("KindOf = (%v, %v), want (ddl, true)", kind, ok)
			}
			if got := analysis.Tables(tt.query); !reflect.DeepEqual(got, tt.wantTables) {
				t.Errorf("Tables = %v, want %v", got, tt.wantTables)
			}
		})
	}
}

func TestKindOfUnknown(t *testing.T) {
	// A statement of an unrecognized, non-DDL type yields ok=false. ValuesQuery is
	// a SELECT-kind read, so use it to confirm the StmtSelect arm via Tables=nil.
	if got := analysis.Tables(sql.Values([]sql.Expression{sql.Expr(1)})); got != nil {
		t.Errorf("Tables(values) = %v, want nil", got)
	}
}
