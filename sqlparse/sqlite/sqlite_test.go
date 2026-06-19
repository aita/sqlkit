package sqlite_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/sqlite"
)

// compile parses one statement and compiles it back under the SQLite dialect.
func compile(t *testing.T, input string) (string, []any) {
	t.Helper()
	q, err := sqlite.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q): %v", input, err)
	}
	text, args, err := q.ToSQL(sql.SQLiteDialect{})
	if err != nil {
		t.Fatalf("ToSQL(%q): %v", input, err)
	}
	return text, args
}

func TestParseSelect(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		args []any
	}{
		{
			name: "columns and where",
			in:   `SELECT id, name FROM users WHERE active = 1`,
			want: `SELECT "id", "name" FROM "users" WHERE ("active" = ?)`,
			args: []any{1},
		},
		{
			name: "star",
			in:   `SELECT * FROM t`,
			want: `SELECT * FROM "t"`,
		},
		{
			name: "qualified star and alias",
			in:   `SELECT a, b.* FROM t`,
			want: `SELECT "a", "b".* FROM "t"`,
		},
		{
			name: "distinct",
			in:   `SELECT DISTINCT a FROM t`,
			want: `SELECT DISTINCT "a" FROM "t"`,
		},
		{
			name: "column and table aliases",
			in:   `SELECT u.id AS uid FROM users AS u`,
			want: `SELECT "u"."id" AS "uid" FROM "users" AS "u"`,
		},
		{
			name: "left join on",
			in:   `SELECT * FROM users u LEFT JOIN orders o ON o.user_id = u.id`,
			want: `SELECT * FROM "users" AS "u" LEFT JOIN "orders" AS "o" ON ("o"."user_id" = "u"."id")`,
		},
		{
			name: "join using",
			in:   `SELECT * FROM a JOIN b USING (id)`,
			want: `SELECT * FROM "a" JOIN "b" USING ("id")`,
		},
		{
			name: "cross join via comma",
			in:   `SELECT * FROM a, b`,
			want: `SELECT * FROM "a" CROSS JOIN "b"`,
		},
		{
			name: "and / or / not",
			in:   `SELECT * FROM t WHERE a = 1 AND (b = 2 OR NOT (c = 3))`,
			want: `SELECT * FROM "t" WHERE (("a" = ?) AND (("b" = ?) OR (NOT ("c" = ?))))`,
			args: []any{1, 2, 3},
		},
		{
			name: "in list",
			in:   `SELECT * FROM t WHERE x IN (1, 2, 3)`,
			want: `SELECT * FROM "t" WHERE ("x" IN (?, ?, ?))`,
			args: []any{1, 2, 3},
		},
		{
			name: "between and is null",
			in:   `SELECT * FROM t WHERE (z BETWEEN 1 AND 9) AND (y IS NULL)`,
			want: `SELECT * FROM "t" WHERE (("z" BETWEEN ? AND ?) AND ("y" IS NULL))`,
			args: []any{1, 9},
		},
		{
			name: "arithmetic and concat",
			in:   `SELECT a + b * 2, x || y FROM t`,
			want: `SELECT ("a" + ("b" * ?)), ("x" || "y") FROM "t"`,
			args: []any{2},
		},
		{
			name: "aggregate count distinct",
			in:   `SELECT COUNT(*), COUNT(DISTINCT id) FROM t GROUP BY k HAVING COUNT(*) > 1`,
			want: `SELECT COUNT(*), COUNT(DISTINCT "id") FROM "t" GROUP BY "k" HAVING (COUNT(*) > ?)`,
			args: []any{1},
		},
		{
			name: "order limit offset",
			in:   `SELECT a FROM t ORDER BY a DESC, b ASC LIMIT 10 OFFSET 5`,
			want: `SELECT "a" FROM "t" ORDER BY "a" DESC, "b" LIMIT 10 OFFSET 5`,
		},
		{
			name: "subquery source",
			in:   `SELECT b.id FROM (SELECT id FROM t) AS b`,
			want: `SELECT "b"."id" FROM (SELECT "id" FROM "t") AS "b"`,
		},
		{
			name: "exists subquery",
			in:   `SELECT * FROM t WHERE EXISTS (SELECT 1 FROM u WHERE u.t_id = t.id)`,
			want: `SELECT * FROM "t" WHERE EXISTS (SELECT 1 FROM "u" WHERE ("u"."t_id" = "t"."id"))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args := compile(t, tt.in)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
			if len(args) == 0 && len(tt.args) == 0 {
				return
			}
			if !reflect.DeepEqual(args, tt.args) {
				t.Errorf("args got %v want %v", args, tt.args)
			}
		})
	}
}

// TestUnsupportedRoundTrips checks that statements without a typed converter
// still yield a usable query by deparsing back to SQL text.
func TestUnsupportedRoundTrips(t *testing.T) {
	tests := []string{
		`INSERT INTO t (a) VALUES (1)`,
		`UPDATE t SET a = 1 WHERE id = 2`,
		`DELETE FROM t WHERE id = 1`,
		`SELECT a FROM t UNION SELECT a FROM s`,
		`CREATE TABLE t (id INTEGER PRIMARY KEY)`,
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			q, err := sqlite.ParseOne(in)
			if err != nil {
				t.Fatalf("ParseOne: %v", err)
			}
			if _, ok := q.(sql.RawQuery); !ok {
				t.Fatalf("expected sql.RawQuery fallback, got %T", q)
			}
			if _, _, err := q.ToSQL(sql.SQLiteDialect{}); err != nil {
				t.Fatalf("ToSQL: %v", err)
			}
		})
	}
}

func TestParseMultipleStatements(t *testing.T) {
	queries, err := sqlite.Parse(`SELECT 1; SELECT 2`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("got %d queries, want 2", len(queries))
	}
}

func TestParseError(t *testing.T) {
	if _, err := sqlite.Parse(`SELECT FROM`); err == nil {
		t.Fatal("expected a parse error for malformed SQL")
	}
}
