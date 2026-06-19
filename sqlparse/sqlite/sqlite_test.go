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
			name: "searched case",
			in:   `SELECT CASE WHEN a > 0 THEN 'p' ELSE 'n' END FROM t`,
			want: `SELECT CASE WHEN ("a" > ?) THEN ? ELSE ? END FROM "t"`,
			args: []any{0, "p", "n"},
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
		`REPLACE INTO t (a) VALUES (1)`,
		`INSERT INTO t DEFAULT VALUES`,
		`DROP INDEX idx`,
		`ALTER TABLE t ADD COLUMN c INT`,
		`CREATE VIEW v AS SELECT 1`,
		`CREATE TABLE t AS SELECT a FROM s`,
		`CREATE TABLE t (id INTEGER, c TEXT COLLATE NOCASE)`,
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

func TestParseCompoundCTEWindow(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		args []any
	}{
		{
			name: "union",
			in:   `SELECT a FROM t UNION SELECT a FROM s`,
			want: `(SELECT "a" FROM "t") UNION (SELECT "a" FROM "s")`,
		},
		{
			name: "union all chain with order/limit",
			in:   `SELECT a FROM t UNION ALL SELECT a FROM s INTERSECT SELECT a FROM r ORDER BY a LIMIT 5`,
			want: `((SELECT "a" FROM "t") UNION ALL (SELECT "a" FROM "s")) INTERSECT (SELECT "a" FROM "r") ORDER BY "a" LIMIT 5`,
		},
		{
			name: "cte",
			in:   `WITH c AS (SELECT id FROM t) SELECT id FROM c`,
			want: `WITH "c" AS (SELECT "id" FROM "t") SELECT "id" FROM "c"`,
		},
		{
			name: "recursive cte",
			in:   `WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 10) SELECT n FROM nums`,
			want: `WITH RECURSIVE "nums" ("n") AS ((SELECT 1) UNION ALL (SELECT ("n" + ?) FROM "nums" WHERE ("n" < ?))) SELECT "n" FROM "nums"`,
			args: []any{1, 10},
		},
		{
			name: "inline window",
			in:   `SELECT ROW_NUMBER() OVER (PARTITION BY k ORDER BY id DESC) FROM t`,
			want: `SELECT ROW_NUMBER() OVER (PARTITION BY "k" ORDER BY "id" DESC) FROM "t"`,
		},
		{
			name: "named window",
			in:   `SELECT SUM(x) OVER w FROM t WINDOW w AS (PARTITION BY k)`,
			want: `SELECT SUM("x") OVER "w" FROM "t" WINDOW "w" AS (PARTITION BY "k")`,
		},
		{
			name: "in subquery",
			in:   `SELECT * FROM t WHERE x IN (SELECT id FROM u)`,
			want: `SELECT * FROM "t" WHERE ("x" IN (SELECT "id" FROM "u"))`,
		},
		{
			name: "scalar subquery",
			in:   `SELECT (SELECT MAX(x) FROM u) FROM t`,
			want: `SELECT (SELECT MAX("x") FROM "u") FROM "t"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args := compile(t, tt.in)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
			if !(len(args) == 0 && len(tt.args) == 0) && !reflect.DeepEqual(args, tt.args) {
				t.Errorf("args got %v want %v", args, tt.args)
			}
		})
	}
}

func TestParseDML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		args []any
	}{
		{
			name: "insert multi-row",
			in:   `INSERT INTO t (a, b) VALUES (1, 2), (3, 4)`,
			want: `INSERT INTO "t" ("a", "b") VALUES (?, ?), (?, ?)`,
			args: []any{1, 2, 3, 4},
		},
		{
			name: "insert from select",
			in:   `INSERT INTO t (a) SELECT a FROM s`,
			want: `INSERT INTO "t" ("a") SELECT "a" FROM "s"`,
		},
		{
			name: "insert upsert",
			in:   `INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET b = excluded.b`,
			want: `INSERT INTO "t" ("a") VALUES (?) ON CONFLICT ("a") DO UPDATE SET "b" = excluded."b"`,
			args: []any{1},
		},
		{
			name: "insert do nothing",
			in:   `INSERT INTO t (a) VALUES (1) ON CONFLICT DO NOTHING`,
			want: `INSERT INTO "t" ("a") VALUES (?) ON CONFLICT DO NOTHING`,
			args: []any{1},
		},
		{
			name: "update with returning",
			in:   `UPDATE t SET a = 1, b = 2 WHERE id = 3 RETURNING id`,
			want: `UPDATE "t" SET "a" = ?, "b" = ? WHERE ("id" = ?) RETURNING "id"`,
			args: []any{1, 2, 3},
		},
		{
			name: "delete",
			in:   `DELETE FROM t WHERE id = 1`,
			want: `DELETE FROM "t" WHERE ("id" = ?)`,
			args: []any{1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args := compile(t, tt.in)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
			if !(len(args) == 0 && len(tt.args) == 0) && !reflect.DeepEqual(args, tt.args) {
				t.Errorf("args got %v want %v", args, tt.args)
			}
		})
	}
}

func TestParseDDL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "create table",
			in:   `CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, age INT DEFAULT 0)`,
			want: "CREATE TABLE \"t\" (\n    \"id\" INTEGER PRIMARY KEY AUTOINCREMENT,\n    \"name\" TEXT NOT NULL,\n    \"age\" INT DEFAULT 0\n);",
		},
		{
			name: "create table if not exists with primary key",
			in:   `CREATE TABLE IF NOT EXISTS t (a INT, b INT, PRIMARY KEY (a, b))`,
			want: "CREATE TABLE IF NOT EXISTS \"t\" (\n    \"a\" INT,\n    \"b\" INT,\n    PRIMARY KEY (\"a\", \"b\")\n);",
		},
		{
			name: "create index",
			in:   `CREATE INDEX idx ON t (a, b)`,
			want: `CREATE INDEX "idx" ON "t" ("a", "b");`,
		},
		{
			name: "create unique index",
			in:   `CREATE UNIQUE INDEX idx ON t (a)`,
			want: `CREATE UNIQUE INDEX "idx" ON "t" ("a");`,
		},
		{
			name: "drop table if exists",
			in:   `DROP TABLE IF EXISTS t`,
			want: `DROP TABLE IF EXISTS "t";`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := compile(t, tt.in)
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestSourcePositions checks that the parser records each node's byte offset in
// the source SQL.
func TestSourcePositions(t *testing.T) {
	//             0         1         2         3
	//             0123456789012345678901234567890123
	const in = `SELECT id FROM users WHERE id = 1`
	q, err := sqlite.ParseOne(in)
	if err != nil {
		t.Fatalf("ParseOne: %v", err)
	}
	stmt := q.(sql.SelectBuilder).Stmt()

	if got := stmt.Pos(); got != 0 {
		t.Errorf("statement Pos() = %d, want 0", got)
	}
	if got := stmt.Columns[0].(sql.ColumnExpression).Pos(); got != 7 {
		t.Errorf("column id Pos() = %d, want 7", got)
	}
	where := stmt.Where.(sql.BinaryExpression)
	if got := where.Pos(); got != 30 {
		t.Errorf("WHERE '=' Pos() = %d, want 30", got)
	}
	if got := where.Left.(sql.ColumnExpression).Pos(); got != 27 {
		t.Errorf("WHERE id Pos() = %d, want 27", got)
	}
	if got := where.Right.(sql.ValueExpression).Pos(); got != 32 {
		t.Errorf("WHERE literal Pos() = %d, want 32", got)
	}
}

// TestPositionsDoNotAffectSQL confirms the compiler ignores positions: a parsed
// query and the same query built fresh compile identically.
func TestPositionsDoNotAffectSQL(t *testing.T) {
	got, args := compile(t, `SELECT id FROM users WHERE id = 1`)
	want, _, err := sql.Select(sql.Col("id")).
		From(sql.Table{Name: "users"}).
		Where(sql.Eq(sql.Col("id"), 1)).
		ToSQL(sql.SQLiteDialect{})
	if err != nil {
		t.Fatalf("builder ToSQL: %v", err)
	}
	if got != want {
		t.Errorf("parsed %q\nbuilt  %q", got, want)
	}
	if len(args) != 1 {
		t.Errorf("args = %v, want one", args)
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
