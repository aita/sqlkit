package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// TestFilterRelationExists drives the relation-aware Filter bundles against the
// real engines. A nested filter on a relation compiles to a correlated EXISTS
// over the related table; the unit tests assert the SQL, and this confirms the
// engines evaluate the correlation to the right rows in both directions of the
// foreign key (belongs-to and has-many).
func TestFilterRelationExists(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// belongs-to: posts whose author (the User relation) has alice's email.
		// Compiles to WHERE EXISTS (SELECT ... FROM users WHERE users.id = posts.user_id AND email = ?).
		var alicePosts []postRow
		if err := blogdb.Posts.Q(db).
			Filter(blogdb.PostsFilter{
				User: &blogdb.UsersFilter{Email: sqlkit.Eq("alice@example.com")},
			}).
			OrderBy(blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&alicePosts); err != nil {
			t.Fatalf("belongs-to relation filter: %v", err)
		}
		if len(alicePosts) != 2 {
			t.Fatalf("alice's posts via relation filter = %d, want 2", len(alicePosts))
		}
		for _, p := range alicePosts {
			if p.UserID != aliceID {
				t.Fatalf("post %#v not authored by alice (%d)", p, aliceID)
			}
		}

		// has-many: users who have at least one published post. Compiles to a
		// correlated EXISTS over posts in the other direction of the key.
		var publishedAuthors []userRow
		if err := blogdb.Users.Q(db).
			Filter(blogdb.UsersFilter{
				Posts: &blogdb.PostsFilter{Published: sqlkit.Eq(true)},
			}).
			OrderBy(blogdb.Users.Email.Asc()).
			All(ctx).Scan(&publishedAuthors); err != nil {
			t.Fatalf("has-many relation filter: %v", err)
		}
		if len(publishedAuthors) != 2 {
			t.Fatalf("published authors via relation filter = %d, want 2 (alice, bob)", len(publishedAuthors))
		}

		// Combine a column term and a relation term: alice must also have a
		// published post (she does: "Hello").
		hits, err := blogdb.Users.Q(db).
			Filter(blogdb.UsersFilter{
				Email: sqlkit.Eq("alice@example.com"),
				Posts: &blogdb.PostsFilter{Published: sqlkit.Eq(true)},
			}).
			Count(ctx)
		if err != nil {
			t.Fatalf("combined filter count: %v", err)
		}
		if hits != 1 {
			t.Fatalf("alice-with-published count = %d, want 1", hits)
		}
	})
}
