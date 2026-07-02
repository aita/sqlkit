package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// TestPreloadInChunksByBindParamLimit drives a Preload whose follow-up IN list
// exceeds the limit, so it splits into several statements whose rows concatenate.
func TestPreloadInChunksByBindParamLimit(t *testing.T) {
	postQueries := 0
	fake := &testutil.Fake{
		QueryFunc: func(s string, args []any) (testutil.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				// One post per IN value, its user_id equal to that value, so every
				// parent matches exactly one child regardless of chunk membership.
				rows := make([][]driver.Value, 0, len(args))
				for i, a := range args {
					rows = append(rows, []driver.Value{int64(1000 + i), a, "t"})
				}
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: rows}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithPreloadBatchSize(2))

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	// Five distinct parent keys, preload batch size 2 => the follow-up splits 2+2+1.
	if postQueries != 3 {
		t.Fatalf("preload posts queries = %d, want 3 (2+2+1 keys)", postQueries)
	}
	if len(users) != 5 {
		t.Fatalf("users = %d, want 5", len(users))
	}
	for _, u := range users {
		if len(u.Posts) != 1 || u.Posts[0].UserID != u.ID {
			t.Fatalf("user %d posts = %+v, want one post with user_id %d", u.ID, u.Posts, u.ID)
		}
	}
}

func TestPreloadWithinLimitIsSingleQuery(t *testing.T) {
	postQueries := 0
	fake := &testutil.Fake{
		QueryFunc: func(s string, args []any) (testutil.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: nil}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	if postQueries != 1 {
		t.Fatalf("preload posts queries = %d, want 1 (keys fit the limit)", postQueries)
	}
}

// returningFake returns one row per inserted/affected row in a chunk, derived
// from the chunk's bind-argument count.
func returningFake(rowsPerChunk func(args []any) int) *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(_ string, args []any) (testutil.Result, error) {
			n := rowsPerChunk(args)
			rows := make([][]driver.Value, 0, n)
			for i := 0; i < n; i++ {
				rows = append(rows, []driver.Value{int64(i + 1), "x@example.com", nil})
			}
			return testutil.Result{Columns: []string{"id", "email", "name"}, Rows: rows}, nil
		},
	}
}

func TestInsertReturningSplitsAndConcatenates(t *testing.T) {
	// Two mapped columns (email, name) => ChunkSize 2 allows 2 rows per statement.
	fake := returningFake(func(args []any) int { return len(args) / 2 })
	db := sqlkit.WrapDB(fake.DB())

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	var out []User
	if err := db.Insert(Users).Values(rows).AutoChunk(sqlkit.ChunkSize(2)).Returning().All(context.Background()).Scan(&out); err != nil {
		t.Fatalf("insert returning error = %v", err)
	}
	queries := fake.Queries()
	if len(queries) != 3 {
		t.Fatalf("returning queries = %d, want 3 (2+2+1 rows)", len(queries))
	}
	for _, q := range queries {
		if !strings.Contains(q.SQL, "RETURNING") {
			t.Fatalf("chunk missing RETURNING: %q", q.SQL)
		}
	}
	if len(out) != 5 {
		t.Fatalf("returned rows = %d, want 5 (concatenated)", len(out))
	}
	// The chunks run atomically in one writable (not read-only) transaction.
	opts := fake.BeginOptions()
	if len(opts) != 1 {
		t.Fatalf("begins = %d, want 1 (one write tx)", len(opts))
	}
	if opts[0].ReadOnly {
		t.Fatal("RETURNING write tx must not be read-only")
	}
}

func TestInsertReturningWithinLimitIsSingleQuery(t *testing.T) {
	fake := returningFake(func(args []any) int { return len(args) / 2 })
	db := sqlkit.WrapDB(fake.DB())

	rows := []User{{Email: "a"}, {Email: "b"}}
	var out []User
	if err := db.Insert(Users).Values(rows).Returning().All(context.Background()).Scan(&out); err != nil {
		t.Fatalf("insert returning error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (rows fit the limit)", n)
	}
	if fake.Begins() != 0 {
		t.Fatalf("begins = %d, want 0 (single statement, no tx)", fake.Begins())
	}
	if len(out) != 2 {
		t.Fatalf("returned rows = %d, want 2", len(out))
	}
}

