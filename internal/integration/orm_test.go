package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/orm"
)

// These tests exercise the generated relation-aware ORM layer (the QuerySet
// reached through blogdb.Users.Q(db) / .Queryer(db)) end to end against the real
// engines. The rest of the suite drives the same bindings through the typed
// builder (db.Select/Insert/…); here the entry point is the generated ORM and
// its relation metadata, which nothing else in the suite executes.

// profileRow is a test-defined DTO for the profiles table; like userRow/postRow
// it lives with the tests since blogdb emits only the table bindings.
type profileRow struct {
	ID     int64
	UserID int64
	Bio    *string
	Public bool
}

// insertProfile inserts a profile row. The id is database-generated and unused,
// so a plain Exec suffices on both engines.
func insertProfile(t *testing.T, db *sqlkit.Database, p profileRow) {
	t.Helper()
	if _, err := db.Insert(blogdb.Profiles).Values(p).Exec(context.Background()); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
}

func TestORMQuerySetRead(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// One over a unique predicate, through the generated QuerySet.
		var alice userRow
		if err := blogdb.Users.Q(db).
			Where(blogdb.Users.Email.Eq("alice@example.com")).
			One(ctx).Scan(&alice); err != nil {
			t.Fatalf("Q().One(): %v", err)
		}
		if alice.ID != aliceID || alice.Name == nil || *alice.Name != "Alice" {
			t.Fatalf("alice = %#v", alice)
		}

		// Get by primary key.
		var byPK userRow
		if err := blogdb.Users.Q(db).Get(ctx, aliceID).Scan(&byPK); err != nil {
			t.Fatalf("Q().Get(): %v", err)
		}
		if byPK.Email != "alice@example.com" {
			t.Fatalf("Get(%d) = %#v", aliceID, byPK)
		}

		// A missing row surfaces ErrNoRows at the terminal.
		var missing userRow
		err := blogdb.Users.Q(db).Where(blogdb.Users.ID.Eq(999999)).First(ctx).Scan(&missing)
		if !errors.Is(err, sqlkit.ErrNoRows) {
			t.Fatalf("First(missing) = %v; want ErrNoRows", err)
		}

		// Count over the whole base table.
		total, err := blogdb.Users.Q(db).Count(ctx)
		if err != nil {
			t.Fatalf("Q().Count(): %v", err)
		}
		if total != 2 {
			t.Fatalf("user count = %d, want 2", total)
		}

		// Ordering with a LIMIT, then a paginated window over the same order.
		var firstByEmail []userRow
		if err := blogdb.Users.Q(db).
			OrderBy(blogdb.Users.Email.Asc()).
			Limit(1).
			All(ctx).Scan(&firstByEmail); err != nil {
			t.Fatalf("Q().OrderBy().Limit(): %v", err)
		}
		if len(firstByEmail) != 1 || firstByEmail[0].Email != "alice@example.com" {
			t.Fatalf("first by email = %#v, want only alice", firstByEmail)
		}

		var second []userRow
		if err := blogdb.Users.Q(db).
			OrderBy(blogdb.Users.Email.Asc()).
			Page(ctx, 1, 1).Scan(&second); err != nil {
			t.Fatalf("Q().Page(): %v", err)
		}
		if len(second) != 1 || second[0].Email != "bob@example.com" {
			t.Fatalf("second page row = %#v, want bob", second)
		}
	})
}

func TestORMQuerySetFilter(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// Filter bundle: a struct whose set fields map to columns by name and hold
		// FilterTerm values. A bool field carries orm.Eq so a false comparison is
		// not skipped as a zero value.
		publishedFilter := struct {
			Published orm.FilterTerm
		}{Published: orm.Eq(true)}
		published, err := blogdb.Posts.Q(db).Filter(publishedFilter).Count(ctx)
		if err != nil {
			t.Fatalf("Filter(struct).Count(): %v", err)
		}
		if published != 2 {
			t.Fatalf("published via Filter = %d, want 2", published)
		}

		// Filter also accepts a bare predicate.
		hello, err := blogdb.Posts.Q(db).Filter(blogdb.Posts.Title.Eq("Hello")).Count(ctx)
		if err != nil {
			t.Fatalf("Filter(predicate).Count(): %v", err)
		}
		if hello != 1 {
			t.Fatalf("Hello via Filter = %d, want 1", hello)
		}
	})
}

