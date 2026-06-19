package sql

import "testing"

func TestRenameColumnStmt(t *testing.T) {
	r := RenameColumn(Table{Name: "users"}, "email", "addr")
	if r.Table.Name != "users" || r.OldName != "email" || r.NewName != "addr" {
		t.Errorf("RenameColumnStmt = %+v", r)
	}
}
