// Package dbtest provides helpers for tests that insert data into a database
// with sqlkit. It cuts the boilerplate of seeding fixtures and managing the
// schema, leaving the connection (and any test container) to the caller.
//
// Every helper takes *testing.T and fails the test with t.Fatalf on error, so
// callers write linear setup code with no error handling. The insert helpers
// accept a sqlkit.Querier — the interface both *sqlkit.Database (auto-commit)
// and *sqlkit.Session (a transaction) implement — so the same helper seeds data
// directly or inside a transaction.
//
// The package smooths over the main cross-dialect wrinkle in seeding: reading
// back a database-generated key. InsertID uses RETURNING on PostgreSQL and
// Result.LastInsertId on MySQL, so fixture code that wires up foreign keys works
// on both without branching.
//
// Four groups of helpers:
//
//   - Data insertion: Insert, InsertID, and InsertReturning.
//   - Fixtures: Fixture, a named, ordered set of rows across tables, with Load.
//   - Schema lifecycle: SetupSchema (apply now, drop on t.Cleanup), the lower
//     level ApplySchema, and Truncate.
//   - Snapshots: Snapshot (and Fixture.LoadSnapshot) capture the seeded state of
//     a session as a SAVEPOINT and return a restore function, so many cases
//     reuse one seed by rolling back between them instead of reloading it.
//
// Foreign-key ordering is the caller's responsibility: insert parent rows
// before the children that reference them, exactly as in hand-written seed code.
//
// dbtest depends only on sqlkit's core packages and the standard library, so
// importing it pulls in no database driver or test-container machinery.
package dbtest
