package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// routerFunc adapts a function to the Router interface.
type routerFunc func(ctx context.Context, stmt sqlkit.Statement) string

func (f routerFunc) Route(ctx context.Context, stmt sqlkit.Statement) string { return f(ctx, stmt) }

// rwSplit routes reads to "replica" and writes to the default backend, honoring
// an explicit WithRoute hint first — the conventional read/write-split policy.
var rwSplit = routerFunc(func(_ context.Context, stmt sqlkit.Statement) string {
	if stmt.RouteHint != "" {
		return stmt.RouteHint
	}
	if stmt.IsWrite() {
		return ""
	}
	return "replica"
})

func rowFake() *testutil.Fake {
	return &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{
			Columns: []string{"id", "email", "name"},
			Rows:    [][]driver.Value{{int64(1), "a@example.com", nil}},
		}, nil
	}}
}

// routedDB wires a primary ("default") and a replica behind rwSplit, returning
// the database and both fakes so a test can see where each statement landed.
func routedDB(t *testing.T) (*sqlkit.Database, *testutil.Fake, *testutil.Fake) {
	t.Helper()
	primary, replica := rowFake(), rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, rwSplit)
	return sqlkit.WrapExecutor(router), primary, replica
}

func TestRouteReadsToReplicaWritesToDefault(t *testing.T) {
	db, primary, replica := routedDB(t)
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Select error = %v", err)
	}
	if len(replica.Queries()) != 1 {
		t.Fatalf("read should hit replica, replica queries = %d", len(replica.Queries()))
	}
	if len(primary.Queries()) != 0 {
		t.Fatalf("read should not hit primary, primary queries = %d", len(primary.Queries()))
	}

	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if len(primary.Execs()) != 1 {
		t.Fatalf("write should hit primary, primary execs = %d", len(primary.Execs()))
	}
	if len(replica.Execs()) != 0 {
		t.Fatalf("write should not hit replica, replica execs = %d", len(replica.Execs()))
	}
}

// A RETURNING write runs through the row-returning path (Op == OpQuery) yet must
// route as a write: the Router branches on Kind/IsWrite, not Op, so it lands on
// the primary, not the read replica.
func TestReturningWriteRoutesByKindNotOp(t *testing.T) {
	db, primary, replica := routedDB(t)
	ctx := context.Background()

	var got User
	err := db.Insert(Users).Values(User{ID: 1, Email: "a@example.com"}).
		Returning(Users.ID, Users.Email, Users.Name).All(ctx).Scan(&got)
	if err != nil {
		t.Fatalf("INSERT RETURNING error = %v", err)
	}
	// It is a query at the driver level, but must be on the primary.
	if len(primary.Queries()) != 1 {
		t.Fatalf("RETURNING write should hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 0 {
		t.Fatalf("RETURNING write must not hit replica, replica queries = %d", len(replica.Queries()))
	}
}

// WithRoute on a terminal sets Statement.RouteHint, which rwSplit honors: a read
// that would default to the replica is sent to the named backend instead.
func TestWithRouteHintHonored(t *testing.T) {
	db, primary, replica := routedDB(t)
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx, sqlkit.WithRoute("default")).Scan(&users); err != nil {
		t.Fatalf("Select error = %v", err)
	}
	if len(primary.Queries()) != 1 {
		t.Fatalf("hinted read should hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 0 {
		t.Fatalf("hinted read should not hit replica, replica queries = %d", len(replica.Queries()))
	}
}

// A SELECT ... FOR UPDATE takes row locks and must run on the primary, not the
// read replica, even though it returns rows.
func TestLockingReadRoutesToPrimary(t *testing.T) {
	db, primary, replica := routedDB(t)
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).ForUpdate().All(ctx).Scan(&users); err != nil {
		t.Fatalf("FOR UPDATE select error = %v", err)
	}
	if len(primary.Queries()) != 1 {
		t.Fatalf("locking read should hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 0 {
		t.Fatalf("locking read must not hit replica, replica queries = %d", len(replica.Queries()))
	}
}

// The built-in ReadWriteRouter sends reads to the replica and writes to the
// primary, the same as the hand-written rwSplit above.
func TestReadWriteRouter(t *testing.T) {
	primary, replica := rowFake(), rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("replica")))
	db := sqlkit.WrapExecutor(router)
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Select error = %v", err)
	}
	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if len(replica.Queries()) != 1 || len(primary.Execs()) != 1 {
		t.Fatalf("split wrong: replica queries=%d, primary execs=%d", len(replica.Queries()), len(primary.Execs()))
	}
}

