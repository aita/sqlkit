package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

func TestPreloadToManyPopulatesSliceField(t *testing.T) {
	// The base query returns the three users; the preload follow-up returns the
	// posts. The fake serves each by inspecting the SQL.
	fake := &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if contains(s, `"public"."posts"`) {
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
					{int64(11), int64(1), "b"},
					{int64(12), int64(2), "c"},
				}}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("scanned users = %+v", users)
	}
	if len(users[0].Posts) != 2 || users[0].Posts[0].Title != "a" || users[0].Posts[1].Title != "b" {
		t.Fatalf("user 1 posts = %+v", users[0].Posts)
	}
	if len(users[1].Posts) != 1 || users[1].Posts[0].Title != "c" {
		t.Fatalf("user 2 posts = %+v", users[1].Posts)
	}
	if len(users[2].Posts) != 0 {
		t.Fatalf("user 3 should have no posts, got %+v", users[2].Posts)
	}
}

func TestPreloadToOnePopulatesPointerField(t *testing.T) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	var posts []prePost2
	if err := db.Select().From(Posts).Preload(Users).All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("scanned posts = %+v", posts)
	}
	if posts[0].User == nil || posts[0].User.Name != "Alice" {
		t.Fatalf("post 10 user = %+v", posts[0].User)
	}
	if posts[2].User == nil || posts[2].User.Name != "Alice" {
		t.Fatalf("post 12 user = %+v", posts[2].User)
	}
}

// Preload also runs for a single-row terminal (First), populating one parent.
func TestPreloadSingleStructViaFirst(t *testing.T) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	var post prePost2
	if err := db.Select().From(Posts).Preload(Users).First(context.Background()).Scan(&post); err != nil {
		t.Fatalf("Preload First error = %v", err)
	}
	if post.User == nil || post.User.Name != "Alice" {
		t.Fatalf("single post user = %+v", post.User)
	}
}

func TestPreloadEmptyParentsIsNoop(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id"}}, nil // no rows
	}}
	db := sqlkit.WrapDB(fake.DB())
	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload on empty result error = %v", err)
	}
}

func TestPreloadMissingFieldErrors(t *testing.T) {
	type noPostsUser struct {
		ID int64 `sqlkit:"id"`
	}
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())
	var users []noPostsUser
	err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users)
	if err == nil {
		t.Fatal("Preload should error when no destination field matches the relation")
	}
}

// Preload(target) errors when the base table has no relation reaching target.
func TestPreloadNoRelationErrors(t *testing.T) {
	db := newDB()
	// Users has a relation to Posts but not to itself.
	err := db.Select(Users.ID).From(Users).Preload(Users).All(context.Background()).Scan(&[]preUser{})
	if err == nil {
		t.Fatal("Preload to an unrelated table should error")
	}
}

// PreloadRel takes the relation by value, for disambiguation or hand-built relations.
func TestPreloadRelExplicit(t *testing.T) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	var posts []prePost2
	if err := db.Select().From(Posts).PreloadRel(PostsUser).All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("PreloadRel error = %v", err)
	}
	if len(posts) == 0 || posts[0].User == nil || posts[0].User.Name != "Alice" {
		t.Fatalf("PreloadRel did not populate: %+v", posts)
	}
}

// TestPreloadNested loads Users -> Posts -> (each post's) User in three batched
// queries and threads the grandchildren onto the value-type child slice.
func TestPreloadNested(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if contains(s, `"public"."posts"`) {
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"}, {int64(11), int64(2), "b"},
				}}, nil
			}
			// Both the base users query and the nested users follow-up land here.
			return testutil.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{
				{int64(1), "Alice"}, {int64(2), "Bob"},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []nestUser
	err := db.Select().From(Users).
		Preload(sqlkit.RelPath(Posts, Users)).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("nested Preload error = %v", err)
	}
	if len(users) != 2 || len(users[0].Posts) != 1 {
		t.Fatalf("scanned users = %+v", users)
	}
	post := users[0].Posts[0]
	if post.User == nil || post.User.Name != "Alice" {
		t.Fatalf("nested user not loaded onto child copy: %+v", post.User)
	}
	if users[1].Posts[0].User == nil || users[1].Posts[0].User.Name != "Bob" {
		t.Fatalf("nested user for second branch = %+v", users[1].Posts[0].User)
	}
}