// TestRoutedInsertReturningSplitStaysAtomic guards a routed (per-statement
// WithRoute) bulk RETURNING write against losing its atomic transaction when
// split: the hint wraps the executor in a routedBinding, which batchExecutor must
// look through so the chunks still run in one transaction on the primary.
func TestRoutedInsertReturningSplitStaysAtomic(t *testing.T) {
	primary := returningFake(func(args []any) int { return len(args) / 2 })
	replica := rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, rwSplit)
	db := sqlkit.WrapExecutor(router)

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	var out []User
	err := db.Insert(Users).Values(rows).AutoChunk(sqlkit.ChunkSize(2)).
		Returning().All(context.Background(), sqlkit.WithRoute("default")).Scan(&out)
	if err != nil {
		t.Fatalf("routed insert returning error = %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("returned rows = %d, want 5 (concatenated)", len(out))
	}
	// The split chunks must share one write transaction on the primary, even
	// though the per-statement WithRoute wrapped the executor in a routedBinding.
	if opts := primary.BeginOptions(); len(opts) != 1 {
		t.Fatalf("primary begins = %d, want 1 (chunks must share one tx)", len(opts))
	}
}

func TestUpdateReturningSplitsByInList(t *testing.T) {
	// SET name = $1 consumes one param; ChunkSize 2 leaves two ids per statement.
	fake := returningFake(func(args []any) int { return len(args) - 1 })
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5}
	name := "x"
	var out []User
	err := db.Update(Users).Set(Users.Name.Set(&name)).Where(Users.ID.In(ids...)).
		AutoChunk(sqlkit.ChunkSize(2)).Returning().All(context.Background()).Scan(&out)
	if err != nil {
		t.Fatalf("update returning error = %v", err)
	}
	if n := len(fake.Queries()); n != 3 {
		t.Fatalf("queries = %d, want 3 (2+2+1 ids)", n)
	}
	if len(out) != 5 {
		t.Fatalf("returned rows = %d, want 5 (concatenated)", len(out))
	}
}

func TestDeleteReturningSplitsByInList(t *testing.T) {
	fake := returningFake(func(args []any) int { return len(args) })
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5}
	var out []User
	if err := db.Delete(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(2)).Returning().All(context.Background()).Scan(&out); err != nil {
		t.Fatalf("delete returning error = %v", err)
	}
	if n := len(fake.Queries()); n != 3 {
		t.Fatalf("queries = %d, want 3 (2+2+1 ids)", n)
	}
	if len(out) != 5 {
		t.Fatalf("returned rows = %d, want 5 (concatenated)", len(out))
	}
}

// TestDeleteReturningSplitsByFilterInList covers Filter's In(...) term rather
// than Column[T].In directly: the two must behave identically for AutoChunk
// splitting. Filter's In previously built its predicate via a plain
// Cond(sql.In(...)), which carries none of the chunk metadata Column[T].In
// attaches, so a Filter-driven IN silently never split regardless of
// AutoChunk/ChunkSize -- a large ID list would produce one oversized
// statement instead of several within the bind-parameter limit.
func TestDeleteReturningSplitsByFilterInList(t *testing.T) {
	fake := returningFake(func(args []any) int { return len(args) })
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []any{int64(1), int64(2), int64(3), int64(4), int64(5)}
	var out []User
	err := db.Delete(Users).Filter(filterTerms{ID: sqlkit.In(ids...)}).
		AutoChunk(sqlkit.ChunkSize(2)).Returning().All(context.Background()).Scan(&out)
	if err != nil {
		t.Fatalf("delete returning error = %v", err)
	}
	if n := len(fake.Queries()); n != 3 {
		t.Fatalf("queries = %d, want 3 (2+2+1 ids)", n)
	}
	if len(out) != 5 {
		t.Fatalf("returned rows = %d, want 5 (concatenated)", len(out))
	}
}

