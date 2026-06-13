package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/examples/blog/schema"
	"github.com/aita/sqlkit/uow"
)

// TestUnitOfWorkTracking exercises the tracking Unit of Work end to end: a row
// loaded through the scope is updated in place (only the changed column is
// written), a new row is inserted with its generated key written back, and a row
// is removed — all flushed in one transaction. The identity map built from the
// catalog also proves the codegen-baked checksum equals meta.Table.Key().
func TestUnitOfWorkTracking(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		model, err := schema.Catalog.Build()
		if err != nil {
			t.Fatalf("build catalog: %v", err)
		}
		identities, err := model.Identities()
		if err != nil {
			t.Fatalf("identities: %v", err)
		}

		carol := &userRow{Email: "carol@example.com", Name: ptr("Carol")}
		var draftID int64
		err = db.WithSession(func(s *sqlkit.Session) error {
			scope := uow.NewScope(s, uow.WithIdentities(identities))

			// Load Alice through the scope (managed) and rename her in place.
			var alice userRow
			found, err := scope.Get(ctx, blogdb.Users, &alice, aliceID)
			if err != nil || !found {
				t.Fatalf("Get alice = %v, %v", found, err)
			}
			alice.Name = ptr("Alice Updated")

			// New user: pointer Add gets its id/created_at written back.
			if err := scope.Add(blogdb.Users, carol); err != nil {
				return err
			}

			// Remove Alice's draft post by primary key.
			var draft postRow
			if err := db.Select(blogdb.Posts.ID).From(blogdb.Posts).
				Where(blogdb.Posts.Title.Eq("Draft")).One(ctx).Scan(&draft); err != nil {
				return err
			}
			draftID = draft.ID
			if err := scope.Remove(blogdb.Posts, draft); err != nil {
				return err
			}

			return scope.Flush(ctx)
		})
		if err != nil {
			t.Fatalf("unit of work: %v", err)
		}

		// The generated primary key is always written back. Other DB-defaulted
		// columns (created_at) come back only via RETURNING; MySQL recovers just
		// the auto-increment key through LastInsertId.
		if carol.ID == 0 {
			t.Fatalf("carol primary key not written back: %#v", carol)
		}
		if eng.supportsReturning && carol.CreatedAt.IsZero() {
			t.Fatalf("carol created_at not written back via RETURNING: %#v", carol)
		}

		var reloaded userRow
		if err := db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(aliceID)).
			One(ctx).Scan(&reloaded); err != nil {
			t.Fatalf("reload alice: %v", err)
		}
		if reloaded.Name == nil || *reloaded.Name != "Alice Updated" {
			t.Fatalf("alice after update = %#v", reloaded)
		}

		var gone postRow
		err = db.Select().From(blogdb.Posts).Where(blogdb.Posts.ID.Eq(draftID)).First(ctx).Scan(&gone)
		if !errorsIsNoRows(err) {
			t.Fatalf("draft still present (err=%v)", err)
		}
	})
}

// TestUnitOfWorkNestedSavepoint verifies a nested scope rolls back to its
// savepoint without discarding the parent scope's flushed work.
func TestUnitOfWorkNestedSavepoint(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		err := db.WithSession(func(s *sqlkit.Session) error {
			scope := uow.NewScope(s)

			// Parent work: insert dave.
			if err := scope.Add(blogdb.Users, userRow{Email: "dave@example.com"}); err != nil {
				return err
			}
			if err := scope.Flush(ctx); err != nil {
				return err
			}

			// Nested scope: insert erin, then roll the nested work back.
			child := scope.NewScope()
			if err := child.Add(blogdb.Users, userRow{Email: "erin@example.com"}); err != nil {
				return err
			}
			if err := child.Flush(ctx); err != nil {
				return err
			}
			return child.Rollback(ctx)
		})
		if err != nil {
			t.Fatalf("unit of work: %v", err)
		}

		// dave (parent) survived; erin (nested, rolled back) did not.
		if got := countUser(t, db, "dave@example.com"); got != 1 {
			t.Fatalf("dave count = %d, want 1 (parent work kept)", got)
		}
		if got := countUser(t, db, "erin@example.com"); got != 0 {
			t.Fatalf("erin count = %d, want 0 (nested work rolled back)", got)
		}
	})
}

func countUser(t *testing.T, db *sqlkit.Database, email string) int64 {
	t.Helper()
	n, err := blogdb.Users.Q(db).Where(blogdb.Users.Email.Eq(email)).Count(context.Background())
	if err != nil {
		t.Fatalf("count %q: %v", email, err)
	}
	return n
}

func errorsIsNoRows(err error) bool { return errors.Is(err, sqlkit.ErrNoRows) }
