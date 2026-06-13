// Command blog demonstrates the typed query builder and DTO scanning against
// the bindings generated from the catalog in schema/. The go:generate
// directives below regenerate the blogdb bindings and schema.sql from that
// catalog with the sqlkit generate and ddl commands (no hand-written
// generator program); run them with `go generate ./...` from this directory.
//
// Run against a real database:
//
//	createdb blog && psql blog -f schema.sql
//	DATABASE_URL=postgres://localhost/blog go run .
package main

//go:generate go run github.com/aita/sqlkit/cmd/sqlkit generate ./schema --name blogdb --out blogdb --relations
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit ddl ./schema --out schema.sql

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/sql"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("set DATABASE_URL to a PostgreSQL connection string")
	}
	db, err := sqlkit.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := run(context.Background(), db); err != nil {
		log.Fatal(err)
	}
}

// User and Post are caller-defined DTOs; sqlkit generates only the table
// bindings (blogdb.Users, blogdb.Posts), and fields map by name.
type User struct {
	ID        int64
	Email     string
	Name      *string
	CreatedAt time.Time
}

type Post struct {
	UserID    int64
	Title     string
	Published bool
}

func run(ctx context.Context, db *sqlkit.Database) error {
	// WithSession runs the callback in one transaction: commit on nil,
	// rollback on error or panic.
	return db.WithSession(func(session *sqlkit.Session) error {

		// INSERT ... RETURNING; Scan recovers database-generated values.
		var inserted []User
		if err := session.Insert(blogdb.Users).Values(User{Email: "alice@example.com"}).
			Returning().
			All(ctx).Scan(&inserted); err != nil {
			return err
		}
		alice := inserted[0]
		fmt.Printf("inserted user id=%d created_at=%s\n", alice.ID, alice.CreatedAt)

		if _, err := session.Insert(blogdb.Posts).Values([]Post{
			{UserID: alice.ID, Title: "Hello, sqlkit"},
			{UserID: alice.ID, Title: "Window functions", Published: true},
		}).Exec(ctx); err != nil {
			return err
		}

		// Typed lookup by primary key; blogdb.Posts.Title.Eq(42) would not compile.
		// The bindings are metadata, not an ORM: queries go through the builder.
		var same User
		if err := session.Select().From(blogdb.Users).
			Where(blogdb.Users.ID.Eq(alice.ID)).
			Limit(1).All(ctx).Scan(&same); err != nil {
			return fmt.Errorf("get user %d: %w", alice.ID, err)
		}
		fmt.Printf("loaded %s\n", same.Email)

		// Build a typed query, scan into a caller-defined DTO.
		type PostSummary struct {
			ID    int64
			Title string
		}
		var summaries []PostSummary
		if err := session.Select(blogdb.Posts.ID, blogdb.Posts.Title).From(blogdb.Posts).
			Join(blogdb.Users, blogdb.Posts.UserID.EqExpr(blogdb.Users.ID)).
			Where(blogdb.Users.Email.Eq("alice@example.com")).
			OrderBy(blogdb.Posts.ID.Desc()).
			All(ctx).Scan(&summaries); err != nil {
			return err
		}
		fmt.Printf("alice has %d posts\n", len(summaries))

		drafts, err := session.Select().From(blogdb.Posts).
			Where(blogdb.Posts.Published.Eq(false)).
			Count(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%d drafts\n", drafts)

		// Aggregates, CASE, window functions compose via the sql package and scan
		// into an ad hoc struct.
		type Stat struct {
			Email     string
			PostCount int64 `sqlkit:"post_count"`
			Rank      int64
		}
		var stats []Stat
		query := sql.Select(
			sql.Col("u.email"),
			sql.As(sql.Count(sql.Col("p.id")), "post_count"),
			sql.As(sql.Rank().Over().OrderBy(sql.Desc(sql.Raw("COUNT(p.id)"))), "rank"),
		).
			From(sql.SourceAlias(sql.Table{Name: "users"}, "u")).
			LeftJoin(sql.SourceAlias(sql.Table{Name: "posts"}, "p"), sql.Eq(sql.Col("p.user_id"), sql.Col("u.id"))).
			GroupBy(sql.Col("u.email")).
			Having(sql.Ge(sql.CountAll(), sql.Int(0)))
		if err := session.RawSQL(ctx, query).Scan(&stats); err != nil {
			return err
		}
		for _, s := range stats {
			fmt.Printf("#%d %s: %d posts\n", s.Rank, s.Email, s.PostCount)
		}

		// Relation-aware read: JoinRel takes the Profile relation and derives the
		// ON clause from the foreign key, so the caller never writes the join
		// condition. The terminal returns sqlkit.Rows, scanned into a
		// caller-defined DTO.
		var publicAuthors []User
		if err := blogdb.Users.Q(session).
			JoinRel(blogdb.Users.Rels.Profile).
			Where(blogdb.Users.Rels.Profile.Public.Eq(true)).
			OrderBy(blogdb.Users.CreatedAt.Desc()).
			Limit(50).
			All(ctx).Scan(&publicAuthors); err != nil {
			return err
		}
		fmt.Printf("%d authors with a public profile\n", len(publicAuthors))

		return nil
	})
}