// ReadWriteRouter ignores a WithRoute hint on a write: a write hinted to a
// replica must still run on the primary, never a read replica.
func TestReadWriteRouterIgnoresWriteHint(t *testing.T) {
	primary, replica := rowFake(), rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("replica")))
	db := sqlkit.WrapExecutor(router)
	ctx := context.Background()

	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).
		Where(Users.ID.Eq(1)).Exec(ctx, sqlkit.WithRoute("replica")); err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if len(primary.Execs()) != 1 {
		t.Fatalf("write must hit primary despite the hint, primary execs = %d", len(primary.Execs()))
	}
	if len(replica.Execs()) != 0 {
		t.Fatalf("write must never hit replica, replica execs = %d", len(replica.Execs()))
	}
}

// With no replicas configured (a single database), ReadWriteRouter sends reads to
// the primary too, so the same wiring works without replicas.
func TestReadWriteRouterSingleDatabase(t *testing.T) {
	primary := rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
	}, sqlkit.ReadWriteRouter())
	db := sqlkit.WrapExecutor(router)
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Select error = %v", err)
	}
	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if len(primary.Queries()) != 1 || len(primary.Execs()) != 1 {
		t.Fatalf("single DB should serve both: queries=%d execs=%d", len(primary.Queries()), len(primary.Execs()))
	}
}

// An AccessUnknown statement (an opaque raw string the classifier can't read,
// e.g. one starting with a CTE) must go to the primary, on the same "stay on
// the safe side" contract as a write — never to a read replica.
func TestReadWriteRouterUnknownAccessGoesToPrimary(t *testing.T) {
	primary, replica := rowFake(), rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("replica")))
	db := sqlkit.WrapExecutor(router)
	ctx := context.Background()

	var out []User
	if err := db.RawSQL(ctx, "WITH t AS (SELECT 1) SELECT * FROM t").Scan(&out); err != nil {
		t.Fatalf("RawSQL Scan error = %v", err)
	}
	if len(primary.Queries()) != 1 {
		t.Fatalf("AccessUnknown statement must hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 0 {
		t.Fatalf("AccessUnknown statement must never hit a replica, replica queries = %d", len(replica.Queries()))
	}
}

// ReadWriteRouter balances reads round-robin across several replicas.
func TestReadWriteRouterBalancesReplicas(t *testing.T) {
	primary, ra, rb := rowFake(), rowFake(), rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"r-a":     sqlkit.DB(ra.DB()),
		"r-b":     sqlkit.DB(rb.DB()),
	}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("r-a", "r-b")))
	db := sqlkit.WrapExecutor(router)
	ctx := context.Background()

	for range 4 {
		var users []User
		if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
			t.Fatalf("Select error = %v", err)
		}
	}
	if len(ra.Queries()) != 2 || len(rb.Queries()) != 2 {
		t.Fatalf("reads not balanced: r-a=%d r-b=%d", len(ra.Queries()), len(rb.Queries()))
	}
}

// WithBalancer swaps the read-balancing policy; Random still keeps reads off the
// primary.
func TestReadWriteRouterWithBalancer(t *testing.T) {
	primary, ra, rb := rowFake(), rowFake(), rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"r-a":     sqlkit.DB(ra.DB()),
		"r-b":     sqlkit.DB(rb.DB()),
	}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("r-a", "r-b"), sqlkit.WithBalancer(sqlkit.Random())))
	db := sqlkit.WrapExecutor(router)
	ctx := context.Background()

	for range 10 {
		var users []User
		if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
			t.Fatalf("Select error = %v", err)
		}
	}
	if got := len(ra.Queries()) + len(rb.Queries()); got != 10 {
		t.Fatalf("reads should all go to replicas, got %d on replicas", got)
	}
	if len(primary.Queries()) != 0 {
		t.Fatalf("no read should hit the primary, got %d", len(primary.Queries()))
	}
}

