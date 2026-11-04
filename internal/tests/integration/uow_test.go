package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
	"github.com/aita/sqlkit/internal/integration/schema"
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
		err = db.WithSession(ctx, func(s *sqlkit.Session) error {
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

		err := db.WithSession(ctx, func(s *sqlkit.Session) error {
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

// uowDocsTable is a minimal hand-written binding for a table with a JSON
// column, used only by TestUnitOfWorkJSONColumnBatchedUpdate.
type uowDocsTable struct {
	info sqlkit.TableInfo
	ID   sqlkit.Column[int64]
	Meta sqlkit.Column[[]byte]
}

func newUOWDocs() uowDocsTable {
	info := sqlkit.TableInfo{Schema: testSchema, Name: "uow_docs"}
	return uowDocsTable{info: info, ID: sqlkit.NewColumn[int64](info, "id"), Meta: sqlkit.NewColumn[[]byte](info, "meta")}
}
func (t uowDocsTable) Info() sqlkit.TableInfo             { return t.info }
func (t uowDocsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t uowDocsTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Meta} }
func (t uowDocsTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t uowDocsTable) Defaulted() []sqlkit.AnyColumn      { return nil }

type uowDoc struct {
	ID   int64
	Meta map[string]any `sqlkit:"meta,json"`
}

// TestUnitOfWorkJSONColumnBatchedUpdate flushes changes to a json-tagged column
// on several managed rows with batching on. A batched flush merges single-PK
// updates into one VALUES-join UPDATE, where an opaque JSON parameter has no
// type the engine can infer; the uow must fall back to per-row UPDATEs for such
// values so the flush succeeds and the JSON lands intact.
func TestUnitOfWorkJSONColumnBatchedUpdate(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		jsonType := "JSONB"
		if eng.name == "mysql" {
			jsonType = "JSON"
		}
		table := db.Dialect().QuoteIdent(testSchema + ".uow_docs")
		if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().ExecContext(ctx, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, meta "+jsonType+" NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.SQL().ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })
		docs := newUOWDocs()
		if _, err := db.Insert(docs).Values([]uowDoc{{ID: 1, Meta: map[string]any{"v": 1}}, {ID: 2, Meta: map[string]any{"v": 2}}}).Exec(ctx); err != nil {
			t.Fatalf("seed: %v", err)
		}

		err := db.WithSession(ctx, func(s *sqlkit.Session) error {
			scope := uow.NewScope(s, uow.WithBatchSize(10))
			var rows []uowDoc
			if err := scope.Find(ctx, docs, &rows); err != nil {
				return err
			}
			for i := range rows {
				rows[i].Meta["v"] = rows[i].ID * 10
			}
			return scope.Flush(ctx)
		})
		if err != nil {
			t.Fatalf("flush: %v", err)
		}

		var got []uowDoc
		if err := db.Select().From(docs).OrderBy(docs.ID.Asc()).All(ctx).Scan(&got); err != nil {
			t.Fatalf("read back: %v", err)
		}
		for _, d := range got {
			if v, _ := d.Meta["v"].(float64); int64(v) != d.ID*10 {
				t.Errorf("doc %d meta = %v, want v=%d", d.ID, d.Meta, d.ID*10)
			}
		}
	})
}
