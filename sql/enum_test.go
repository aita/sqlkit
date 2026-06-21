package sql

import "testing"

func TestCreateEnumStmt(t *testing.T) {
	if e := CreateEnum("mood", "sad", "ok"); e.Name.String() != "mood" || len(e.Values) != 2 {
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

// TestEnumCoverage exercises the enum/type builders' options and the
// Stmt/buildStmt/setPos plumbing.
func TestEnumCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustCompile := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mustCompile(CreateEnum("mood", "happy", "sad"))
	mustCompile(DropType("mood").IfExists().Cascade())
	mustCompile(DropType("mood", "status").IfExists()) // multiple names exercise the separator
	mustCompile(AlterTypeAddValue("mood", "meh").IfNotExists().After("happy"))
	mustCompile(AlterTypeAddValue("mood", "x").Before("sad"))
	mustCompile(AlterTypeRenameValue("mood", "sad", "down"))

	_ = DropType("m").Stmt()
	_ = DropType("m").buildStmt()
	_ = AlterTypeAddValue("m", "v").Stmt()
	_ = AlterTypeAddValue("m", "v").buildStmt()
	for _, q := range []Query{
		CreateEnum("m", "a"),
		DropType("m"), DropType("m").Stmt(),
		AlterTypeAddValue("m", "v"), AlterTypeAddValue("m", "v").Stmt(),
		AlterTypeRenameValue("m", "a", "b"),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(3)
		}
	}
}

// TestEnumValidationErrors covers the enum/type compile validation guards.
func TestEnumValidationErrors(t *testing.T) {
	pg := PostgreSQLDialect{}
	errOn := func(name string, q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	errOn("create enum no name", CreateEnum(""))
	errOn("drop type no names", DropType())
	errOn("add value empty", AlterTypeAddValue("", ""))
	errOn("rename value empty", AlterTypeRenameValue("", "", ""))
}
