package sql

import "testing"

func TestPostgreSQLDialectQuoteIdent(t *testing.T) {
	dialect := PostgreSQLDialect{}
	if got, want := dialect.QuoteIdent(`public.user"profiles`), `"public"."user""profiles"`; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
}

func TestPostgreSQLDialectPlaceholder(t *testing.T) {
	dialect := PostgreSQLDialect{}
	for position, want := range []string{"$1", "$2", "$3"} {
		if got := dialect.Placeholder(position + 1); got != want {
			t.Fatalf("Placeholder(%d) = %q, want %q", position+1, got, want)
		}
	}
}

func TestMySQLDialectQuoteIdent(t *testing.T) {
	dialect := MySQLDialect{}
	if got, want := dialect.QuoteIdent("blog.user`profiles"), "`blog`.`user``profiles`"; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
}

func TestMySQLDialectPlaceholder(t *testing.T) {
	dialect := MySQLDialect{}
	for position := 1; position <= 3; position++ {
		if got := dialect.Placeholder(position); got != "?" {
			t.Fatalf("Placeholder(%d) = %q, want ?", position, got)
		}
	}
}

func TestDialectNamesAndCapabilities(t *testing.T) {
	if (PostgreSQLDialect{}).Name() != "postgres" || !(PostgreSQLDialect{}).SupportsReturning() {
		t.Fatal("postgres dialect metadata wrong")
	}
	if (MySQLDialect{}).Name() != "mysql" || (MySQLDialect{}).SupportsReturning() {
		t.Fatal("mysql dialect metadata wrong")
	}
}
