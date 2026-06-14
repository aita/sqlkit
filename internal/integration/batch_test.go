package integration

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
)

// These tests drive the auto-chunking feature end to end against the real
// engines: queries with a small per-query AutoChunk(ChunkSize) must split bulk
// INSERTs and large IN lists into several statements yet produce the same result
// as an unsplit run. A counting interceptor confirms the split actually happened
// (more than one statement reached the database), and the row counts confirm it
// stayed correct.

// stmtCounter counts the query and exec statements that reach the database,
// including each split chunk (interceptors wrap every statement actually sent).
type stmtCounter struct {
	sqlkit.NoopInterceptor
	queries int
	execs   int
}

func (c *stmtCounter) InterceptQuery(ctx context.Context, stmt sqlkit.Statement, next sqlkit.QueryHandler) (*stdsql.Rows, error) {
	c.queries++
	return next(ctx, stmt)
}

func (c *stmtCounter) InterceptExec(ctx context.Context, stmt sqlkit.Statement, next sqlkit.ExecHandler) (stdsql.Result, error) {
	c.execs++
	return next(ctx, stmt)
}

func TestBindParamSplittingEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		// A handle over the same pool that counts how many statements actually run.
		// Each query below carries a small per-query AutoChunk(ChunkSize), forcing
		// both the bulk insert and the IN lists to span several statements.
		counter := &stmtCounter{}
		sdb := sqlkit.WrapDB(db.SQL(),
			sqlkit.WithDialect(db.Dialect()),
			sqlkit.WithAutoChunk(true),
			sqlkit.WithInterceptor(counter))

		const n = 50
		users := make([]userRow, n)
		for i := range users {
			users[i] = userRow{Email: fmt.Sprintf("user%03d@example.com", i)}
		}

		// Bulk INSERT splits into several statements but inserts every row.
		execBefore := counter.execs
		if _, err := sdb.Insert(blogdb.Users).Values(users).AutoChunk(sqlkit.ChunkSize(5)).Exec(ctx); err != nil {
			t.Fatalf("bulk insert: %v", err)
		}
		if got := counter.execs - execBefore; got < 2 {
			t.Fatalf("bulk insert ran %d statements, want it split into several", got)
		}
		total, err := blogdb.Users.Q(sdb).Count(ctx)
		if err != nil {
			t.Fatalf("count after bulk insert: %v", err)
		}
		if total != n {
			t.Fatalf("rows after bulk insert = %d, want %d", total, n)
		}

		// Collect the generated ids for the IN-list cases.
		var ids []int64
		if err := sdb.Select(blogdb.Users.ID).From(blogdb.Users).
			OrderBy(blogdb.Users.ID.Asc()).All(ctx).Scan(&ids); err != nil {
			t.Fatalf("select ids: %v", err)
		}
		if len(ids) != n {
			t.Fatalf("collected %d ids, want %d", len(ids), n)
		}

		// SELECT ... WHERE id IN (all) splits across statements and concatenates
		// the rows back into one result.
		queryBefore := counter.queries
		var got []userRow
		if err := sdb.Select().From(blogdb.Users).
			Where(blogdb.Users.ID.In(ids...)).
			AutoChunk(sqlkit.ChunkSize(10)).
			All(ctx).Scan(&got); err != nil {
			t.Fatalf("select IN: %v", err)
		}
		if q := counter.queries - queryBefore; q < 2 {
			t.Fatalf("select IN ran %d queries, want it split into several", q)
		}
		if len(got) != n {
			t.Fatalf("select IN returned %d rows, want %d", len(got), n)
		}
		seen := map[int64]bool{}
		for _, u := range got {
			seen[u.ID] = true
		}
		if len(seen) != n {
			t.Fatalf("select IN returned %d distinct ids, want %d", len(seen), n)
		}

		// UPDATE ... WHERE id IN (all) splits, runs atomically, and its result
		// sums RowsAffected across the chunks.
		res, err := sdb.Update(blogdb.Users).
			Set(blogdb.Users.Name.Set(ptr("renamed"))).
			Where(blogdb.Users.ID.In(ids...)).
			AutoChunk(sqlkit.ChunkSize(9)).
			Exec(ctx)
		if err != nil {
			t.Fatalf("update IN: %v", err)
		}
		if affected, err := res.RowsAffected(); err != nil {
			t.Fatalf("update RowsAffected: %v", err)
		} else if affected != n {
			t.Fatalf("update affected %d rows, want %d", affected, n)
		}
		renamed, err := blogdb.Users.Q(sdb).Where(blogdb.Users.Name.Eq(ptr("renamed"))).Count(ctx)
		if err != nil {
			t.Fatalf("count renamed: %v", err)
		}
		if renamed != n {
			t.Fatalf("renamed rows = %d, want %d", renamed, n)
		}

		// DELETE ... WHERE id IN (half) splits and removes exactly that half.
		half := ids[:n/2]
		res, err = sdb.Delete(blogdb.Users).Where(blogdb.Users.ID.In(half...)).AutoChunk(sqlkit.ChunkSize(10)).Exec(ctx)
		if err != nil {
			t.Fatalf("delete IN: %v", err)
		}
		if affected, err := res.RowsAffected(); err != nil {
			t.Fatalf("delete RowsAffected: %v", err)
		} else if affected != int64(len(half)) {
			t.Fatalf("delete affected %d rows, want %d", affected, len(half))
		}
		remaining, err := blogdb.Users.Q(sdb).Count(ctx)
		if err != nil {
			t.Fatalf("count after delete: %v", err)
		}
		if remaining != int64(n-len(half)) {
			t.Fatalf("rows after delete = %d, want %d", remaining, n-len(half))
		}
	})
}

func TestInBatchesEndToEnd(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		sdb := sqlkit.WrapDB(db.SQL(),
			sqlkit.WithDialect(db.Dialect()))

		const n = 30
		users := make([]userRow, n)
		for i := range users {
			users[i] = userRow{Email: fmt.Sprintf("batch%03d@example.com", i)}
		}
		if _, err := sdb.Insert(blogdb.Users).Values(users).Exec(ctx); err != nil {
			t.Fatalf("bulk insert: %v", err)
		}

		var ids []int64
		if err := sdb.Select(blogdb.Users.ID).From(blogdb.Users).All(ctx).Scan(&ids); err != nil {
			t.Fatalf("select ids: %v", err)
		}

		// InBatches splits the value slice (explicit chunk size 8) and concatenates
		// each chunk's rows.
		var out []userRow
		err := sqlkit.InBatches(ctx, sdb, ids, func(chunk []int64) sqlkit.Rows {
			return sdb.Select().From(blogdb.Users).Where(blogdb.Users.ID.In(chunk...)).All(ctx)
		}, &out, 8)
		if err != nil {
			t.Fatalf("InBatches: %v", err)
		}
		if len(out) != n {
			t.Fatalf("InBatches returned %d rows, want %d", len(out), n)
		}
	})
}
