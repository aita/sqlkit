package sql

import "testing"

func TestRenameColumnStmt(t *testing.T) {
	r := RenameColumn(Table{Name: NewIdent("users")}, "email", "addr")
	if r.Table.Name.String() != "users" || r.OldName.String() != "email" || r.NewName.String() != "addr" {
		t.Errorf("RenameColumnStmt = %+v", r)
	}
}

// TestRenameCoverage exercises RENAME TABLE/COLUMN rendering and plumbing.
func TestRenameCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	tbl := Table{Name: NewIdent("t")}
	for _, q := range []Query{RenameTable(tbl, "t2"), RenameColumn(tbl, "a", "b")} {
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}

// TestRenameTableRequiresNames covers the guard that both the source table and
// the new name must be present.
func TestRenameTableRequiresNames(t *testing.T) {
	pg := PostgreSQLDialect{}
	if _, _, err := RenameTable(Table{}, "b").ToSQL(pg); err == nil {
		t.Error("rename with zero table: want error")
	}
	if _, _, err := RenameTable(Table{Name: NewIdent("a")}, "").ToSQL(pg); err == nil {
		t.Error("rename with empty new name: want error")
	}
}
