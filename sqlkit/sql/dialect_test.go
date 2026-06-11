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
