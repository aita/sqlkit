package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
	"github.com/aita/sqlkit/sql"
)

// TestMaterializedView covers PostgreSQL's materialized views: CreateMaterializedView
// over a builder query, REFRESH after the underlying data changes, and a SELECT
// reading the cached rows. MySQL has no materialized views, so it skips.
func TestMaterializedView(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if eng.name != "postgres" {
			t.Skipf("%s has no materialized views", eng.name)
		}
		ctx := context.Background()
		seedBlog(t, db, eng)

		// The view body must carry no bound parameters (a matview is a stored
		// definition), so the published filter is a raw predicate rather than a
		// value-binding comparison.
		body := db.Select(
			sql.As(blogdb.Posts.UserID, "user_id"),
			sql.As(sql.CountAll(), "n"),
		).From(blogdb.Posts).
			Where(sqlkit.Cond(sql.Raw("published"))).
			GroupBy(blogdb.Posts.UserID)

		view := sql.Tbl("published_counts")
		_, _ = db.Exec(ctx, sql.DropMaterializedView(view).IfExists())
		if _, err := db.Exec(ctx, sql.CreateMaterializedView(view, body)); err != nil {
			t.Fatalf("create materialized view: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.Exec(context.Background(), sql.DropMaterializedView(view).IfExists())
		})

		// Initially two authors have a published post (alice, bob).
		total, err := countView(ctx, db)
		if err != nil {
			t.Fatalf("count view: %v", err)
		}
		if total != 2 {
			t.Fatalf("view rows = %d, want 2", total)
		}

		// Publishing a fresh author's post does not change the view until REFRESH.
		eve := insertUser(t, db, eng, userRow{Email: "eve@example.com"})
		if _, err := db.Insert(blogdb.Posts).
			Values(postRow{UserID: eve, Title: "Eve's hello", Published: true}).
			Exec(ctx); err != nil {
			t.Fatalf("insert eve post: %v", err)
		}
		if total, err = countView(ctx, db); err != nil || total != 2 {
			t.Fatalf("stale view rows = %d, %v; want 2 before refresh", total, err)
		}

		if _, err := db.Exec(ctx, sql.RefreshMaterializedView(view)); err != nil {
			t.Fatalf("refresh materialized view: %v", err)
		}
		if total, err = countView(ctx, db); err != nil || total != 3 {
			t.Fatalf("refreshed view rows = %d, %v; want 3", total, err)
		}
	})
}

func countView(ctx context.Context, db *sqlkit.Database) (int64, error) {
	var n int64
	err := db.SQL().QueryRowContext(ctx, "SELECT count(*) FROM published_counts").Scan(&n)
	return n, err
}
