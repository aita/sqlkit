package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// TestDistinct runs SELECT DISTINCT against both engines: a has-many join
// duplicates each base row once per child, and DISTINCT must collapse them back.
func TestDistinct(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// Join users to their posts (alice has 2, bob has 1), projecting only the
		// author id; without DISTINCT that yields three rows, with it just two.
		var ids []int64
		if err := db.Select(blogdb.Users.ID).From(blogdb.Users).
			Join(blogdb.Posts, blogdb.Users.ID.EqExpr(blogdb.Posts.UserID)).
			Distinct().
			OrderBy(blogdb.Users.ID.Asc()).
			All(ctx).Scan(&ids); err != nil {
			t.Fatalf("SELECT DISTINCT: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("distinct author ids = %#v, want 2", ids)
		}
	})
}

// TestDistinctOn covers PostgreSQL's DISTINCT ON, which keeps the first row per
// distinct key under the ORDER BY. MySQL has no DISTINCT ON, and the compiler
// rejects it at ToSQL time, so the test skips there rather than sending invalid
// SQL.
func TestDistinctOn(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if eng.name != "postgres" {
			t.Skipf("%s has no DISTINCT ON", eng.name)
		}
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)

		// One row per author, taking the alphabetically-last title (id breaks ties).
		// alice has "Draft"/"Hello" -> "Hello"; bob has "Bob's first".
		type latest struct {
			UserID int64 `sqlkit:"user_id"`
			Title  string
		}
		var rows []latest
		if err := db.Select(blogdb.Posts.UserID, blogdb.Posts.Title).From(blogdb.Posts).
			DistinctOn(blogdb.Posts.UserID).
			OrderBy(blogdb.Posts.UserID.Asc(), blogdb.Posts.Title.Desc()).
			All(ctx).Scan(&rows); err != nil {
			t.Fatalf("SELECT DISTINCT ON: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("distinct-on rows = %#v, want 2 (one per author)", rows)
		}
		got := map[int64]string{}
		for _, r := range rows {
			got[r.UserID] = r.Title
		}
		if got[aliceID] != "Hello" {
			t.Fatalf("alice latest = %q, want Hello", got[aliceID])
		}
		if got[bobID] != "Bob's first" {
			t.Fatalf("bob latest = %q, want Bob's first", got[bobID])
		}
	})
}
