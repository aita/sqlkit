package sql

import "testing"

func TestRenameColumnStmt(t *testing.T) {
	r := RenameColumn(Table{Name: NewIdent("users")}, "email", "addr")
	if r.Table.Name.String() != "users" || r.OldName.String() != "email" || r.NewName.String() != "addr" {
		t.Errorf("RenameColumnStmt = %+v", r)
	}
}
