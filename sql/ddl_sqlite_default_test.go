package sql

import (
	"strings"
	"testing"
)

// SQLite's column DEFAULT accepts a literal, a signed number or CURRENT_* bare,
// and any other expression only parenthesized: "DEFAULT lower('x')" is a syntax
// error. A literal default keeps its bare form.
func TestSQLiteDefaultExpressionParenthesized(t *testing.T) {
	cases := map[string]Expression{
		`"a" text DEFAULT (lower('X'))`:          Func("lower", Raw("'X'")),
		`"a" text DEFAULT (datetime('now'))`:     Raw("datetime('now')"),
		`"a" text DEFAULT ((1) + (2))`:           Raw("(1) + (2)"),
		`"a" text DEFAULT (lower('x'))`:          Raw("(lower('x'))"),
		`"a" text DEFAULT 'x'`:                   Raw("'x'"),
		`"a" text DEFAULT -1`:                    Raw("-1"),
		`"a" text DEFAULT 1.5`:                   Raw("1.5"),
		`"a" text DEFAULT NULL`:                  Raw("NULL"),
		`"a" text DEFAULT CURRENT_TIMESTAMP`:     Raw("CURRENT_TIMESTAMP"),
		`"a" text DEFAULT 'it''s (not) a group'`: Raw("'it''s (not) a group'"),
	}
	for want, def := range cases {
		for _, d := range []Dialect{SQLiteDialect{}, LibSQLDialect{}} {
			q := CreateTable(Table{Name: NewIdent("t")}).
				Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "TEXT"}, Default: def})
			got, _, err := q.ToSQL(d)
			if err != nil {
				t.Fatalf("%s: %v", want, err)
			}
			if !strings.Contains(got, want) {
				t.Errorf("%s: got %s\nwant it to contain %s", d.Name(), got, want)
			}
		}
	}
	// PostgreSQL is unchanged.
	q := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "text"}, Default: Func("lower", Raw("'X'"))})
	if got, _, _ := q.ToSQL(PostgreSQLDialect{}); !strings.Contains(got, `DEFAULT lower('X')`) {
		t.Errorf("postgres: %s", got)
	}
}
