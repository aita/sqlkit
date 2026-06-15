package sql

import "testing"

func TestCreateFunctionCompile(t *testing.T) {
	stmt := CreateFunction("add").OrReplace().
		Arg("a", "integer").Arg("b", "integer").
		Returns("integer").Language("sql").Option("IMMUTABLE", "STRICT").
		Body("SELECT a + b")
	got, args, err := stmt.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `CREATE OR REPLACE FUNCTION "add"(a integer, b integer) RETURNS integer ` +
		`LANGUAGE sql IMMUTABLE STRICT AS $$SELECT a + b$$;`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
}

func TestCreateFunctionValidation(t *testing.T) {
	if _, _, err := CreateFunction("f").Body("x").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CREATE FUNCTION without language succeeded, want error")
	}
	if _, _, err := CreateFunction("f").Language("sql").Body("a $$ b").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CREATE FUNCTION with $$ in body succeeded, want error")
	}
}

func TestDropFunctionCompile(t *testing.T) {
	stmt := DropFunction("hr.add").IfExists().ArgTypes("integer", "integer").Cascade()
	got, _, err := stmt.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `DROP FUNCTION IF EXISTS "hr"."add"(integer, integer) CASCADE;`
	if got != want {
		t.Fatalf("ToSQL() = %q, want %q", got, want)
	}
}
