package sql

import (
	"strings"
	"testing"
)

// A negative JSON index counts from the end, as on PostgreSQL. In a JSON path
// (MySQL's JSON_EXTRACT, the standard JSON_VALUE/JSON_QUERY) that is
// $[last] / $[last-N]; $[-1] is an invalid path.
func TestJSONPathNegativeIndex(t *testing.T) {
	cases := []struct {
		d    Dialect
		e    Expression
		want string
	}{
		{MySQLDialect{}, JSONGet(Col("doc"), -1), `'$[last]'`},
		{MySQLDialect{}, JSONGetText(Col("doc"), "a", -3), `'$.a[last-2]'`},
		{MySQLDialect{}, JSONGet(Col("doc"), 2), `'$[2]'`},
		{StandardSQLDialect{}, JSONGetText(Col("doc"), -2), `'$[last-1]'`},
	}
	for _, tc := range cases {
		got, _, err := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(Eq(tc.e, Val(1))).ToSQL(tc.d)
		if err != nil || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %s (err %v), want it to contain %s", tc.d.Name(), got, err, tc.want)
		}
	}
}
