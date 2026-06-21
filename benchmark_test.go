package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// Benchmarks for the hot paths a production workload exercises on every request:
// compiling a query to SQL, scanning rows into structs, executing a write,
// encoding/decoding a pagination cursor, and assembling a preloaded relation.
//
// They make latency and allocations of these paths measurable and
// regression-trackable (e.g. with benchstat) — the "no feature is quietly slow"
// guarantee. They use the in-memory testutil.Fake driver, so they measure
// sqlkit's own work (build, compile, scan, assemble), not a real database.

// BenchmarkSelectCompile measures building a representative SELECT chain and
// compiling it to SQL — the path every read goes through before it reaches the
// driver.
func BenchmarkSelectCompile(b *testing.B) {
	db := newDB()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := db.Select(Users.ID, Users.Email).From(Users).
			Where(Users.ID.Gt(0), Users.Name.IsNotNull()).
			OrderBy(Users.Email.Asc(), Users.ID.Desc()).
			Limit(20)
		if _, _, err := q.ToSQL(pg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelectScan measures a full read through the fake driver and the
// reflection-based scan of the result rows into a slice of caller structs.
func BenchmarkSelectScan(b *testing.B) {
	db := sqlkit.WrapDB(usersFake().DB())
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var users []MyUserDTO
		if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsertExec measures building and executing an INSERT, including the
// struct-to-column mapping of the values.
func BenchmarkInsertExec(b *testing.B) {
	db := sqlkit.WrapDB((&testutil.Fake{}).DB())
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Insert(Users).Values(insertDTO{Mail: "x@example.com"}).Exec(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPaginate measures fetching a keyset page from a cursor: decoding the
// cursor, augmenting the query with the cursor predicate and ordering, executing
// it, and scanning the page — the per-page cost of cursor pagination.
func BenchmarkPaginate(b *testing.B) {
	db := sqlkit.WrapDB(usersFake().DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Email), sqlkit.KeyAsc(Users.ID)).Limit(2)
	var seed []MyUserDTO
	first, err := pager.Paginate(context.Background(), db.Select().From(Users), &seed, sqlkit.Start())
	if err != nil {
		b.Fatal(err)
	}
	next := first.Next()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var users []MyUserDTO
		if _, err := pager.Paginate(ctx, db.Select().From(Users), &users, next); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPreload measures the eager-load path: the base query, the batched
// follow-up SELECT, and the reflection that assigns related rows back onto the
// destination structs.
func BenchmarkPreload(b *testing.B) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var posts []prePost2
		if err := db.Select().From(Posts).Preload(Users).All(ctx).Scan(&posts); err != nil {
			b.Fatal(err)
		}
	}
}
