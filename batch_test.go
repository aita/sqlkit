package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// repeatableRead is the driver isolation level a REPEATABLE READ snapshot tx
// requests, for asserting BeginOptions.
var repeatableRead = driver.IsolationLevel(stdsql.LevelRepeatableRead)

// --- INSERT bulk auto-splitting driven by the configured bind-parameter limit.

func TestInsertAutoSplitsByBindParamLimit(t *testing.T) {
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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

// --- SELECT IN-list auto-chunking.

// idFromArgsFake returns one row per query whose id is the number of bind args,
// so a scan reveals how the IN list was split across statements.
func idFromArgsFake() *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(_ string, args []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(len(args)), "x@example.com", nil}},
			}, nil
		},
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

// ToSQL always folds a deferred IN into a single statement, regardless of any
// configured limit, so compiled SQL is unaffected by the splitting feature.

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

// --- UPDATE / DELETE IN-list auto-chunking.

func TestUpdateInChunksByBindParamLimit(t *testing.T) {
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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
	fake := &sqltest.Fake{}
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

// --- Interceptors observe each split chunk.

func TestSplitStatementsPassThroughInterceptors(t *testing.T) {
	fake := idFromArgsFake()
	var queries, execs int
	count := sqlkit.Rewrite(func(_ context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
		switch stmt.Op {
		case sqlkit.OpQuery:
			queries++
		case sqlkit.OpExec:
			execs++
		}
		return stmt, nil
	})
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true), sqlkit.WithInterceptor(count))

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

// --- InBatches helper.

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
