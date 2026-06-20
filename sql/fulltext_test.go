package sql

import "testing"

func TestMatchPostgres(t *testing.T) {
	// Single column, natural-language query.
	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("articles")}).
			Where(Match(Col("body")).Against("quick fox")),
		`SELECT "id" FROM "articles" WHERE (to_tsvector("body") @@ plainto_tsquery($1))`,
		[]any{"quick fox"})

	// Multiple columns form the document, concatenated null-safely.
	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("articles")}).
			Where(Match(Col("title"), Col("body")).Against("quick fox")),
		`SELECT "id" FROM "articles" WHERE (to_tsvector(concat_ws(' ', "title", "body")) @@ plainto_tsquery($1))`,
		[]any{"quick fox"})

	// Boolean mode uses websearch_to_tsquery; WithConfig passes the regconfig.
	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("articles")}).
			Where(Match(Col("title")).Against("quick -slow").Boolean().WithConfig("english")),
		`SELECT "id" FROM "articles" WHERE (to_tsvector('english', "title") @@ websearch_to_tsquery('english', $1))`,
		[]any{"quick -slow"})
}

func TestMatchMySQL(t *testing.T) {
	// Natural-language mode over a multi-column FULLTEXT index.
	assertMySQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("articles")}).
			Where(Match(Col("title"), Col("body")).Against("quick fox")),
		"SELECT `id` FROM `articles` WHERE (MATCH(`title`, `body`) AGAINST(? IN NATURAL LANGUAGE MODE))",
		[]any{"quick fox"})

	// Boolean mode; the PostgreSQL-only config is ignored.
	assertMySQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("articles")}).
			Where(Match(Col("body")).Against("+quick -slow").Boolean().WithConfig("english")),
		"SELECT `id` FROM `articles` WHERE (MATCH(`body`) AGAINST(? IN BOOLEAN MODE))",
		[]any{"+quick -slow"})
}

func TestMatchUnsupportedDialect(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: NewIdent("articles")}).
		Where(Match(Col("body")).Against("quick fox"))
	for _, d := range []Dialect{StandardSQLDialect{}} {
		if _, _, err := q.ToSQL(d); err == nil {
			t.Fatalf("ToSQL(%s) error = nil, want unsupported full-text search", d.Name())
		}
	}
}
