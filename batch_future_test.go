package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// --- Item 1: Preload IN-list auto-chunking. ---

// TestPreloadInChunksByBindParamLimit drives a Preload whose follow-up IN list
// exceeds the limit, so it splits into several statements whose rows concatenate.
func TestPreloadInChunksByBindParamLimit(t *testing.T) {
	postQueries := 0
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				// One post per IN value, its user_id equal to that value, so every
				// parent matches exactly one child regardless of chunk membership.
				rows := make([][]driver.Value, 0, len(args))
				for i, a := range args {
					rows = append(rows, []driver.Value{int64(1000 + i), a, "t"})
				}
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: rows}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithPreloadBatchSize(2))

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
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: nil}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
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

// --- Item 2: RETURNING from split writes. ---

// returningFake returns one row per inserted/affected row in a chunk, derived
// from the chunk's bind-argument count.
func returningFake(rowsPerChunk func(args []any) int) *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(_ string, args []any) (sqltest.Result, error) {
			n := rowsPerChunk(args)
			rows := make([][]driver.Value, 0, n)
			for i := 0; i < n; i++ {
				rows = append(rows, []driver.Value{int64(i + 1), "x@example.com", nil})
			}
			return sqltest.Result{Columns: []string{"id", "email", "name"}, Rows: rows}, nil
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

func TestUpdateReturningSplitsByInList(t *testing.T) {
	// SET name = $1 consumes one param; ChunkSize 2 leaves two ids per statement.
	fake := returningFake(func(args []any) int { return len(args) - 1 })
	db := sqlkit.WrapDB(fake.DB())

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
	db := sqlkit.WrapDB(fake.DB())

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

// --- Deliverable A: explicit auto-split toggle. ---

func TestWithAutoChunkFalseDisablesSplitting(t *testing.T) {
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB()) // database: auto-chunk on
	s := db.NewSession(sqlkit.WithSessionAutoChunk(false))
	defer s.Close()

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
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(false))
	s := db.NewSession(sqlkit.WithSessionAutoChunk(true))
	defer s.Close()

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

// --- Preload chunk size (WithPreloadBatchSize, selectinload-style). ---

// TestPreloadBatchSizeChunksBelowLimit drives the preload split with the configured
// preload batch size even though the bind-parameter limit alone would not split.
func TestPreloadBatchSizeChunksBelowLimit(t *testing.T) {
	postQueries := 0
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				rows := make([][]driver.Value, 0, len(args))
				for i, a := range args {
					rows = append(rows, []driver.Value{int64(1000 + i), a, "t"})
				}
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: rows}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithPreloadBatchSize(2))

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
	fake := &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				postQueries++
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: nil}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
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
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithPreloadBatchSize(2))

	var users []User
	if err := db.Select().From(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (preload batch size must not cap user queries)", n)
	}
}

// --- Per-query AutoChunk (ChunkSize / ChunkOff). ---

func TestAutoChunkChunkSizeSplitsSelect(t *testing.T) {
	fake := idFromArgsFake()
	// The dialect limit is generous, so only the per-query ChunkSize drives the split.
	db := sqlkit.WrapDB(fake.DB())

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
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Delete(Users).Where(Users.ID.In(1, 2, 3, 4, 5)).
		AutoChunk(sqlkit.ChunkSize(2)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n := len(fake.Execs()); n != 3 {
		t.Fatalf("execs = %d, want 3 (ChunkSize 2 => 2+2+1)", n)
	}
}

func TestAutoChunkOffForcesSingleUpdate(t *testing.T) {
	fake := &sqltest.Fake{}
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
