package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sqlparse/mysql"
)

// Benchmarks for the MySQL parse hot path: SQL text -> typed sqlkit/sql query
// through the TiDB parser (the real MySQL grammar). They make the parser's
// latency and allocations measurable and regression-trackable (e.g. with
// benchstat) — parsing is on the request path for anyone who accepts SQL text,
// so "no feature is quietly slow" applies to it too.

var myParseStatements = []struct {
	name string
	sql  string
}{
	{"Select", "SELECT id, name FROM users WHERE active AND age >= 18 ORDER BY name LIMIT 20"},
	{"Join", "SELECT p.id, u.name FROM posts p JOIN users u ON p.user_id = u.id " +
		"LEFT JOIN comments c ON c.post_id = p.id WHERE u.name = 'alice'"},
	{"Aggregate", "SELECT user_id, count(*), sum(amount) FROM orders WHERE status = 'paid' " +
		"GROUP BY user_id HAVING sum(amount) > 100 ORDER BY count(*) DESC"},
	{"Insert", "INSERT INTO users (name, email, active) VALUES ('a', 'a@b.c', 1), ('b', 'b@b.c', 0)"},
	{"CTE", "WITH recent AS (SELECT id FROM posts WHERE created_at > '2020-01-01') SELECT count(*) FROM recent"},
}

// BenchmarkParse measures parsing one representative statement of each shape
// (simple SELECT, multi-table JOIN, aggregate, multi-row INSERT, CTE).
func BenchmarkParse(b *testing.B) {
	for _, tc := range myParseStatements {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := mysql.ParseOne(tc.sql); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