func TestWithAutoChunkFalseDisablesSplitting(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(false))

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	if _, err := db.Insert(Users).Values(rows).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("execs = %d, want 1 (auto-chunk disabled)", n)
	}
	if db.AutoChunkEnabled() {
		t.Fatal("AutoChunkEnabled() = true, want false")
	}
}

func TestWithAutoChunkFalseSelectInIsSingleQuery(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(false))

	var users []User
	// A per-query ChunkSize is suppressed because auto-chunk is off globally.
	if err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).
		AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (auto-chunk disabled suppresses per-query chunking)", n)
	}
}

func TestWithAutoChunkFalseExplicitBatchSizeStillSplits(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(false))

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	if _, err := db.Insert(Users).Values(rows).AutoChunk(sqlkit.ChunkSize(2)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	// An explicit INSERT chunk is honored even with auto-chunk off (2+2+1 => 3 statements).
	if n := len(fake.Execs()); n != 3 {
		t.Fatalf("execs = %d, want 3 (explicit INSERT chunk ignores the toggle)", n)
	}
}

func TestWithSessionAutoChunkDisablesPerSession(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true)) // database: auto-chunk on
	s := db.NewSession(sqlkit.WithSessionAutoChunk(false))
	defer s.Close() //nolint:errcheck // best-effort close

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	if _, err := s.Insert(Users).Values(rows).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("execs = %d, want 1 (session disabled auto-chunk)", n)
	}
	if s.AutoChunkEnabled() {
		t.Fatal("session AutoChunkEnabled() = true, want false")
	}
}

func TestWithSessionAutoChunkEnablesOverDisabledDatabase(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(false))
	s := db.NewSession(sqlkit.WithSessionAutoChunk(true))
	defer s.Close() //nolint:errcheck // best-effort close

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	if _, err := s.Insert(Users).Values(rows).AutoChunk(sqlkit.ChunkSize(2)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	// ChunkSize 2 => 2 rows per statement, 5 rows => 3 statements in the session tx.
	if n := len(fake.Execs()); n != 3 {
		t.Fatalf("execs = %d, want 3 (session re-enabled auto-chunk)", n)
	}
}

// TestPreloadBatchSizeChunksBelowLimit drives the preload split with the configured
// preload batch size even though the bind-parameter limit alone would not split.
func TestPreloadBatchSizeChunksBelowLimit(t *testing.T) {
	postQueries := 0
	fake := &testutil.Fake{
		QueryFunc: func(s string, args []any) (testutil.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				rows := make([][]driver.Value, 0, len(args))
				for i, a := range args {
					rows = append(rows, []driver.Value{int64(1000 + i), a, "t"})
				}
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: rows}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithPreloadBatchSize(2))

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	if postQueries != 3 {
		t.Fatalf("preload posts queries = %d, want 3 (preload batch size 2 => 2+2+1)", postQueries)
	}
	if len(users) != 5 {
		t.Fatalf("users = %d, want 5", len(users))
	}
	for _, u := range users {
		if len(u.Posts) != 1 || u.Posts[0].UserID != u.ID {
			t.Fatalf("user %d posts = %+v, want one post with user_id %d", u.ID, u.Posts, u.ID)
		}
	}
}

// TestPreloadBatchSizeZeroUsesHardLimitOnly: WithPreloadBatchSize(0) leaves the
// preload follow-up uncapped, so keys that fit the dialect limit run as one query.
func TestPreloadBatchSizeZeroUsesHardLimitOnly(t *testing.T) {
	postQueries := 0
	fake := &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: nil}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithPreloadBatchSize(0))

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	if postQueries != 1 {
		t.Fatalf("preload posts queries = %d, want 1 (no preload cap, fits hard limit)", postQueries)
	}
}