func TestRouteToUnknownBackendErrors(t *testing.T) {
	primary := rowFake()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
	}, routerFunc(func(context.Context, sqlkit.Statement) string { return "nope" }))
	db := sqlkit.WrapExecutor(router)

	var users []User
	err := db.Select().From(Users).All(context.Background()).Scan(&users)
	if err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("error = %v, want unknown backend", err)
	}
}

// A session is pinned to one backend at Begin, chosen from its WithRoute hint;
// every statement in it — even reads — runs on that backend.
func TestSessionPinnedByWithRoute(t *testing.T) {
	db, primary, replica := routedDB(t)
	ctx := context.Background()

	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		var users []User
		if err := s.Select().From(Users).All(ctx).Scan(&users); err != nil {
			return err
		}
		_, err := s.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1)).Exec(ctx)
		return err
	}, sqlkit.WithRoute("replica"))
	if err != nil {
		t.Fatalf("WithSession error = %v", err)
	}
	if replica.Begins() != 1 {
		t.Fatalf("session should begin on replica, replica begins = %d", replica.Begins())
	}
	if primary.Begins() != 0 {
		t.Fatalf("session must not begin on primary, primary begins = %d", primary.Begins())
	}
	if len(replica.Queries()) != 1 || len(replica.Execs()) != 1 {
		t.Fatalf("both statements should run on replica: queries=%d execs=%d", len(replica.Queries()), len(replica.Execs()))
	}
}

// A read-only session routes to the replica without an explicit hint, because
// beginRouted classifies its synthetic statement as a read.
func TestReadOnlySessionRoutesToReplica(t *testing.T) {
	db, primary, replica := routedDB(t)
	ctx := context.Background()

	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		var users []User
		return s.Select().From(Users).All(ctx).Scan(&users)
	}, sqlkit.WithTxOptions(sqlkit.TxOptions{ReadOnly: true}))
	if err != nil {
		t.Fatalf("WithSession error = %v", err)
	}
	if replica.Begins() != 1 || primary.Begins() != 0 {
		t.Fatalf("read-only session should begin on replica: replica=%d primary=%d", replica.Begins(), primary.Begins())
	}
}

// A per-statement WithRoute naming a backend other than the session's pin is a
// distributed-transaction request the router will not fake: it is ErrRouteConflict.
func TestSessionRouteConflictErrors(t *testing.T) {
	db, _, _ := routedDB(t)
	ctx := context.Background()

	// A write session pins to "default"; a statement asking for "replica" conflicts.
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		var users []User
		return s.Select().From(Users).All(ctx, sqlkit.WithRoute("replica")).Scan(&users)
	})
	if !errors.Is(err, sqlkit.ErrRouteConflict) {
		t.Fatalf("error = %v, want ErrRouteConflict", err)
	}
}

// A per-statement WithRoute matching the session's pin is redundant but harmless:
// it runs without error on the pinned backend.
func TestSessionRedundantRouteAllowed(t *testing.T) {
	db, _, replica := routedDB(t)
	ctx := context.Background()

	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		var users []User
		return s.Select().From(Users).All(ctx, sqlkit.WithRoute("replica")).Scan(&users)
	}, sqlkit.WithRoute("replica"))
	if err != nil {
		t.Fatalf("WithSession error = %v", err)
	}
	if len(replica.Queries()) != 1 {
		t.Fatalf("statement should run on replica, queries = %d", len(replica.Queries()))
	}
}

// WithRoute on a SELECT propagates to its Preload follow-up by default, so a
// hinted parent and its children land on the same backend.
func TestWithRoutePropagatesToPreload(t *testing.T) {
	db, primary, replica := preloadRoutedDB(t)
	ctx := context.Background()

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).
		All(ctx, sqlkit.WithRoute("default")).Scan(&users); err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	// Both the parent users query and the child posts query ran on the primary.
	if len(primary.Queries()) != 2 {
		t.Fatalf("parent+child should both hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 0 {
		t.Fatalf("nothing should hit replica, replica queries = %d", len(replica.Queries()))
	}
}

// NoPropagate confines the hint to the parent: the Preload follow-up routes
// independently (by the rwSplit read policy, to the replica).
func TestWithRouteNoPropagateLeavesPreloadToRouter(t *testing.T) {
	db, primary, replica := preloadRoutedDB(t)
	ctx := context.Background()

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).
		All(ctx, sqlkit.WithRoute("default", sqlkit.NoPropagate)).Scan(&users); err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	// Parent on primary (hinted), child on replica (router's read choice).
	if len(primary.Queries()) != 1 {
		t.Fatalf("only the parent should hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 1 {
		t.Fatalf("the child should route to replica, replica queries = %d", len(replica.Queries()))
	}
}