// TestPreloadConditionalAppliesWhere checks a Preload condition narrows the
// follow-up SELECT while leaving the parent-key IN as the (still splittable)
// deferred filter.
func TestPreloadConditionalAppliesWhere(t *testing.T) {
	var postsSQL string
	fake := &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
		if contains(s, `"public"."posts"`) {
			postsSQL = s
			return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"},
			}}, nil
		}
		return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	err := db.Select(Users.ID).From(Users).
		Preload(Posts, Posts.Title.Eq("a")).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("conditional Preload error = %v", err)
	}
	if !contains(postsSQL, `"public"."posts"."title"`) || !contains(postsSQL, "IN") {
		t.Fatalf("conditional preload SQL missing condition or IN: %q", postsSQL)
	}
	if len(users) != 1 || len(users[0].Posts) != 1 || users[0].Posts[0].Title != "a" {
		t.Fatalf("scanned users = %+v", users)
	}
}

// A RelPath whose hops are not related defers an error.
func TestPreloadRelPathUnrelatedHopErrors(t *testing.T) {
	db := newDB()
	// Users relates to Posts, but Posts has no relation to Posts, so the second
	// hop cannot resolve.
	err := db.Select().From(Users).
		Preload(sqlkit.RelPath(Posts, Posts)).
		All(context.Background()).Scan(&[]nestUser{})
	if err == nil {
		t.Fatal("RelPath with an unrelated hop should defer an error")
	}
}

// TestPreloadRelPathReusesRegisteredLevel checks that a table named by both a
// plain Preload and a RelPath hop is loaded once, with the deeper hop nested under
// the existing spec rather than duplicated.
func TestPreloadRelPathReusesRegisteredLevel(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
		if contains(s, `"public"."posts"`) {
			return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"}, {int64(11), int64(2), "b"},
			}}, nil
		}
		return testutil.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{
			{int64(1), "Alice"}, {int64(2), "Bob"},
		}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []nestUser
	err := db.Select().From(Users).
		Preload(Posts).
		Preload(sqlkit.RelPath(Posts, Users)).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("RelPath reuse error = %v", err)
	}
	if n := countQueries(fake, `"public"."posts"`); n != 1 {
		t.Fatalf("posts queried %d times, want 1 (the RelPath hop should reuse the Preload(Posts) spec)", n)
	}
	if len(users) != 2 || len(users[0].Posts) != 1 || users[0].Posts[0].User == nil ||
		users[0].Posts[0].User.Name != "Alice" {
		t.Fatalf("nested user not loaded under reused Posts: %+v", users)
	}
}

// TestPreloadRelPathConditionOnFinalHop checks Preload conditions apply only to
// the last table on a RelPath, not the intermediate hops.
func TestPreloadRelPathConditionOnFinalHop(t *testing.T) {
	var postsSQL, nestedUsersSQL string
	fake := &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
		if contains(s, `"public"."posts"`) {
			postsSQL = s
			return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"},
			}}, nil
		}
		if contains(s, "IN") { // the nested users follow-up carries the parent-key IN
			nestedUsersSQL = s
		}
		return testutil.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{int64(1), "Alice"}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []nestUser
	err := db.Select().From(Users).
		Preload(sqlkit.RelPath(Posts, Users), Users.Email.Eq("z@x")).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("RelPath condition error = %v", err)
	}
	if contains(postsSQL, `"email"`) {
		t.Fatalf("condition leaked onto the intermediate Posts hop: %q", postsSQL)
	}
	if !contains(nestedUsersSQL, `"public"."users"."email"`) {
		t.Fatalf("condition missing on the final Users hop: %q", nestedUsersSQL)
	}
}

// TestPreloadRelSingleTable checks a one-element RelPath behaves like Preload(t).
func TestPreloadRelPathSingleTable(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
		if contains(s, `"public"."posts"`) {
			return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"},
			}}, nil
		}
		return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	if err := db.Select(Users.ID).From(Users).
		Preload(sqlkit.RelPath(Posts)).
		All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("single-table RelPath error = %v", err)
	}
	if len(users) != 1 || len(users[0].Posts) != 1 {
		t.Fatalf("single-table RelPath did not load posts: %+v", users)
	}
}

