package sql

import (
	"strings"
	"testing"
)

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
	if _, _, err := CreateFunction("").Language("sql").Body("x").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CREATE FUNCTION without a name succeeded, want error")
	}
	if _, _, err := DropFunction("").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("DROP FUNCTION without a name succeeded, want error")
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

// TestFunctionRejectedOnMySQL covers requirePostgresObject for CREATE/DROP
// FUNCTION: the body is always dollar-quoted and LANGUAGE is mandatory, which
// is PostgreSQL-only syntax MySQL's parser rejects — compiling for MySQL must
// error, not silently emit invalid SQL (matching the sequence/enum/extension/
// materialized-view guards; requirePostgresObject omitted functions from its
// sweep until this fix).
func TestFunctionRejectedOnMySQL(t *testing.T) {
	mysql := MySQLDialect{}
	if _, _, err := CreateFunction("f").Language("sql").Body("SELECT 1").ToSQL(mysql); err == nil {
		t.Fatal("CREATE FUNCTION on mysql succeeded, want error")
	} else if !strings.Contains(err.Error(), "does not support functions") {
		t.Fatalf("CREATE FUNCTION on mysql error = %v, want mentioning functions", err)
	}
	if _, _, err := DropFunction("f").ToSQL(mysql); err == nil {
		t.Fatal("DROP FUNCTION on mysql succeeded, want error")
	} else if !strings.Contains(err.Error(), "does not support functions") {
		t.Fatalf("DROP FUNCTION on mysql error = %v, want mentioning functions", err)
	}
}

// TestFunctionCoverage exercises the function builder options and plumbing.
func TestFunctionCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustCompile := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mustCompile(CreateFunction("f").OrReplace().Arg("x", "int").
		Args(FunctionArg{Mode: "IN", Name: "y", Type: "int", Default: "0"}).
		Returns("int").Language("sql").Option("STABLE").Body("SELECT 1"))
	mustCompile(DropFunction("f").IfExists().ArgTypes("int").Cascade())
	mustCompile(DropFunction("g").Restrict())

	_ = CreateFunction("f").Stmt()
	_ = CreateFunction("f").buildStmt()
	_ = DropFunction("f").Stmt()
	_ = DropFunction("f").buildStmt()
	for _, q := range []Query{
		CreateFunction("f"), CreateFunction("f").Stmt(),
		DropFunction("f"), DropFunction("f").Stmt(),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}
