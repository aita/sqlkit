package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
	"github.com/aita/sqlkit/sql"
)

// These tests exercise the subquery surfaces against the real engines: a
// correlated EXISTS / NOT EXISTS, an IN (subquery), a correlated scalar subquery
// in the projection, and a derived table in the FROM clause. Each shape compiles
// to nested SELECTs whose placeholder numbering and correlation the engine has to
// resolve, so running them confirms more than the unit oracles can.

func TestExistsSubquery(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)
		// carol has no posts, so EXISTS must exclude her and NOT EXISTS include her.
		carolID := insertUser(t, db, eng, userRow{Email: "carol@example.com"})

		hasPost := db.Select(sql.Raw("1")).From(blogdb.Posts).
			Where(blogdb.Posts.UserID.EqExpr(blogdb.Users.ID))

		var authors []int64
		if err := db.Select(blogdb.Users.ID).From(blogdb.Users).
			Where(sqlkit.Exists(hasPost)).
			OrderBy(blogdb.Users.ID.Asc()).
			All(ctx).Scan(&authors); err != nil {
			t.Fatalf("EXISTS: %v", err)
		}
		if len(authors) != 2 || authors[0] != aliceID || authors[1] != bobID {
			t.Fatalf("EXISTS authors = %#v, want alice and bob", authors)
		}

		var idle []int64
		if err := db.Select(blogdb.Users.ID).From(blogdb.Users).
			Where(sqlkit.NotExists(hasPost)).
			All(ctx).Scan(&idle); err != nil {
			t.Fatalf("NOT EXISTS: %v", err)
		}
		if len(idle) != 1 || idle[0] != carolID {
			t.Fatalf("NOT EXISTS authors = %#v, want only carol (%d)", idle, carolID)
		}
	})
}

func TestInSubquery(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)

		// Authors of a published post, selected through IN (subquery).
		publishedAuthors := db.Select(blogdb.Posts.UserID).From(blogdb.Posts).
			Where(blogdb.Posts.Published.Eq(true))

		var emails []string
		if err := db.Select(blogdb.Users.Email).From(blogdb.Users).
			Where(blogdb.Users.ID.InQuery(publishedAuthors)).
			OrderBy(blogdb.Users.Email.Asc()).
			All(ctx).Scan(&emails); err != nil {
			t.Fatalf("IN (subquery): %v", err)
		}
		if len(emails) != 2 || emails[0] != "alice@example.com" || emails[1] != "bob@example.com" {
			t.Fatalf("IN subquery emails = %#v", emails)
		}
		_ = aliceID
		_ = bobID
	})
}

func TestScalarSubquery(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)

		// Correlated scalar subquery in the projection: each user's post count.
		postCount := db.Select(sql.CountAll()).From(blogdb.Posts).
			Where(blogdb.Posts.UserID.EqExpr(blogdb.Users.ID))

		type row struct {
			ID int64 `sqlkit:"id"`
			N  int64 `sqlkit:"n"`
		}
		var rows []row
		if err := db.Select(blogdb.Users.ID, sql.As(sqlkit.SubExpr[int64](postCount), "n")).
			From(blogdb.Users).
			OrderBy(blogdb.Users.ID.Asc()).
			All(ctx).Scan(&rows); err != nil {
			t.Fatalf("scalar subquery: %v", err)
		}
		counts := map[int64]int64{}
		for _, r := range rows {
			counts[r.ID] = r.N
		}
		if counts[aliceID] != 2 || counts[bobID] != 1 {
			t.Fatalf("post counts = %#v, want alice=2 bob=1", counts)
		}
	})
}

func TestDerivedTable(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// A derived table (subquery in FROM): per-author post counts, then filter
		// the derived result for authors with more than one post.
		sub := db.Select(
			sql.As(blogdb.Posts.UserID, "user_id"),
			sql.As(sql.CountAll(), "n"),
		).From(blogdb.Posts).
			GroupBy(blogdb.Posts.UserID).
			As("counts")

		var ns []int64
		if err := db.Select(sub.Col("n")).From(sub).
			Where(sqlkit.Cond(sql.Gt(sub.Col("n"), sql.Int(1)))).
			All(ctx).Scan(&ns); err != nil {
			t.Fatalf("derived table: %v", err)
		}
		if len(ns) != 1 || ns[0] != 2 {
			t.Fatalf("derived table counts = %#v, want only alice's 2", ns)
		}
	})
}
