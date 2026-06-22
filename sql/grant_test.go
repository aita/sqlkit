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

// TestRevokeObjectNameContainingTO pins that REVOKE emits FROM by building the
// clause with the right keyword, not by rewriting " TO " in the finished text:
// an object name that itself contains " TO " must survive intact.
func TestRevokeObjectNameContainingTO(t *testing.T) {
	got, _, err := Revoke("SELECT").On("a TO b").From("alice").ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `REVOKE SELECT ON TABLE "a TO b" FROM "alice";`
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

// TestGrantCoverage exercises the GRANT/REVOKE builder options and plumbing.
func TestGrantCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustCompile := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mustCompile(Grant("SELECT").On("t").To("u").WithGrantOption())
	mustCompile(Grant("USAGE").OnType("SCHEMA", "public").To("u"))
	mustCompile(Grant("SELECT").On("t1", "t2").To("u1", "u2")) // multiple objects and grantees
	mustCompile(Revoke("SELECT").GrantOptionFor().On("t").From("u").Cascade())
	mustCompile(Revoke("USAGE").OnType("SCHEMA", "public").From("u").Restrict())

	// REVOKE with no object reaches the grantBody error path.
	if _, _, err := Revoke("SELECT").From("u").ToSQL(pg); err == nil {
		t.Fatal("REVOKE without an object: want error")
	}

	// buildStmt unwraps the builder to its statement form.
	_ = Grant("SELECT").On("t").To("u").buildStmt()
	_ = Revoke("SELECT").On("t").From("u").buildStmt()

	for _, q := range []Query{
		Grant("SELECT").On("t").To("u"), Grant("SELECT").On("t").To("u").Stmt(),
		Revoke("SELECT").On("t").From("u"), Revoke("SELECT").On("t").From("u").Stmt(),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}