// TestScanBatchedPreloadHonorsNoPropagate covers the split-query path of the
// same guarantee TestWithRouteNoPropagateLeavesPreloadToRouter checks for an
// unsplit one: scanBatched (a base query whose IN list auto-chunks into
// several statements) must still route its Preload follow-up through
// preloadEx, not through the batch executor derived from the hinted binding —
// otherwise NoPropagate silently stops working the moment a preloaded query
// also splits.
func TestScanBatchedPreloadHonorsNoPropagate(t *testing.T) {
	mk := func() *testutil.Fake {
		return &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
				}}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		}}
	}
	primary, replica := mk(), mk()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, rwSplit)
	db := sqlkit.WrapExecutor(router, sqlkit.WithAutoChunk(true))
	ctx := context.Background()

	var users []preUser
	err := db.Select(Users.ID).From(Users).Where(Users.ID.In(1, 2)).
		AutoChunk(sqlkit.ChunkSize(1)).
		Preload(Posts).
		All(ctx, sqlkit.WithRoute("default", sqlkit.NoPropagate)).Scan(&users)
	if err != nil {
		t.Fatalf("Preload error = %v", err)
	}
	// Both split base chunks hinted to primary; the child preload routes
	// independently (by rwSplit's read policy) to the replica.
	if len(primary.Queries()) != 2 {
		t.Fatalf("both base chunks should hit primary, primary queries = %d", len(primary.Queries()))
	}
	if len(replica.Queries()) != 1 {
		t.Fatalf("the preload should route to replica, replica queries = %d", len(replica.Queries()))
	}
}

// preloadRoutedDB wires two backends whose fakes both serve the users base query
// and the posts follow-up, so a test can see which backend each landed on.
func preloadRoutedDB(t *testing.T) (*sqlkit.Database, *testutil.Fake, *testutil.Fake) {
	t.Helper()
	mk := func() *testutil.Fake {
		return &testutil.Fake{QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if strings.Contains(s, `"public"."posts"`) {
				return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
				}}, nil
			}
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		}}
	}
	primary, replica := mk(), mk()
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, rwSplit)
	return sqlkit.WrapExecutor(router), primary, replica
}

// TestRoutedSelectSplitGetsReadSnapshot guards a routed (per-statement
// WithRoute) split SELECT against losing the REPEATABLE READ snapshot:
// batchExecutor must unwrap the routedBinding even for reads so the chunks run
// inside one snapshot transaction.
func TestRoutedSelectSplitGetsReadSnapshot(t *testing.T) {
	primary := &testutil.Fake{QueryFunc: func(_ string, args []any) (testutil.Result, error) {
		rows := make([][]driver.Value, 0, len(args))
		for _, a := range args {
			rows = append(rows, []driver.Value{a, "x@example.com", nil})
		}
		return testutil.Result{Columns: []string{"id", "email", "name"}, Rows: rows}, nil
	}}
	replica := &testutil.Fake{QueryFunc: func(_ string, _ []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id", "email", "name"}}, nil
	}}
	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
		"default": sqlkit.DB(primary.DB()),
		"replica": sqlkit.DB(replica.DB()),
	}, rwSplit)
	db := sqlkit.WrapExecutor(router, sqlkit.WithAutoChunk(true))

	var users []User
	ids := []int64{1, 2, 3, 4, 5}
	err := db.Select().From(Users).Where(Users.ID.In(ids...)).
		AutoChunk(sqlkit.ChunkSize(2)).
		All(context.Background(), sqlkit.WithRoute("default")).Scan(&users)
	if err != nil {
		t.Fatalf("routed select error = %v", err)
	}
	if len(users) != 5 {
		t.Fatalf("users = %d, want 5", len(users))
	}
	// The split read chunks must share one snapshot transaction on the primary
	// (the hinted backend), even though WithRoute wrapped the executor in a
	// routedBinding. Without the fix, batchExecutor could not look through the
	// routedBinding to the *Database, so each chunk ran as an independent
	// auto-commit read with no snapshot.
	if opts := primary.BeginOptions(); len(opts) != 1 {
		t.Fatalf("primary begins = %d, want 1 (chunks must share one snapshot tx)", len(opts))
	}
}