// TestPreloadBatchSizeDoesNotCapUserSelect: the preload batch size must not chunk a
// user-issued Column.In query.
func TestPreloadBatchSizeDoesNotCapUserSelect(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithPreloadBatchSize(2))

	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (preload batch size must not cap user queries)", n)
	}
}

func TestAutoChunkChunkSizeSplitsSelect(t *testing.T) {
	fake := idFromArgsFake()
	// The dialect limit is generous, so only the per-query ChunkSize drives the split.
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).
		AutoChunk(sqlkit.ChunkSize(2)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 3 {
		t.Fatalf("queries = %d, want 3 (ChunkSize 2 => 2+2+1)", n)
	}
}

func TestAutoChunkOffForcesSingleSelect(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB())

	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).
		AutoChunk(sqlkit.ChunkOff()).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	queries := fake.Queries()
	if len(queries) != 1 {
		t.Fatalf("queries = %d, want 1 (ChunkOff runs a single statement)", len(queries))
	}
	if len(queries[0].Args) != 5 {
		t.Fatalf("args = %d, want 5 (full IN list in one statement)", len(queries[0].Args))
	}
}

func TestAutoChunkChunkSizeSplitsDelete(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	if _, err := db.Delete(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).
		AutoChunk(sqlkit.ChunkSize(2)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n := len(fake.Execs()); n != 3 {
		t.Fatalf("execs = %d, want 3 (ChunkSize 2 => 2+2+1)", n)
	}
}

func TestAutoChunkOffForcesSingleUpdate(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Update(Users).Set(Users.Email.Set("x")).Where(Users.ID.In(1, 2, 3, 4, 5)).
		AutoChunk(sqlkit.ChunkOff()).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1 (ChunkOff runs a single statement)", len(execs))
	}
	// SET email = $1 plus the full 5-value IN list => 6 args in one statement.
	if len(execs[0].Args) != 6 {
		t.Fatalf("args = %d, want 6 (SET value + full IN list)", len(execs[0].Args))
	}
}

// repeatableRead is the driver isolation level a REPEATABLE READ snapshot tx
// requests, for asserting BeginOptions.
var repeatableRead = driver.IsolationLevel(stdsql.LevelRepeatableRead)

func TestInsertAutoSplitsByBindParamLimit(t *testing.T) {
	fake := &testutil.Fake{}
	// 2 rows per statement (old limit 4 / 2 columns) via a per-query chunk size.
	db := sqlkit.WrapDB(fake.DB())

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}, {Email: "d"}, {Email: "e"}}
	res, err := db.Insert(Users).Values(rows).AutoChunk(sqlkit.ChunkSize(2)).Exec(context.Background())
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("execs = %d, want 3 (2+2+1 rows)", len(execs))
	}
	for _, e := range execs {
		if !strings.HasPrefix(e.SQL, "INSERT INTO") {
			t.Fatalf("statement = %q, want INSERT", e.SQL)
		}
	}
	// First two batches carry 2 rows (4 args), the last carries 1 row (2 args).
	if got := []int{len(execs[0].Args), len(execs[1].Args), len(execs[2].Args)}; got[0] != 4 || got[1] != 4 || got[2] != 2 {
		t.Fatalf("per-batch arg counts = %v, want [4 4 2]", got)
	}
	// The split runs atomically in one auto-committed transaction.
	if fake.Begins() != 1 || fake.Commits() != 1 {
		t.Fatalf("begins=%d commits=%d, want 1/1 (auto-tx)", fake.Begins(), fake.Commits())
	}
	if n, _ := res.RowsAffected(); n != 3 {
		t.Fatalf("RowsAffected = %d, want 3 (summed batches)", n)
	}
}

func TestInsertNoLimitNoBatchSizeIsSingleStatement(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB()) // no limit configured

	rows := []User{{Email: "a"}, {Email: "b"}, {Email: "c"}}
	if _, err := db.Insert(Users).Values(rows).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("execs = %d, want 1 (no splitting without a limit)", n)
	}
	if fake.Begins() != 0 {
		t.Fatalf("begins = %d, want 0 (no transaction for a single statement)", fake.Begins())
	}
}

