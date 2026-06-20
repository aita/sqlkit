// Package sqltest is sqlkit's toolkit for testing SQL code. It brings together
// two complementary halves: helpers that seed and manage a real database, and a
// spy-and-mock pair built on querytrace for asserting on the queries code
// issues. Import the one package whether a test needs a seeded database, a query
// budget, or both.
//
// # Seeding a database
//
// The seeding helpers cut the boilerplate of managing the schema and inserting
// fixtures, leaving the connection (and any test container) to the caller. Every
// helper takes *testing.T and fails the test with t.Fatalf on error, so callers
// write linear setup code with no error handling.
//
// A DB is the entry point. New wraps a *sqlkit.Database the caller opened against
// a disposable test database, returning a handle whose methods reset, isolate,
// and seed it. New itself changes nothing; an explicit SetupSchema applies the
// schema and drops it again on cleanup:
//
//	tdb := sqltest.New(database, md)
//	tdb.SetupSchema(t) // schema applied now, dropped on t.Cleanup
//	id := tdb.InsertID(t, appdb.Users, UserDTO{Email: "a@example.com"})
//	tdb.InsertRows(t, appdb.Posts, PostDTO{UserID: id, Title: "hello"})
//
// Constructing a DB is the safety bar: the destructive operations (applying or
// dropping the schema, truncating tables) live only as DB methods, so a database
// the test did not deliberately wrap as throwaway is never dropped.
//
//   - Schema lifecycle: DB.SetupSchema (apply now, drop on t.Cleanup), and the
//     methods DB.ApplySchema (apply, no teardown) and DB.Truncate for resetting
//     within a test. Their drop and truncate steps are foreign-key-safe regardless
//     of how the tables were declared — children before or after parents,
//     cross-schema, or in a cycle.
//   - Isolation: DB.Tx hands each case its own transaction, rolled back on
//     t.Cleanup, so cases that share one schema stay independent and may run in
//     parallel. Snapshot (and Fixture.LoadSnapshot) instead capture the seeded
//     state of a single session as a SAVEPOINT and return a restore function, so
//     many serial cases reuse one seed by rolling back between them.
//   - Data insertion: the DB.InsertRows / DB.InsertID / DB.Load methods seed
//     through auto-commit; the InsertRows, InsertID, InsertReturning, and
//     Fixture.Load functions take any sqlkit.Querier, so they seed into a Tx
//     session (isolated, rolled back with the case) just as well.
//   - Fixtures: Fixture, a named, ordered set of rows across tables, with Load.
//
// The insert helpers smooth over the main cross-dialect wrinkle in seeding:
// reading back a database-generated key. InsertID uses RETURNING on PostgreSQL
// and Result.LastInsertId on MySQL, so fixture code that wires up foreign keys
// works on both without branching. Foreign-key ordering of the rows is the
// caller's responsibility: insert parent rows before the children that reference
// them, exactly as in hand-written seed code.
//
// # Asserting on queries
//
// The spy turns a querytrace.Trace into a record of the SQL one operation
// actually issued, and asserts on it after the fact. Where a mock library makes
// you declare the queries up front and fails if they do not run, the spy works
// the other way around — run the code under a Trace, then ask what it did: a
// query budget, no writes, no N+1, a SELECT on a given table that ran exactly
// once, an INSERT before the COMMIT.
//
// The entry point is Expect, a fluent assertion bound to a testing.TB:
//
//	trace := querytrace.New("GetUserPage")
//	ctx := querytrace.WithTrace(context.Background(), trace)
//	service.RenderUserPage(ctx, id)
//
//	sqltest.Expect(t, trace).
//		MaxQueries(5).
//		NoWrites().
//		NoNPlusOne().
//		Query(sqltest.Select(), sqltest.Table("users")).Times(1)
//
// Match values (Select, Table, Fingerprint, SqlContains, Caller, HasTopLevelWhere, …)
// select the statements an assertion applies to; pass several and they must all
// hold. All, Any, and Not compose them. Count and Events expose the same matching
// without a testing.TB, for inspecting a Trace directly.
//
// # Mocking a database
//
// For unit tests that should not touch a database at all, NewDB returns a
// *sql.DB backed by a programmable Mock and traced by querytrace, so the mock
// provides the inputs (canned rows, results, or errors registered with
// ExpectQuery and ExpectExec) and the spy assertions verify the calls.
package sqltest