// TestPreloadEmptyRelPathErrors checks RelPath() defers an error.
func TestPreloadEmptyRelPathErrors(t *testing.T) {
	db := newDB()
	err := db.Select().From(Users).Preload(sqlkit.RelPath()).
		All(context.Background()).Scan(&[]preUser{})
	if err == nil {
		t.Fatal("Preload(RelPath()) should defer an error")
	}
}

// TestPreloadRelAppliesCondition checks PreloadRel takes conditions too.
func TestPreloadRelAppliesCondition(t *testing.T) {
	var usersSQL string
	fake := &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
		if contains(s, `"public"."users"`) {
			usersSQL = s
			return testutil.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{int64(1), "Alice"}}}, nil
		}
		return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
			{int64(10), int64(1), "a"},
		}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var posts []prePost2
	if err := db.Select().From(Posts).
		PreloadRel(PostsUser, Users.Email.Eq("a@x")).
		All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("PreloadRel condition error = %v", err)
	}
	if !contains(usersSQL, `"public"."users"."email"`) {
		t.Fatalf("PreloadRel condition not applied: %q", usersSQL)
	}
}

// TestPreloadRequiresFromTable checks Preload defers an error when the source is
// not a table (e.g. a subquery), so no relation can be resolved.
func TestPreloadRequiresFromTable(t *testing.T) {
	db := newDB()
	err := db.Select().From(sqlkit.Select().From(Users).SQLSource()).
		Preload(Posts).
		All(context.Background()).Scan(&[]preUser{})
	if err == nil {
		t.Fatal("Preload without a From table should defer an error")
	}
}

// TestPreloadResolvesFieldByTag covers relationField's tag branch: the relation
// rows land in the tagged field even though it is not named after the relation.
func TestPreloadResolvesFieldByTag(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if contains(s, `"public"."posts"`) {
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
				}}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []taggedUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(users) != 1 || len(users[0].Authored) != 1 || users[0].Authored[0].Title != "a" {
		t.Fatalf("tagged field not populated: %+v", users)
	}
}

// TestPreloadToManyNonSliceFieldErrors covers the guard that a to-many relation
// requires a slice destination field.
func TestPreloadToManyNonSliceFieldErrors(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []toManyScalarUser
	err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users)
	if err == nil || !strings.Contains(err.Error(), "must be a slice") {
		t.Fatalf("Preload error = %v, want a 'must be a slice' rejection", err)
	}
}

// TestPreloadSkipsNullParentKey covers scalarValue: a parent whose key is nil is
// skipped, while parents with a non-nil key are still loaded.
func TestPreloadSkipsNullParentKey(t *testing.T) {
	var postsQueried int
	fake := &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if contains(s, `"public"."posts"`) {
				postsQueried++
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
				}}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {nil},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []nullableKeyUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("scanned users = %d, want 2", len(users))
	}
	// First user (id=1) got its post; the nil-key user got none.
	if len(users[0].Posts) != 1 || users[0].Posts[0].Title != "a" {
		t.Fatalf("user 1 posts = %+v", users[0].Posts)
	}
	if len(users[1].Posts) != 0 {
		t.Fatalf("nil-key user should have no posts, got %+v", users[1].Posts)
	}
}

// TestPreloadThroughSessionUsesItsChunkSize covers Session.preloadChunkSize: a
// session inherits the database's WithPreloadBatchSize, so a preload over more
// distinct parent keys than the batch size runs several follow-up IN queries.
func TestPreloadThroughSessionUsesItsChunkSize(t *testing.T) {
	var postsQueries int
	fake := &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if contains(s, `"public"."posts"`) {
				postsQueries++
				return testutil.Result{Columns: []string{"id", "user_id", "title"}}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithPreloadBatchSize(2))
	session := db.NewSession()
	defer session.Close()

	var users []preUser
	if err := session.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	// 3 distinct parent keys, batch size 2 => ceil(3/2) = 2 follow-up queries.
	if postsQueries != 2 {
		t.Fatalf("posts follow-up queries = %d, want 2 (chunked by the session's preload batch size)", postsQueries)
	}
}
