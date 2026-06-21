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
