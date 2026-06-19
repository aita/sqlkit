package sql

import "testing"

func TestCreateEnumStmt(t *testing.T) {
	if e := CreateEnum("mood", "sad", "ok"); e.Name != "mood" || len(e.Values) != 2 {
		t.Errorf("CreateEnumStmt = %+v", e)
	}
}

func TestAlterTypeCompile(t *testing.T) {
	cases := []struct {
		stmt Statement
		want string
	}{
		{AlterTypeAddValue("mood", "great").Before("happy"), `ALTER TYPE "mood" ADD VALUE 'great' BEFORE 'happy';`},
		{AlterTypeRenameValue("mood", "ok", "fine"), `ALTER TYPE "mood" RENAME VALUE 'ok' TO 'fine';`},
	}
	for _, tc := range cases {
		got, _, err := tc.stmt.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("ToSQL() = %q, want %q", got, tc.want)
		}
	}
}
