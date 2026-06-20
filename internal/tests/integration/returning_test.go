package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/sql"
)

// TestReturning covers the RETURNING clause on INSERT, UPDATE, and DELETE. It
// runs only on engines that support RETURNING; MySQL is skipped (it reads
// generated keys through Result.LastInsertId instead, exercised elsewhere).
func TestReturning(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if !eng.supportsReturning {
			t.Skipf("%s has no RETURNING", eng.name)
		}
		ctx := context.Background()

		// INSERT ... RETURNING yields the database-generated id and created_at.
		var inserted []userRow
		if err := db.Insert(blogdb.Users).Values(userRow{Email: "ret@example.com"}).
			Returning().
			All(ctx).Scan(&inserted); err != nil {
			t.Fatalf("insert returning: %v", err)
		}
		if len(inserted) != 1 || inserted[0].ID == 0 || inserted[0].CreatedAt.IsZero() {
			t.Fatalf("insert returning = %#v", inserted)
		}
		id := inserted[0].ID

		// UPDATE ... RETURNING a single column.
		var names []*string
		if err := db.Update(blogdb.Users).
			Set(blogdb.Users.Name.Set(ptr("Returned"))).
			Where(blogdb.Users.ID.Eq(id)).
			Returning(blogdb.Users.Name).
			All(ctx).Scan(&names); err != nil {
			t.Fatalf("update returning: %v", err)
		}
		if len(names) != 1 || names[0] == nil || *names[0] != "Returned" {
			t.Fatalf("update returning = %#v", names)
		}

		// DELETE ... RETURNING the deleted id.
		var deleted []int64
		if err := db.Delete(blogdb.Users).
			Where(blogdb.Users.ID.Eq(id)).
			Returning(blogdb.Users.ID).
			All(ctx).Scan(&deleted); err != nil {
			t.Fatalf("delete returning: %v", err)
		}
		if len(deleted) != 1 || deleted[0] != id {
			t.Fatalf("delete returning = %#v, want [%d]", deleted, id)
		}
	})
}

// TestSetOperations covers INTERSECT and EXCEPT. They are kept PostgreSQL-only
// here: MySQL gained them in 8.0.31, but the suite targets the broader MySQL 8
// baseline, so portable tests stick to UNION (see TestUnion).
func TestSetOperations(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if eng.name != "postgres" {
			t.Skipf("INTERSECT/EXCEPT not exercised on %s", eng.name)
		}
		ctx := context.Background()
		seedBlog(t, db, eng)

		// EXCEPT: published post titles that are not drafts (i.e. all published).
		published := db.Select(blogdb.Posts.Title).From(blogdb.Posts).Where(blogdb.Posts.Published.Eq(true))
		drafts := db.Select(blogdb.Posts.Title).From(blogdb.Posts).Where(blogdb.Posts.Published.Eq(false))

		var only []string
		if err := published.Except(drafts).OrderBy(sql.Asc(sql.Col("title"))).All(ctx).Scan(&only); err != nil {
			t.Fatalf("except: %v", err)
		}
		if len(only) != 2 {
			t.Fatalf("except titles = %#v, want 2", only)
		}

		// INTERSECT of all titles with published titles is the published set. A
		// compound query's ORDER BY refers to output columns, not table-qualified
		// ones.
		allTitles := db.Select(blogdb.Posts.Title).From(blogdb.Posts)
		var common []string
		if err := allTitles.Intersect(db.Select(blogdb.Posts.Title).From(blogdb.Posts).Where(blogdb.Posts.Published.Eq(true))).
			OrderBy(sql.Asc(sql.Col("title"))).All(ctx).Scan(&common); err != nil {
			t.Fatalf("intersect: %v", err)
		}
		if len(common) != 2 {
			t.Fatalf("intersect titles = %#v, want 2", common)
		}
	})
}
