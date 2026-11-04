package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// foldUserPosts folds the has-many Posts relation in a single joined query.
type foldUserPosts struct {
	ID    int64 `sqlkit:"id"`
	Email string
	Posts []postRow
}

// foldUserNested / foldPostWithAuthor / foldAuthorRow fold users → posts →
// author, where the author hop returns to the users table and is aliased.
type foldUserNested struct {
	ID    int64 `sqlkit:"id"`
	Posts []foldPostWithAuthor
}

type foldPostWithAuthor struct {
	ID    int64 `sqlkit:"id"`
	Title string
	User  *foldAuthorRow
}

type foldAuthorRow struct {
	ID    int64 `sqlkit:"id"`
	Email string
}

// TestFoldToManyEndToEnd folds users → posts in one query against a real engine
// and checks each parent carries its own children (and a childless parent an
// empty slice).
func TestFoldToManyEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)
		insertUser(t, db, eng, userRow{Email: "carol@example.com"}) // no posts

		var users []foldUserPosts
		if err := db.Select().From(blogdb.Users).
			FoldRel(blogdb.Users.Rels.Posts).
			OrderBy(blogdb.Users.ID.Asc(), blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&users); err != nil {
			t.Fatalf("fold users→posts: %v", err)
		}

		if len(users) != 3 {
			t.Fatalf("folded %d users, want 3: %#v", len(users), users)
		}
		if users[0].Email != "alice@example.com" || len(users[0].Posts) != 2 {
			t.Fatalf("alice = %#v", users[0])
		}
		if users[0].Posts[0].Title != "Draft" || users[0].Posts[1].Title != "Hello" {
			t.Fatalf("alice posts = %#v", users[0].Posts)
		}
		if users[1].Email != "bob@example.com" || len(users[1].Posts) != 1 {
			t.Fatalf("bob = %#v", users[1])
		}
		if users[2].Email != "carol@example.com" || len(users[2].Posts) != 0 {
			t.Fatalf("carol = %#v, want no posts", users[2])
		}
	})
}

// TestFoldNestedSelfReferenceEndToEnd folds users → posts → author, where the
// author hop is the users table again and so is joined under an alias. The author
// of every folded post is the top-level user, which is the invariant checked.
func TestFoldNestedSelfReferenceEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		var users []foldUserNested
		if err := db.Select().From(blogdb.Users).
			Fold(sqlkit.RelPath(blogdb.Posts, blogdb.Users)).
			OrderBy(blogdb.Users.ID.Asc(), blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&users); err != nil {
			t.Fatalf("nested self-reference fold: %v", err)
		}

		if len(users) != 2 {
			t.Fatalf("folded %d users, want 2: %#v", len(users), users)
		}
		seen := 0
		for _, u := range users {
			for _, p := range u.Posts {
				seen++
				if p.User == nil {
					t.Fatalf("post %d has no folded author", p.ID)
				}
				if p.User.ID != u.ID {
					t.Fatalf("post %d author = %d, want its user %d", p.ID, p.User.ID, u.ID)
				}
			}
		}
		if seen != 3 {
			t.Fatalf("folded %d posts across users, want 3", seen)
		}
	})
}