func TestInsertWithinLimitIsSingleStatement(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	rows := []User{{Email: "a"}, {Email: "b"}}
	if _, err := db.Insert(Users).Values(rows).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("execs = %d, want 1 (rows fit within the limit)", n)
	}
	if fake.Begins() != 0 {
		t.Fatalf("begins = %d, want 0", fake.Begins())
	}
}

// idFromArgsFake returns one row per query whose id is the number of bind args,
// so a scan reveals how the IN list was split across statements.
func idFromArgsFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(_ string, args []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(len(args)), "x@example.com", nil}},
			}, nil
		},
	}
}

// TestSelectWithWindowFunctionIsNotSplit guards against splitting a deferred IN
// when the projection carries a window function: a window aggregates/ranks over
// the whole result set, so per-chunk statements would compute it wrong (e.g.
// ROW_NUMBER restarting in each chunk). The query must run as one statement.
func TestSelectWithWindowFunctionIsNotSplit(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	var users []User
	err := db.Select(Users.ID, sql.As(sql.RowNumber().Over().OrderBy(Users.ID.Asc()), "rn")).
		From(Users).Where(Users.ID.In(1, 2, 3, 4, 5, 6, 7)).
		AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (a window function must not be split)", n)
	}
	if got := len(fake.Queries()[0].Args); got != 7 {
		t.Fatalf("single statement arg count = %d, want 7 (all ids in one IN)", got)
	}
}

func TestSelectInChunksByBindParamLimit(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	var users []User
	err := db.Select().From(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	queries := fake.Queries()
	if len(queries) != 3 {
		t.Fatalf("queries = %d, want 3 (3+3+1 ids)", len(queries))
	}
	if got := []int{len(queries[0].Args), len(queries[1].Args), len(queries[2].Args)}; got[0] != 3 || got[1] != 3 || got[2] != 1 {
		t.Fatalf("per-chunk arg counts = %v, want [3 3 1]", got)
	}
	// Rows from every chunk are concatenated: one row per query, ids 3, 3, 1.
	if len(users) != 3 {
		t.Fatalf("users = %d, want 3 (one row per chunk)", len(users))
	}
	if users[0].ID != 3 || users[1].ID != 3 || users[2].ID != 1 {
		t.Fatalf("ids = [%d %d %d], want [3 3 1]", users[0].ID, users[1].ID, users[2].ID)
	}
}

func TestSelectInChunksRunUnderRepeatableReadSnapshot(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	// The 3 chunk queries run inside one snapshot transaction by default.
	if fake.Begins() != 1 || fake.Commits() != 1 {
		t.Fatalf("begins=%d commits=%d, want 1/1 (one snapshot tx)", fake.Begins(), fake.Commits())
	}
	if n := len(fake.Queries()); n != 3 {
		t.Fatalf("queries = %d, want 3", n)
	}
}

func TestSelectInChunksWithoutSnapshotRunIndependently(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithBatchReadSnapshot(false))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	// With the snapshot off, the chunks run as independent reads (no transaction).
	if fake.Begins() != 0 {
		t.Fatalf("begins = %d, want 0 (no snapshot tx)", fake.Begins())
	}
	if n := len(fake.Queries()); n != 3 {
		t.Fatalf("queries = %d, want 3", n)
	}
}

func TestSelectInSingleQueryNoSnapshotTx(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB())

	// Values fit in one statement, so no splitting and no snapshot transaction.
	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if fake.Begins() != 0 {
		t.Fatalf("begins = %d, want 0 (single query, no tx)", fake.Begins())
	}
}

func TestSelectInWithinLimitIsSingleQuery(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB())

	var users []User
	err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3)).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (ids fit within the limit)", n)
	}
}

