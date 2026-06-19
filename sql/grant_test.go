package sql

import "testing"

func TestGrantCompile(t *testing.T) {
	stmt := Grant("SELECT", "INSERT").On("employees").To("analyst", "PUBLIC").WithGrantOption()
	got, args, err := stmt.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `GRANT SELECT, INSERT ON TABLE "employees" TO "analyst", PUBLIC WITH GRANT OPTION;`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
}

func TestGrantAllPrivileges(t *testing.T) {
	stmt := Grant().OnType("SCHEMA", "public").To("dev")
	got, _, err := stmt.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `GRANT ALL PRIVILEGES ON SCHEMA "public" TO "dev";`
	if got != want {
		t.Fatalf("ToSQL() = %q, want %q", got, want)
	}
}

func TestRevokeCompile(t *testing.T) {
	stmt := Revoke("UPDATE").GrantOptionFor().On("hr.employees").From("analyst").Cascade()
	got, _, err := stmt.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `REVOKE GRANT OPTION FOR UPDATE ON TABLE "hr"."employees" FROM "analyst" CASCADE;`
	if got != want {
		t.Fatalf("ToSQL() = %q, want %q", got, want)
	}
}

func TestGrantRequiresObjectAndGrantee(t *testing.T) {
	if _, _, err := Grant("SELECT").To("r").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("GRANT without objects succeeded, want error")
	}
	if _, _, err := Grant("SELECT").On("t").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("GRANT without grantees succeeded, want error")
	}
}

func TestGrantStmt(t *testing.T) {
	g := Grant("SELECT", "INSERT").On("employees").To("analyst").WithGrantOption().Stmt()
	if len(g.Privileges) != 2 || len(g.Objects) != 1 || len(g.Grantees) != 1 || !g.WithGrantOption {
		t.Errorf("GrantStmt = %+v", g)
	}
}
