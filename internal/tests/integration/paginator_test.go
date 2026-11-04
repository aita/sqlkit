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

// TestCursorPaginationNullableKey pages over a nullable key (users.name) on the
// real engines. PostgreSQL and MySQL disagree on where NULLs sort by default and
// a plain "name > ?" never matches NULL, so a naive keyset walk drops or
// repeats the NULL-named rows; the paginator must visit every row exactly once,
// forward and backward, in both key directions.
func TestCursorPaginationNullableKey(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		names := []*string{ptr("b"), nil, ptr("a"), nil, ptr("c"), nil, ptr("a")}
		for i, name := range names {
			insertUser(t, db, eng, userRow{Email: fmt.Sprintf("null%02d@example.com", i), Name: name})
		}

		for _, tc := range []struct {
			name string
			key  sqlkit.PageKey
		}{
			{"asc", sqlkit.KeyAsc(blogdb.Users.Name)},
			{"desc", sqlkit.KeyDesc(blogdb.Users.Name)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				pager := sqlkit.NewPaginator(tc.key, sqlkit.KeyAsc(blogdb.Users.ID)).Limit(2)

				// Forward walk: every row exactly once.
				var forward []int64
				var pagesSeen []sqlkit.Page
				cur := sqlkit.Start()
				for i := 0; ; i++ {
					if i > len(names) {
						t.Fatal("forward pagination did not terminate")
					}
					var batch []userRow
					page, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &batch, cur)
					if err != nil {
						t.Fatalf("paginate forward: %v", err)
					}
					for _, u := range batch {
						forward = append(forward, u.ID)
					}
					pagesSeen = append(pagesSeen, page)
					if !page.HasNext {
						break
					}
					cur = page.Next()
				}
				seen := map[int64]int{}
				for _, id := range forward {
					seen[id]++
				}
				if len(forward) != len(names) || len(seen) != len(names) {
					t.Fatalf("forward visited %v, want each of the %d rows exactly once", forward, len(names))
				}

				// Backward walk from the last page reproduces the same order.
				var backward []int64
				cur = pagesSeen[len(pagesSeen)-1].Prev()
				var lastPage []userRow
				if _, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &lastPage, pagesSeen[len(pagesSeen)-2].Next()); err != nil {
					t.Fatalf("paginate last: %v", err)
				}
				for _, u := range lastPage {
					backward = append(backward, u.ID)
				}
				for i := 0; ; i++ {
					if i > len(names) {
						t.Fatal("backward pagination did not terminate")
					}
					var batch []userRow
					page, err := pager.Paginate(ctx, db.Select().From(blogdb.Users), &batch, cur)
					if err != nil {
						t.Fatalf("paginate backward: %v", err)
					}
					ids := make([]int64, 0, len(batch))
					for _, u := range batch {
						ids = append(ids, u.ID)
					}
					backward = append(ids, backward...)
					if !page.HasPrev {
						break
					}
					cur = page.Prev()
				}
				if fmt.Sprint(backward) != fmt.Sprint(forward) {
					t.Fatalf("backward order %v != forward order %v", backward, forward)
				}
			})
		}
	})
}
