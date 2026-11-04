package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// TestCursorPagination drives the keyset paginator against the real engines.
// The seek predicate and the limit+1 probe it builds have to order and compare
// correctly on each engine, so walking forward then backward over a known set
// confirms pages neither overlap nor drop rows. A second key (id) breaks ties so
// the order is total and the cursor round-trips deterministically.
func TestCursorPagination(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		const n = 7
		for i := 0; i < n; i++ {
			insertUser(t, db, eng, userRow{Email: fmt.Sprintf("page%02d@example.com", i)})
		}

		pager := sqlkit.NewPaginator(
			sqlkit.KeyAsc(blogdb.Users.Email),
			sqlkit.KeyAsc(blogdb.Users.ID),
		).Limit(3)

		// Walk forward, collecting emails page by page. Three pages of 3/3/1 cover
		// all seven rows in order with no repeats.
		var forward []string
		cur := sqlkit.Start()
		pages := 0
		for {
			var batch []userRow
			page, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &batch, cur)
			if err != nil {
				t.Fatalf("paginate forward: %v", err)
			}
			pages++
			for _, u := range batch {
				forward = append(forward, u.Email)
			}
			if !page.HasNext {
				break
			}
			cur = page.Next()
			if pages > n {
				t.Fatal("forward pagination did not terminate")
			}
		}
		if pages != 3 {
			t.Fatalf("forward pages = %d, want 3 (3+3+1)", pages)
		}
		if len(forward) != n {
			t.Fatalf("forward rows = %d (%v), want %d", len(forward), forward, n)
		}
		for i := 0; i < n; i++ {
			want := fmt.Sprintf("page%02d@example.com", i)
			if forward[i] != want {
				t.Fatalf("forward[%d] = %q, want %q", i, forward[i], want)
			}
		}

		// From the start of the last page, stepping backward must yield the page
		// before it (rows 3,4,5), in forward order.
		var lastPage []userRow
		page, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &lastPage, sqlkit.Start())
		if err != nil {
			t.Fatalf("paginate first: %v", err)
		}
		if !page.HasNext {
			t.Fatal("first page should have a next")
		}
		var second []userRow
		page2, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &second, page.Next())
		if err != nil {
			t.Fatalf("paginate second: %v", err)
		}
		if !page2.HasPrev {
			t.Fatal("second page should have a prev")
		}
		var back []userRow
		if _, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &back, page2.Prev()); err != nil {
			t.Fatalf("paginate backward: %v", err)
		}
		if len(back) != 3 {
			t.Fatalf("backward page rows = %d, want 3", len(back))
		}
		for i := 0; i < 3; i++ {
			want := fmt.Sprintf("page%02d@example.com", i)
			if back[i].Email != want {
				t.Fatalf("backward[%d] = %q, want %q", i, back[i].Email, want)
			}
		}
	})
}
