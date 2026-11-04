package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// These tests cover the to-one Preload directions against the real engines: a
// belongs-to (posts -> their author) and a has-one (users -> their profile). The
// has-many direction is exercised by TestPreloadSplitEndToEnd; here the
// follow-up query keys the single related row back onto each parent's pointer
// field, which the engines must return in a shape the scanner can match. Preload
// projects every column of the related table, so the destination structs mirror
// the full row shape.

type postWithUser struct {
	ID        int64
	UserID    int64
	Title     string
	Body      *string
	Published bool
	User      *userRow
}

func TestPreloadBelongsTo(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)

		var posts []postWithUser
		if err := db.Select().From(blogdb.Posts).
			Preload(blogdb.Users).
			OrderBy(blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&posts); err != nil {
			t.Fatalf("preload belongs-to: %v", err)
		}
		if len(posts) != 3 {
			t.Fatalf("posts = %d, want 3", len(posts))
		}
		for _, p := range posts {
			if p.User == nil {
				t.Fatalf("post %q has no preloaded author", p.Title)
			}
			if p.User.ID != p.UserID {
				t.Fatalf("post %q author id = %d, want %d", p.Title, p.User.ID, p.UserID)
			}
			switch p.UserID {
			case aliceID:
				if p.User.Email != "alice@example.com" {
					t.Fatalf("post %q author = %q, want alice", p.Title, p.User.Email)
				}
			case bobID:
				if p.User.Email != "bob@example.com" {
					t.Fatalf("post %q author = %q, want bob", p.Title, p.User.Email)
				}
			default:
				t.Fatalf("unexpected author id %d", p.UserID)
			}
		}
	})
}

type userWithProfile struct {
	userRow
	Profile *profileRow
}

func TestPreloadHasOne(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)
		// Only alice gets a profile; bob is left without one so the preload has to
		// leave his pointer nil rather than inventing a zero row.
		insertProfile(t, db, profileRow{UserID: aliceID, Bio: ptr("hello")})

		var users []userWithProfile
		if err := db.Select().From(blogdb.Users).
			Preload(blogdb.Profiles).
			OrderBy(blogdb.Users.Email.Asc()).
			All(ctx).Scan(&users); err != nil {
			t.Fatalf("preload has-one: %v", err)
		}
		if len(users) != 2 {
			t.Fatalf("users = %d, want 2", len(users))
		}
		byEmail := map[string]userWithProfile{}
		for _, u := range users {
			byEmail[u.Email] = u
		}
		alice := byEmail["alice@example.com"]
		if alice.Profile == nil || alice.Profile.UserID != aliceID {
			t.Fatalf("alice profile = %#v, want one for %d", alice.Profile, aliceID)
		}
		if alice.Profile.Bio == nil || *alice.Profile.Bio != "hello" {
			t.Fatalf("alice bio = %#v, want hello", alice.Profile.Bio)
		}
		if bob := byEmail["bob@example.com"]; bob.Profile != nil {
			t.Fatalf("bob profile = %#v, want nil (no profile seeded)", bob.Profile)
		}
	})
}
