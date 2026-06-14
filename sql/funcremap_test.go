package sql_test

import (
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func funcSQL(t *testing.T, expr Selection, d Dialect) string {
	t.Helper()
	got, _, err := Select(expr).ToSQL(d)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	return got
}

func TestFuncRemapNow(t *testing.T) {
	// PostgreSQL/MySQL have no FuncRenderer: NOW() passes through unchanged.
	if got, want := funcSQL(t, Now(), PostgreSQLDialect{}), "SELECT NOW()"; got != want {
		t.Errorf("postgres NOW = %q, want %q", got, want)
	}
	if got, want := funcSQL(t, Now(), MySQLDialect{}), "SELECT NOW()"; got != want {
		t.Errorf("mysql NOW = %q, want %q", got, want)
	}
	// SQLite and standard remap NOW to CURRENT_TIMESTAMP.
	if got, want := funcSQL(t, Now(), SQLiteDialect{}), "SELECT CURRENT_TIMESTAMP"; got != want {
		t.Errorf("sqlite NOW = %q, want %q", got, want)
	}
	if got, want := funcSQL(t, Now(), StandardSQLDialect{}), "SELECT CURRENT_TIMESTAMP"; got != want {
		t.Errorf("standard NOW = %q, want %q", got, want)
	}
}

func TestFuncRemapLength(t *testing.T) {
	// CHAR_LENGTH passes through on postgres; sqlite remaps to LENGTH; standard
	// keeps the standard CHAR_LENGTH spelling.
	if got, want := funcSQL(t, CharLength(Col("name")), PostgreSQLDialect{}), `SELECT CHAR_LENGTH("name")`; got != want {
		t.Errorf("postgres CHAR_LENGTH = %q, want %q", got, want)
	}
	if got, want := funcSQL(t, CharLength(Col("name")), SQLiteDialect{}), `SELECT LENGTH("name")`; got != want {
		t.Errorf("sqlite CHAR_LENGTH = %q, want %q", got, want)
	}
	if got, want := funcSQL(t, CharLength(Col("name")), StandardSQLDialect{}), `SELECT CHAR_LENGTH("name")`; got != want {
		t.Errorf("standard CHAR_LENGTH = %q, want %q", got, want)
	}
}

func TestFuncRemapUnknownPassThrough(t *testing.T) {
	// A function the dialect does not remap passes through verbatim everywhere.
	for _, d := range []Dialect{PostgreSQLDialect{}, MySQLDialect{}, SQLiteDialect{}, StandardSQLDialect{}} {
		got, _, err := Select(Func("my_custom_fn", Col("x"))).ToSQL(d)
		if err != nil {
			t.Fatalf("ToSQL(%s) error = %v", d.Name(), err)
		}
		quoted := `"x"`
		if d.Name() == "mysql" {
			quoted = "`x`"
		}
		if want := "SELECT my_custom_fn(" + quoted + ")"; got != want {
			t.Errorf("%s custom fn = %q, want %q", d.Name(), got, want)
		}
	}
}

func TestFuncRemapCaseInsensitiveName(t *testing.T) {
	// Canonical names are matched case-insensitively.
	if got, want := funcSQL(t, Func("now"), SQLiteDialect{}), "SELECT CURRENT_TIMESTAMP"; got != want {
		t.Errorf("sqlite now = %q, want %q", got, want)
	}
}