func TestORMQuerySetJoins(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// belongs-to INNER JOIN: the ON clause is derived from the foreign key, so
		// the caller filters on the joined table without writing it.
		var alicePosts []postRow
		if err := blogdb.Posts.Q(db).
			Join(blogdb.Posts.Rels.User).
			Where(blogdb.Posts.Rels.User.Email.Eq("alice@example.com")).
			OrderBy(blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&alicePosts); err != nil {
			t.Fatalf("belongs-to Join(): %v", err)
		}
		if len(alicePosts) != 2 || alicePosts[0].Title != "Draft" || alicePosts[1].Title != "Hello" {
			t.Fatalf("alice posts via relation join = %#v", alicePosts)
		}

		// has-many INNER JOIN counted through a subquery.
		publishedAuthors, err := blogdb.Users.Q(db).
			Join(blogdb.Users.Rels.Posts).
			Where(blogdb.Users.Rels.Posts.Published.Eq(true)).
			Count(ctx)
		if err != nil {
			t.Fatalf("has-many Join().Count(): %v", err)
		}
		if publishedAuthors != 2 {
			t.Fatalf("user/published-post pairs = %d, want 2", publishedAuthors)
		}

		// DISTINCT collapses the duplicate base rows a has-many join produces:
		// both seeded users have at least one post.
		distinctAuthors, err := blogdb.Users.Q(db).
			Join(blogdb.Users.Rels.Posts).
			Distinct().
			Count(ctx)
		if err != nil {
			t.Fatalf("Join().Distinct().Count(): %v", err)
		}
		if distinctAuthors != 2 {
			t.Fatalf("distinct authors with posts = %d, want 2", distinctAuthors)
		}

		// to-one join: give alice a public profile, leave bob without one.
		insertProfile(t, db, profileRow{UserID: aliceID, Public: true})

		// INNER JOIN on the to-one relation, filtered on the joined table.
		var publicAuthor userRow
		if err := blogdb.Users.Q(db).
			Join(blogdb.Users.Rels.Profile).
			Where(blogdb.Users.Rels.Profile.Public.Eq(true)).
			One(ctx).Scan(&publicAuthor); err != nil {
			t.Fatalf("to-one Join().One(): %v", err)
		}
		if publicAuthor.ID != aliceID {
			t.Fatalf("public author = %#v, want alice (%d)", publicAuthor, aliceID)
		}

		// LEFT JOIN keeps users without a profile (bob), so all users remain.
		withProfile, err := blogdb.Users.Q(db).
			LeftJoin(blogdb.Users.Rels.Profile).
			Count(ctx)
		if err != nil {
			t.Fatalf("LeftJoin().Count(): %v", err)
		}
		if withProfile != 2 {
			t.Fatalf("users after LEFT JOIN profile = %d, want 2", withProfile)
		}
	})
}

func TestORMQuerySetWrite(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// Insert through the QuerySet entry point (database-defaulted id/created_at
		// are omitted just as with db.Insert).
		if _, err := blogdb.Users.Q(db).Insert().
			Values(userRow{Email: "eve@example.com"}).
			Exec(ctx); err != nil {
			t.Fatalf("Q().Insert(): %v", err)
		}
		total, err := blogdb.Users.Q(db).Count(ctx)
		if err != nil {
			t.Fatalf("count after insert: %v", err)
		}
		if total != 3 {
			t.Fatalf("user count after insert = %d, want 3", total)
		}

		// Update through the QuerySet entry point, then reload via Get.
		if _, err := blogdb.Users.Q(db).Update().
			Set(blogdb.Users.Name.Set(ptr("Updated"))).
			Where(blogdb.Users.ID.Eq(aliceID)).
			Exec(ctx); err != nil {
			t.Fatalf("Q().Update(): %v", err)
		}
		var reloaded userRow
		if err := blogdb.Users.Q(db).Get(ctx, aliceID).Scan(&reloaded); err != nil {
			t.Fatalf("reload after update: %v", err)
		}
		if reloaded.Name == nil || *reloaded.Name != "Updated" {
			t.Fatalf("after update = %#v", reloaded)
		}

		// Delete the drafts through the QuerySet entry point.
		if _, err := blogdb.Posts.Q(db).Delete().
			Where(blogdb.Posts.Published.Eq(false)).
			Exec(ctx); err != nil {
			t.Fatalf("Q().Delete(): %v", err)
		}
		remaining, err := blogdb.Posts.Q(db).Count(ctx)
		if err != nil {
			t.Fatalf("count after delete: %v", err)
		}
		if remaining != 2 {
			t.Fatalf("posts after delete = %d, want 2", remaining)
		}
	})
}
