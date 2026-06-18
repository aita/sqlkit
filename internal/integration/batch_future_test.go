package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/uow"
)

// These tests drive the splitting added on top of #34 against the real engines:
// preload IN chunking, RETURNING from split writes, and the uow flush batching
// (chunked DELETE and VALUES-join UPDATE).

// userWithPosts scans a user id plus its eager-loaded posts (the "Posts"
// relation), so a Preload over many parents exercises the follow-up IN chunking.
type userWithPosts struct {
	ID    int64 `sqlkit:"id"`
	Posts []postRow
}

func TestPreloadSplitEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		const n = 25
		users := make([]userRow, n)
		for i := range users {
			users[i] = userRow{Email: fmt.Sprintf("pre%03d@example.com", i)}
		}
		if _, err := db.Insert(blogdb.Users).Values(users).Exec(ctx); err != nil {
			t.Fatalf("insert users: %v", err)
		}
		var ids []int64
		if err := db.Select(blogdb.Users.ID).From(blogdb.Users).All(ctx).Scan(&ids); err != nil {
			t.Fatalf("select ids: %v", err)
		}
		posts := make([]postRow, len(ids))
		for i, id := range ids {
			posts[i] = postRow{UserID: id, Title: fmt.Sprintf("p%03d", i)}
		}
		if _, err := db.Insert(blogdb.Posts).Values(posts).Exec(ctx); err != nil {
			t.Fatalf("insert posts: %v", err)
		}

		// A small preload batch size forces the preload's follow-up IN list to span
		// several statements; the counter confirms it actually split.
		counter := &stmtCounter{}
		sdb := sqlkit.WrapDB(db.SQL(),
			sqlkit.WithDialect(db.Dialect()),
			sqlkit.WithAutoChunk(true),
			sqlkit.WithPreloadBatchSize(8),
			sqlkit.WithHooks(counter.hooks()))

		var loaded []userWithPosts
		before := counter.queries
		if err := sdb.Select(blogdb.Users.ID).From(blogdb.Users).
			Preload(blogdb.Posts).All(ctx).Scan(&loaded); err != nil {
			t.Fatalf("preload: %v", err)
		}
		// Base query plus several preload chunks (25 keys at a batch size of 8).
		if q := counter.queries - before; q < 3 {
			t.Fatalf("preload ran %d queries, want the follow-up split into several", q)
		}
		total := 0
		for _, u := range loaded {
			total += len(u.Posts)
		}
		if total != n {
			t.Fatalf("preloaded %d posts, want %d", total, n)
		}
	})
}

func TestReturningSplitEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if !eng.supportsReturning {
			t.Skipf("%s has no RETURNING", eng.name)
		}
		ctx := context.Background()

		counter := &stmtCounter{}
		sdb := sqlkit.WrapDB(db.SQL(),
			sqlkit.WithDialect(db.Dialect()),
			sqlkit.WithHooks(counter.hooks()))

		const n = 30
		users := make([]userRow, n)
		for i := range users {
			users[i] = userRow{Email: fmt.Sprintf("ret%03d@example.com", i)}
		}

		var out []userRow
		before := counter.queries
		if err := sdb.Insert(blogdb.Users).Values(users).AutoChunk(sqlkit.ChunkSize(5)).Returning().All(ctx).Scan(&out); err != nil {
			t.Fatalf("insert returning: %v", err)
		}
		if q := counter.queries - before; q < 2 {
			t.Fatalf("insert returning ran %d statements, want it split", q)
		}
		if len(out) != n {
			t.Fatalf("returned %d rows, want %d (concatenated)", len(out), n)
		}
		seen := map[int64]bool{}
		for _, u := range out {
			if u.ID == 0 || u.CreatedAt.IsZero() {
				t.Fatalf("returned row missing generated fields: %#v", u)
			}
			seen[u.ID] = true
		}
		if len(seen) != n {
			t.Fatalf("returned %d distinct ids, want %d", len(seen), n)
		}
	})
}

func TestUowBatchedFlushEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		const n = 12
		seed := make([]userRow, n)
		for i := range seed {
			seed[i] = userRow{Email: fmt.Sprintf("uowb%03d@example.com", i)}
		}
		if _, err := db.Insert(blogdb.Users).Values(seed).Exec(ctx); err != nil {
			t.Fatalf("seed users: %v", err)
		}

		// A small uow batch size makes the flush batch its writes: a VALUES-join
		// UPDATE and chunked DELETEs, both split to stay within it.
		counter := &stmtCounter{}
		lim := sqlkit.WrapDB(db.SQL(),
			sqlkit.WithDialect(db.Dialect()),
			sqlkit.WithHooks(counter.hooks()))

		// Bulk UPDATE: load all, rename each in place, flush as VALUES-join chunks.
		updBefore := counter.execs
		err := lim.WithSession(func(s *sqlkit.Session) error {
			scope := uow.NewScope(s, uow.WithBatchSize(2))
			var loaded []userRow
			if err := scope.Find(ctx, blogdb.Users, &loaded); err != nil {
				return err
			}
			for i := range loaded {
				loaded[i].Name = ptr(fmt.Sprintf("name%03d", i))
			}
			return scope.Flush(ctx)
		})
		if err != nil {
			t.Fatalf("uow bulk update: %v", err)
		}
		if e := counter.execs - updBefore; e < 2 {
			t.Fatalf("bulk update ran %d statements, want chunked", e)
		}
		var renamed []userRow
		if err := lim.Select().From(blogdb.Users).All(ctx).Scan(&renamed); err != nil {
			t.Fatalf("select after update: %v", err)
		}
		if len(renamed) != n {
			t.Fatalf("rows = %d, want %d", len(renamed), n)
		}
		for _, u := range renamed {
			if u.Name == nil {
				t.Fatalf("name not updated for id %d", u.ID)
			}
		}

		// Chunked DELETE: remove every row through the scope.
		delBefore := counter.execs
		err = lim.WithSession(func(s *sqlkit.Session) error {
			scope := uow.NewScope(s, uow.WithBatchSize(2))
			for _, u := range renamed {
				if err := scope.Remove(blogdb.Users, u); err != nil {
					return err
				}
			}
			return scope.Flush(ctx)
		})
		if err != nil {
			t.Fatalf("uow batched delete: %v", err)
		}
		if e := counter.execs - delBefore; e < 2 {
			t.Fatalf("batched delete ran %d statements, want chunked", e)
		}
		remaining, err := blogdb.Users.Q(lim).Count(ctx)
		if err != nil {
			t.Fatalf("count after delete: %v", err)
		}
		if remaining != 0 {
			t.Fatalf("rows after delete = %d, want 0", remaining)
		}
	})
}
