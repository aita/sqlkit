package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestFullTextSearch covers the cross-dialect Match(...).Against(...) builder.
// The compiler renders it per dialect — to_tsvector @@ plainto_tsquery on
// PostgreSQL, MATCH ... AGAINST on MySQL — so the same builder call has to
// produce SQL each engine accepts and evaluates. The fixture differs only in the
// DDL (MySQL needs a FULLTEXT index); the query is identical.
func TestFullTextSearch(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		createArticles(t, db, eng)

		rows := []string{
			"(1, 'PostgreSQL tutorial', 'learn about relational databases and queries')",
			"(2, 'MySQL guide', 'covering indexes and joins in depth')",
			"(3, 'Cooking recipes', 'pasta with tomato sauce and basil')",
		}
		for _, r := range rows {
			if _, err := db.SQL().ExecContext(ctx, "INSERT INTO articles (id, title, body) VALUES "+r); err != nil {
				t.Fatalf("seed articles: %v", err)
			}
		}

		// "databases" appears only in row 1's body, so the full-text match must
		// return exactly that article on either engine.
		var hits []int64
		if err := db.Select(sql.Col("id")).From(sql.Tbl("articles")).
			Where(sqlkit.Cond(sql.Match(sql.Col("title"), sql.Col("body")).Against("databases"))).
			All(ctx).Scan(&hits); err != nil {
			t.Fatalf("full-text search: %v", err)
		}
		if len(hits) != 1 || hits[0] != 1 {
			t.Fatalf("match 'databases' ids = %#v, want [1]", hits)
		}
	})
}

func createArticles(t *testing.T, db *sqlkit.Database, eng engine) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatalf("drop articles: %v", err)
	}
	// MySQL needs an explicit FULLTEXT index for MATCH ... AGAINST; PostgreSQL
	// evaluates to_tsvector over the columns directly, no index required.
	ddl := "CREATE TABLE articles (id BIGINT PRIMARY KEY, title VARCHAR(255) NOT NULL, body TEXT NOT NULL)"
	if eng.name == "mysql" {
		ddl = "CREATE TABLE articles (id BIGINT PRIMARY KEY, title VARCHAR(255) NOT NULL, body TEXT NOT NULL, FULLTEXT idx_ft (title, body)) ENGINE=InnoDB"
	}
	if _, err := db.SQL().ExecContext(ctx, ddl); err != nil {
		t.Fatalf("create articles: %v", err)
	}
	t.Cleanup(func() { _, _ = db.SQL().ExecContext(context.Background(), "DROP TABLE IF EXISTS articles") })
}
