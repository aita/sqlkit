// Command redact applies a schema to PostgreSQL, introspects it, and prints a
// few SQL statements with their sensitive string literals masked by the redact
// package. The PII columns are read from the schema itself — every column whose
// comment contains "pii" — so the database is the single source of truth.
//
// It applies schema.sql first, so an empty database is fine:
//
//	createdb redact_demo
//	DATABASE_URL=postgres://localhost/redact_demo go run .
package main

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aita/sqlkit/introspect"
	"github.com/aita/sqlkit/redact"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// dsn is read from the environment in init and reused by main; only the
// connection string is shared, each opens (and closes) its own *sql.DB.
var dsn string

// init reads the DSN and applies schema.sql, so the tables and their PII column
// comments exist before main introspects them.
func init() {
	dsn = os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("set DATABASE_URL to a PostgreSQL connection string")
	}
	db, err := stdsql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ddl, err := os.ReadFile("schema.sql")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(ddl)); err != nil {
		log.Fatal(err)
	}
}

func main() {
	db, err := stdsql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// Introspect the schema into metadata, carrying each column's comment.
	md, err := introspect.Schema(context.Background(), db, "postgres", "public")
	if err != nil {
		log.Fatal(err)
	}

	// The PII columns are those whose comment contains "pii"; the predicate also
	// receives the table and column name in case you want to match on those.
	masker := redact.New(redact.CommentMarker(md, func(table, column, comment string) bool {
		return strings.Contains(strings.ToLower(comment), "pii")
	}))

	// mask parses one plain SQL statement, masks its PII literals, and prints the
	// result as a complete statement. CompileInline renders the masked literals
	// inline (a masked value reads '*****') instead of as $N placeholders.
	mask := func(raw string) {
		q, err := postgres.ParseOne(raw)
		if err != nil {
			log.Fatal(err)
		}
		text, err := sql.CompileInline(sql.PostgreSQLDialect{}, masker.Apply(q))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(text)
	}

	// Mask a few statements written as plain SQL. Only the literals bound to a PII
	// column (email, full_name, phone) are replaced; country and id are untouched.
	// The masked output each call prints is shown above it.

	// SELECT * FROM "users" WHERE (("email" = '*****') AND ("country" = 'JP'))
	mask(`SELECT * FROM users WHERE email = 'alice@example.com' AND country = 'JP'`)

	// SELECT * FROM "users" WHERE ("full_name" IN ('*****', '*****'))
	mask(`SELECT * FROM users WHERE full_name IN ('Alice Smith', 'Bob Jones')`)

	// INSERT INTO "users" ("email", "full_name", "phone", "country") VALUES ('*****', '*****', '*****', 'JP')
	mask(`INSERT INTO users (email, full_name, phone, country) VALUES ('alice@example.com', 'Alice Smith', '555-1234', 'JP')`)

	// UPDATE "users" SET "full_name" = '*****', "country" = 'US' WHERE ("id" = 1)
	mask(`UPDATE users SET full_name = 'Alice Jones', country = 'US' WHERE id = 1`)
}
