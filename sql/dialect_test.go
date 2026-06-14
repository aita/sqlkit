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
	if (SQLiteDialect{}).Name() != "sqlite" || !(SQLiteDialect{}).SupportsReturning() {
		t.Fatal("sqlite dialect metadata wrong")
	}
	if (StandardSQLDialect{}).Name() != "standard" || (StandardSQLDialect{}).SupportsReturning() {
		t.Fatal("standard dialect metadata wrong")
	}
	for _, d := range []BindParamLimiter{PostgreSQLDialect{}, MySQLDialect{}} {
		if got := d.MaxBindParams(); got != 65535 {
			t.Fatalf("MaxBindParams() = %d, want 65535", got)
		}
	}
}

func TestSQLiteDialectQuoteAndPlaceholder(t *testing.T) {
	d := SQLiteDialect{}
	if got, want := d.QuoteIdent(`main.user"profiles`), `"main"."user""profiles"`; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
	if got := d.Placeholder(3); got != "?" {
		t.Fatalf("Placeholder(3) = %q, want ?", got)
	}
}

func TestStandardDialectQuoteAndPlaceholder(t *testing.T) {
	d := StandardSQLDialect{}
	if got, want := d.QuoteIdent(`s.t`), `"s"."t"`; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
	if got := d.Placeholder(2); got != "?" {
		t.Fatalf("Placeholder(2) = %q, want ?", got)
	}
}

func TestDialectByNameSQLiteAndStandard(t *testing.T) {
	for _, name := range []string{"sqlite", "sqlite3"} {
		d, err := DialectByName(name)
		if err != nil || d.Name() != "sqlite" {
			t.Fatalf("DialectByName(%q) = %v, %v", name, d, err)
		}
	}
	for _, name := range []string{"standard", "ansi", "sql"} {
		d, err := DialectByName(name)
		if err != nil || d.Name() != "standard" {
			t.Fatalf("DialectByName(%q) = %v, %v", name, d, err)
		}
	}
}
