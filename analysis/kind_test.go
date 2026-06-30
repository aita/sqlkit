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
		{"merge", sql.MergeInto(users).Using(orders).On(sql.Eq(sql.Col("users.id"), sql.Col("orders.id"))).When(sql.WhenMatched().ThenDelete()), analysis.StmtMerge, true},
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
	for _, k := range []analysis.StmtKind{analysis.StmtInsert, analysis.StmtUpdate, analysis.StmtDelete, analysis.StmtMerge} {
		if !k.IsWrite() {
			t.Errorf("%s.IsWrite() = false, want true", k)
		}
		if k.IsRead() {
			t.Errorf("%s.IsRead() = true, want false", k)
		}
	}
}

// A MERGE is a data-modifying statement: it must classify as a write through
// every entry point — KindOf on the AST, DetectKind on the leading keyword, and
// Analyze.IsWrite — or a read/write router could send it to a read replica. The
// analysis layer recognized MERGE inside a writable CTE (ctesWrite) but missed
// the top-level statement.
func TestMergeIsAWrite(t *testing.T) {
	accounts := sql.Table{Name: sql.NewIdent("accounts")}
	staging := sql.Table{Name: sql.NewIdent("staging")}
	q := sql.MergeInto(accounts).Using(staging).
		On(sql.Eq(sql.Col("accounts.id"), sql.Col("staging.id"))).
		When(sql.WhenMatched().ThenUpdate(sql.Set("balance", sql.Expr(0)))).
		When(sql.WhenNotMatched().ThenInsert([]string{"id"}, 1))

	if k, ok := analysis.KindOf(q); !ok || k != analysis.StmtMerge || !k.IsWrite() {
		t.Errorf("KindOf = (%v, %v); want (merge, true) with IsWrite true", k, ok)
	}
	if k := analysis.DetectKind("MERGE INTO accounts USING staging ON true"); k != analysis.StmtMerge || !k.IsWrite() {
		t.Errorf("DetectKind = %v; want merge (a write)", k)
	}
	an := analysis.New().Analyze("MERGE INTO accounts ...", q)
	if !an.IsWrite() {
		t.Errorf("Analyze.IsWrite() = false; want true for a MERGE")
	}
	if got := analysis.Tables(q); !reflect.DeepEqual(got, []string{"accounts", "staging"}) {
		t.Errorf("Tables = %v; want [accounts staging]", got)
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