func TestStatementKindAndIsWrite(t *testing.T) {
	cases := []struct {
		name  string
		stmt  sqlkit.Statement
		kind  sqlkit.StatementKind
		write bool
	}{
		{"select", sqlkit.Statement{Query: sqlkit.Select().From(Users)}, sqlkit.KindSelect, false},
		{"insert", sqlkit.Statement{Query: sqlkit.Insert(Users)}, sqlkit.KindInsert, true},
		{"update", sqlkit.Statement{Query: sqlkit.Update(Users)}, sqlkit.KindUpdate, true},
		{"delete", sqlkit.Statement{Query: sqlkit.Delete(Users)}, sqlkit.KindDelete, true},
		// A RETURNING write carries the InsertQuery AST with Op == OpQuery.
		{"insert-returning", sqlkit.Statement{Op: sqlkit.OpQuery, Query: sqlkit.Insert(Users)}, sqlkit.KindInsert, true},
		// A locking read stays KindSelect but is a write for routing.
		{"select-for-update", sqlkit.Statement{Query: sqlkit.Select().From(Users).ForUpdate()}, sqlkit.KindSelect, true},
		{"select-for-share", sqlkit.Statement{Query: sqlkit.Select().From(Users).ForShare()}, sqlkit.KindSelect, true},
		{"raw-select", sqlkit.Statement{Op: sqlkit.OpQuery, SQL: "SELECT 1"}, sqlkit.KindRawRead, false},
		{"raw-select-for-update", sqlkit.Statement{Op: sqlkit.OpQuery, SQL: "SELECT * FROM users FOR UPDATE"}, sqlkit.KindRawRead, true},
		{"raw-unknown", sqlkit.Statement{Op: sqlkit.OpQuery, SQL: "UPDATE users SET x = 1"}, sqlkit.KindRawUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.stmt.Kind(); got != tc.kind {
				t.Fatalf("Kind() = %v, want %v", got, tc.kind)
			}
			if got := tc.stmt.IsWrite(); got != tc.write {
				t.Fatalf("IsWrite() = %v, want %v", got, tc.write)
			}
		})
	}
}

func TestStatementAccess(t *testing.T) {
	cases := []struct {
		name string
		stmt sqlkit.Statement
		want sqlkit.Access
	}{
		{"select", sqlkit.Statement{Query: sqlkit.Select().From(Users)}, sqlkit.AccessRead},
		{"insert", sqlkit.Statement{Query: sqlkit.Insert(Users)}, sqlkit.AccessWrite},
		{"select-for-update", sqlkit.Statement{Query: sqlkit.Select().From(Users).ForUpdate()}, sqlkit.AccessWrite},
		{"raw-select", sqlkit.Statement{Op: sqlkit.OpQuery, SQL: "SELECT 1"}, sqlkit.AccessRead},
		// An opaque raw string is undetermined — distinct from a definite read,
		// though IsWrite still folds it to the safe (write) side.
		{"raw-unknown", sqlkit.Statement{Op: sqlkit.OpQuery, SQL: "WITH t AS (...) SELECT 1"}, sqlkit.AccessUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.stmt.Access(); got != tc.want {
				t.Fatalf("Access() = %v, want %v", got, tc.want)
			}
			// IsWrite is the safe boolean: anything not a definite read is a write.
			if got, want := tc.stmt.IsWrite(), tc.want != sqlkit.AccessRead; got != want {
				t.Fatalf("IsWrite() = %v, want %v", got, want)
			}
		})
	}
}