func TestSelectInOrderByRunsSingleQuery(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB())

	ids := []int64{1, 2, 3, 4, 5}
	var users []User
	// ORDER BY makes concatenating split results unsafe, so it stays one query.
	err := db.Select().From(Users).Where(Users.ID.In(ids...)).OrderBy(Users.ID.Asc()).
		AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	queries := fake.Queries()
	if len(queries) != 1 {
		t.Fatalf("queries = %d, want 1 (ORDER BY suppresses splitting)", len(queries))
	}
	if len(queries[0].Args) != 5 {
		t.Fatalf("args = %d, want 5 (full IN list)", len(queries[0].Args))
	}
}

func TestSelectInNoLimitIsSingleQuery(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB()) // no limit

	ids := []int64{1, 2, 3, 4, 5}
	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(ids...)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (no splitting without a limit)", n)
	}
}

func TestSelectNotInRunsSingleQuery(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB())

	ids := []int64{1, 2, 3, 4, 5}
	var users []User
	// NOT IN cannot be partitioned across statements, so it is never split.
	if err := db.Select().From(Users).Where(Users.ID.NotIn(ids...)).AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	queries := fake.Queries()
	if len(queries) != 1 {
		t.Fatalf("queries = %d, want 1 (NOT IN is not split)", len(queries))
	}
	if len(queries[0].Args) != 5 {
		t.Fatalf("args = %d, want 5 (full NOT IN list)", len(queries[0].Args))
	}
}

func TestSelectInToSQLFoldsFullList(t *testing.T) {
	q := sqlkit.Select().From(Users).Where(Users.ID.In(1, 2, 3))
	assertToSQL(t, q,
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."id" IN ($1, $2, $3))`,
		[]any{int64(1), int64(2), int64(3)})
}

func TestSelectInCombinedWithFilterToSQLUnchanged(t *testing.T) {
	// A filter plus an IN keeps the same flat AND grouping it had before the
	// deferral machinery existed.
	q := sqlkit.Select().From(Users).Where(Users.Email.Eq("x@example.com")).Where(Users.ID.In(1, 2))
	assertToSQL(t, q,
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE (("public"."users"."email" = $1) AND ("public"."users"."id" IN ($2, $3)))`,
		nil)
}

func TestUpdateInChunksByBindParamLimit(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	name := "x"
	// ChunkSize 2 => 2 ids per statement (old limit 3 minus one SET param).
	res, err := db.Update(Users).Set(Users.Name.Set(&name)).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(2)).Exec(context.Background())
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 4 {
		t.Fatalf("execs = %d, want 4 (2+2+2+1 ids)", len(execs))
	}
	for _, e := range execs {
		if !strings.HasPrefix(e.SQL, "UPDATE") {
			t.Fatalf("statement = %q, want UPDATE", e.SQL)
		}
	}
	if fake.Begins() != 1 || fake.Commits() != 1 {
		t.Fatalf("begins=%d commits=%d, want 1/1 (auto-tx)", fake.Begins(), fake.Commits())
	}
	if n, _ := res.RowsAffected(); n != 4 {
		t.Fatalf("RowsAffected = %d, want 4 (summed batches)", n)
	}
}

func TestDeleteInChunksByBindParamLimit(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	if _, err := db.Delete(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("execs = %d, want 3 (3+3+1 ids)", len(execs))
	}
	for _, e := range execs {
		if !strings.HasPrefix(e.SQL, "DELETE FROM") {
			t.Fatalf("statement = %q, want DELETE", e.SQL)
		}
	}
}

// A split UPDATE/DELETE through a Session joins its transaction rather than
// opening a new one.
func TestUpdateInChunksJoinsSessionTransaction(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))
	session := db.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	ids := []int64{1, 2, 3, 4, 5}
	if _, err := session.Delete(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := session.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 1 {
		t.Fatalf("begins=%d commits=%d, want 1/1 (single session tx)", fake.Begins(), fake.Commits())
	}
	if n := len(fake.Execs()); n != 2 {
		t.Fatalf("execs = %d, want 2 (3+2 ids)", n)
	}
}

