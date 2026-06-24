package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// foldUser folds its has-many Posts relation in a single joined query.
type foldUser struct {
	ID    int64 `sqlkit:"id"`
	Name  *string
	Posts []Post
}

// foldAuthor and foldPost exercise a to-one fold (posts → users).
type foldAuthor struct {
	ID   int64 `sqlkit:"id"`
	Name *string
}

type foldPost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
	User   *foldAuthor
}

// usersFoldPostsFake serves one LEFT JOIN users→posts result: Alice has two
// posts, Bob one, Carol none (the joined post columns are NULL).
func usersFoldPostsFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name", "id", "user_id", "title"},
				Rows: [][]driver.Value{
					{int64(1), "a@x", "Alice", int64(10), int64(1), "a"},
					{int64(1), "a@x", "Alice", int64(12), int64(1), "c"},
					{int64(2), "b@x", "Bob", int64(11), int64(2), "b"},
					{int64(3), "c@x", "Carol", nil, nil, nil},
				},
			}, nil
		},
	}
}

func TestFoldRelToMany(t *testing.T) {
	db := sqlkit.WrapDB(usersFoldPostsFake().DB())
	ctx := context.Background()

	var users []foldUser
	if err := db.Select().From(Users).FoldRel(UsersPosts).
		All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(users) != 3 {
		t.Fatalf("got %d users, want 3: %#v", len(users), users)
	}
	if users[0].ID != 1 || users[0].Name == nil || *users[0].Name != "Alice" {
		t.Fatalf("users[0] = %#v", users[0])
	}
	if got := titles(users[0].Posts); got != "a,c" {
		t.Fatalf("Alice posts = %q, want \"a,c\"", got)
	}
	if got := titles(users[1].Posts); got != "b" {
		t.Fatalf("Bob posts = %q, want \"b\"", got)
	}
	if len(users[2].Posts) != 0 {
		t.Fatalf("Carol posts = %#v, want none", users[2].Posts)
	}
	// The folded post rows carry their own columns.
	if users[0].Posts[1].ID != 12 || users[0].Posts[1].UserID != 1 || users[0].Posts[1].Title != "c" {
		t.Fatalf("Alice second post = %#v", users[0].Posts[1])
	}
}

func TestFoldResolvesRelationFromTarget(t *testing.T) {
	db := sqlkit.WrapDB(usersFoldPostsFake().DB())
	ctx := context.Background()

	var users []foldUser
	// Fold(Posts) resolves the users→posts relation from the target table.
	if err := db.Select().From(Users).Fold(Posts).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(users) != 3 || titles(users[0].Posts) != "a,c" {
		t.Fatalf("Fold(Posts) = %#v", users)
	}
}

func TestFoldRelToOne(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "user_id", "title", "id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(10), int64(1), "a", int64(1), "a@x", "Alice"},
					{int64(11), int64(2), "b", int64(2), "b@x", "Bob"},
					{int64(13), int64(99), "z", nil, nil, nil}, // no matching user
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	var posts []foldPost
	if err := db.Select().From(Posts).FoldRel(PostsUser).
		All(ctx).Scan(&posts); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("got %d posts, want 3", len(posts))
	}
	if posts[0].User == nil || posts[0].User.ID != 1 || posts[0].User.Name == nil || *posts[0].User.Name != "Alice" {
		t.Fatalf("posts[0].User = %#v", posts[0].User)
	}
	if posts[1].User == nil || posts[1].User.ID != 2 {
		t.Fatalf("posts[1].User = %#v", posts[1].User)
	}
	if posts[2].User != nil {
		t.Fatalf("posts[2].User = %#v, want nil (no matching user)", posts[2].User)
	}
}

func TestFoldRelRejectsExplicitColumns(t *testing.T) {
	db := newDB()
	var users []foldUser
	err := db.Select(Users.ID).From(Users).FoldRel(UsersPosts).
		All(context.Background()).Scan(&users)
	if err == nil || !contains(err.Error(), "no explicit columns") {
		t.Fatalf("error = %v, want explicit-columns rejection", err)
	}
}

func titles(posts []Post) string {
	out := ""
	for i, p := range posts {
		if i > 0 {
			out += ","
		}
		out += p.Title
	}
	return out
}
