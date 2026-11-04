package sql

import (
	"strings"
	"testing"
)

// SQLite's -> reads a right operand beginning with "$" as a JSON path, not an
// object label, and has no bare negative index; such steps take the quoted
// path-member and $[#-N] forms. Ordinary keys and indices are unchanged.
func TestSQLiteJSONAccessDollarKeyAndNegativeIndex(t *testing.T) {
	cases := map[string]Expression{
		`("doc" ->> '$."$x"')`:           JSONGetText(Col("doc"), "$x"),
		`(("doc" -> '$."$.a"') ->> 'b')`: JSONGetText(Col("doc"), "$.a", "b"),
		`("doc" ->> '$[#-1]')`:           JSONGetText(Col("doc"), -1),
		`(("doc" -> 'a.b') -> 0)`:        JSONGet(Col("doc"), "a.b", 0),
		`("doc" ->> 'it''s')`:            JSONGetText(Col("doc"), "it's"),
	}
	for want, e := range cases {
		for _, d := range []Dialect{SQLiteDialect{}, LibSQLDialect{}} {
			got, _, err := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(Eq(e, Val(1))).ToSQL(d)
			if err != nil || !strings.Contains(got, want) {
				t.Errorf("%s: got %s (err %v), want it to contain %s", d.Name(), got, err, want)
			}
		}
	}
	if _, _, err := Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(Eq(JSONGet(Col("doc"), `$"q`), Val(1))).ToSQL(SQLiteDialect{}); err == nil {
		t.Error(`key $"q: want an error`)
	}
}