// TestStatementAccessWritableCTEAndBareAST covers two gaps Access() used to
// miss because it only recognized sqlkit's typed SelectQuery/UnionQuery and
// the sql package's *Builder types, never a bare parsed/hand-built AST (what
// a parser, or RawSQL given a sql.Query, hands back) nor a data-modifying CTE
// under a top-level SELECT. Both must report AccessWrite — a false AccessRead
// here would route a write to a read replica.
func TestStatementAccessWritableCTEAndBareAST(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}

	// A read-only CTE stays a read.
	readCTE := sql.Select(sql.Col("id")).From(users).CTE("recent")
	readQuery := sql.Select(sql.Star()).From(readCTE).WithCTE(readCTE)
	if got := (sqlkit.Statement{Op: sqlkit.OpQuery, Query: readQuery}).Access(); got != sqlkit.AccessRead {
		t.Errorf("read-only CTE Access() = %v, want AccessRead", got)
	}

	// A data-modifying CTE writes even though the top-level statement is a
	// SELECT: WITH ins AS (INSERT ... RETURNING ...) SELECT * FROM ins.
	insert := sql.InsertInto(users).Columns("email").Values("a@example.com").Returning(sql.Col("id"))
	writeCTE := readCTE.WithBody(insert)
	writeQuery := sql.Select(sql.Star()).From(writeCTE).WithCTE(writeCTE)
	if got := (sqlkit.Statement{Op: sqlkit.OpQuery, Query: writeQuery}).Access(); got != sqlkit.AccessWrite {
		t.Errorf("data-modifying CTE Access() = %v, want AccessWrite (would route the write to a read replica)", got)
	}

	// A bare sql.SelectStmt (not wrapped in sql.SelectBuilder) — what a parser,
	// or RawSQL given a built sql.Query, produces — with a locking clause.
	bareLocking := sql.Select(sql.Star()).From(users).ForUpdate().Stmt()
	if got := (sqlkit.Statement{Op: sqlkit.OpQuery, Query: bareLocking}).Access(); got != sqlkit.AccessWrite {
		t.Errorf("bare locking SelectStmt Access() = %v, want AccessWrite", got)
	}

	// A bare sql.SelectStmt without a locking clause stays a read.
	bareRead := sql.Select(sql.Star()).From(users).Stmt()
	if got := (sqlkit.Statement{Op: sqlkit.OpQuery, Query: bareRead}).Access(); got != sqlkit.AccessRead {
		t.Errorf("bare SelectStmt Access() = %v, want AccessRead", got)
	}
}

// TestStatementKindString covers StatementKind.String over every kind plus an
// out-of-range value, which renders the diagnostic label.
func TestStatementKindString(t *testing.T) {
	cases := map[sqlkit.StatementKind]string{
		sqlkit.KindSelect:        "select",
		sqlkit.KindInsert:        "insert",
		sqlkit.KindUpdate:        "update",
		sqlkit.KindDelete:        "delete",
		sqlkit.KindMerge:         "merge",
		sqlkit.KindDDL:           "ddl",
		sqlkit.KindRawRead:       "raw-read",
		sqlkit.KindRawUnknown:    "raw-unknown",
		sqlkit.StatementKind(99): "unknown",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Fatalf("StatementKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}

// TestAccessString covers Access.String over each access plus an out-of-range
// value.
func TestAccessString(t *testing.T) {
	cases := map[sqlkit.Access]string{
		sqlkit.AccessRead:    "read",
		sqlkit.AccessWrite:   "write",
		sqlkit.AccessUnknown: "unknown",
		sqlkit.Access(99):    "invalid",
	}
	for access, want := range cases {
		if got := access.String(); got != want {
			t.Fatalf("Access(%d).String() = %q, want %q", access, got, want)
		}
	}
}

func TestStatementTables(t *testing.T) {
	cases := []struct {
		name string
		stmt sqlkit.Statement
		want string
	}{
		{"select", sqlkit.Statement{Query: sqlkit.Select().From(Users)}, "public.users"},
		{"insert", sqlkit.Statement{Query: sqlkit.Insert(Posts)}, "public.posts"},
		{"update", sqlkit.Statement{Query: sqlkit.Update(Users)}, "public.users"},
		{"delete", sqlkit.Statement{Query: sqlkit.Delete(Posts)}, "public.posts"},
		{"merge", sqlkit.Statement{Query: sqlkit.Merge(Users)}, "public.users"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tables := tc.stmt.Tables()
			if len(tables) != 1 || tables[0] != tc.want {
				t.Fatalf("Tables() = %v, want [%s]", tables, tc.want)
			}
		})
	}

	// A raw-string statement exposes no AST table, so Tables is the empty
	// "no opinion" result.
	if tables := (sqlkit.Statement{Op: sqlkit.OpQuery, SQL: "SELECT 1"}).Tables(); tables != nil {
		t.Fatalf("raw Tables() = %v, want nil", tables)
	}
}