// A reference UPDATE — SET plus a correlated FROM source — splits its IN list
// the same way, each chunk re-running the full statement for its id subset.
func TestReferenceUpdateInChunks(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	_, err := db.Update(Posts).
		Set(Posts.Title.Set("x")). // 1 param, leaving 2 ids per statement
		From(Users).
		Where(Posts.UserID.EqExpr(Users.ID)).
		Where(Posts.ID.In(ids...)).
		AutoChunk(sqlkit.ChunkSize(2)).
		Exec(context.Background())
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 4 {
		t.Fatalf("execs = %d, want 4 (2+2+2+1 ids)", len(execs))
	}
	for _, e := range execs {
		if !strings.HasPrefix(e.SQL, "UPDATE") || !strings.Contains(e.SQL, "FROM") {
			t.Fatalf("statement = %q, want a reference UPDATE with FROM", e.SQL)
		}
	}
	// Default: the atomic wrapping transaction keeps the driver's isolation.
	for _, opts := range fake.BeginOptions() {
		if opts.Isolation != driver.IsolationLevel(stdsql.LevelDefault) {
			t.Fatalf("write tx isolation = %v, want default", opts.Isolation)
		}
	}
}

func TestBatchWriteSnapshotUsesRepeatableRead(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(),
		sqlkit.WithAutoChunk(true),
		sqlkit.WithBatchWriteSnapshot(true))

	ids := []int64{1, 2, 3, 4, 5}
	if _, err := db.Delete(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	opts := fake.BeginOptions()
	if len(opts) != 1 {
		t.Fatalf("begins = %d, want 1", len(opts))
	}
	if opts[0].Isolation != repeatableRead {
		t.Fatalf("write tx isolation = %v, want REPEATABLE READ", opts[0].Isolation)
	}
	if opts[0].ReadOnly {
		t.Fatal("write tx must not be read-only")
	}
}

func TestBatchReadSnapshotIsReadOnlyRepeatableRead(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))

	ids := []int64{1, 2, 3, 4, 5}
	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	opts := fake.BeginOptions()
	if len(opts) != 1 {
		t.Fatalf("begins = %d, want 1", len(opts))
	}
	if opts[0].Isolation != repeatableRead || !opts[0].ReadOnly {
		t.Fatalf("read tx opts = %+v, want REPEATABLE READ read-only", opts[0])
	}
}

func TestSplitStatementsPassThroughHooks(t *testing.T) {
	fake := idFromArgsFake()
	var queries, execs int
	count := sqlkit.Hooks{Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
		switch stmt.Op {
		case sqlkit.OpQuery:
			queries++
		case sqlkit.OpExec:
			execs++
		}
		return ctx, nil
	}}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithHooks(count))

	ids := []int64{1, 2, 3, 4, 5, 6, 7}
	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if queries != 3 {
		t.Fatalf("intercepted queries = %d, want 3 (one per chunk)", queries)
	}
	if _, err := db.Delete(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(3)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if execs != 3 {
		t.Fatalf("intercepted execs = %d, want 3 (one per chunk)", execs)
	}
}

func TestInBatchesConcatenatesChunks(t *testing.T) {
	fake := idFromArgsFake()
	db := sqlkit.WrapDB(fake.DB())

	ids := []int64{10, 20, 30, 40, 50}
	var users []User
	err := sqlkit.InBatches(context.Background(), db, ids, func(chunk []int64) sqlkit.Rows {
		return db.Select().From(Users).Where(Users.ID.In(chunk...)).All(context.Background())
	}, &users, 2)
	if err != nil {
		t.Fatalf("InBatches() error = %v", err)
	}
	// Explicit chunk size 2: 2+2+1 => 3 chunks, one row each.
	if len(users) != 3 {
		t.Fatalf("users = %d, want 3 (one row per chunk)", len(users))
	}
	if users[0].ID != 2 || users[2].ID != 1 {
		t.Fatalf("ids = [%d ... %d], want first 2 last 1", users[0].ID, users[2].ID)
	}
}
