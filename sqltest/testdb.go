package sqltest

import (
	stdsql "database/sql"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/meta"
)

// DB is a throwaway test database: a thin handle over a *sqlkit.Database that
// owns the destructive test operations — applying and dropping the schema,
// truncating tables, and opening isolated per-test transactions — alongside
// auto-commit seeding sugar.
//
// Constructing a DB is itself the safety bar. The drop and truncate operations
// live only as methods, reachable only through a DB a test deliberately built
// over a disposable database, so a *sqlkit.Database wired to a staging or
// production DSN — a stray environment variable, the wrong config profile — can
// never be wiped by a test that did not wrap it here. Construction touches
// nothing: SetupSchema and ApplySchema are the explicit steps that change the
// database, never New.
//
// Do not confuse DB with NewDB: NewDB builds a programmable mock *sql.DB for
// unit tests that must not touch a database at all, whereas a DB drives a real
// one. The two halves of the package are independent.
type DB struct {
	db *sqlkit.Database
	md meta.Metadata
}

// New wraps db as a throwaway test database. It only builds the handle — it
// issues no SQL and changes nothing — so the schema is applied by an explicit
// SetupSchema (or ApplySchema), never as a side effect of construction:
//
//	tdb := sqltest.New(database, md)
//	tdb.SetupSchema(t) // drop + create now, dropped again on t.Cleanup
//	id := tdb.InsertID(t, appdb.Users, UserDTO{Email: "a@example.com"})
//	tdb.InsertRows(t, appdb.Posts, PostDTO{UserID: id, Title: "hello"})
//
// md is the meta.Metadata a schema catalog produces (decl.Catalog.Build). db is
// a *sqlkit.Database the caller opened against a disposable test database — a
// container, a CI service database, or a scratch schema — never production.
func New(db *sqlkit.Database, md meta.Metadata) *DB {
	return &DB{db: db, md: md}
}

// Database returns the underlying *sqlkit.Database, for running queries, seeding
// through a sqlkit.Querier helper (InsertRows, Fixture.Load, …), or any sqlkit
// operation the DB does not wrap.
func (d *DB) Database() *sqlkit.Database { return d.db }

// SetupSchema gives the test a clean schema and tears it down automatically: it
// applies md's schema now — a foreign-key-safe drop of any existing tables
// followed by the CREATE DDL md renders for the dialect — then registers a
// t.Cleanup that drops the tables again, leaving the database empty. Call it once
// at the top of a test, or of a parent test whose subtests share the schema.
func (d *DB) SetupSchema(t testing.TB) {
	t.Helper()
	applySchema(t, d.db, d.md)
	t.Cleanup(func() { dropTables(t, d.db, d.md) })
}

// ApplySchema applies md's schema — a foreign-key-safe drop of the existing
// tables followed by the CREATE DDL — registering no teardown. It is SetupSchema
// without the automatic drop on cleanup: reach for it to reset the schema again
// within a test, or when something else owns the teardown.
func (d *DB) ApplySchema(t testing.TB) {
	t.Helper()
	applySchema(t, d.db, d.md)
}

// Truncate deletes every row from the given tables foreign-key-safely, whatever
// order they were declared in. Called with no tables it clears every table in
// the schema. It is a faster between-cases reset than re-applying the schema
// when the tables already exist and only their data needs clearing.
func (d *DB) Truncate(t testing.TB, tables ...sqlkit.Table) {
	t.Helper()
	truncate(t, d.db, d.md, tables)
}

// Tx gives the test its own transaction and rolls it back automatically when the
// test finishes. Each call opens a separate sqlkit.Session — a transaction on
// its own pooled connection — so cases that share the one schema stay isolated
// from each other's writes and can run in parallel: nothing a case inserts,
// updates, or deletes is visible to another, and the rollback leaves the
// database exactly as the next case (or the next run) finds it.
//
// Apply the schema once for the group, then hand each case its own transaction:
//
//	func TestBlog(t testing.TB) {
//		tdb := sqltest.New(database, md)
//		tdb.SetupSchema(t) // schema created once, dropped after all subtests
//		t.Run("publish", func(t testing.TB) {
//			t.Parallel()
//			s := tdb.Tx(t)  // isolated transaction, rolled back on return
//			blog.Load(t, s) // seed inside the transaction
//			// ... mutate and assert against s ...
//		})
//		t.Run("archive", func(t testing.TB) {
//			t.Parallel()
//			s := tdb.Tx(t)  // sees none of "publish"'s writes
//			// ...
//		})
//	}
//
// SetupSchema's teardown runs on the parent test's cleanup, which Go schedules
// after every parallel subtest has finished, so the shared schema outlives the
// cases.
//
// The returned *sqlkit.Session is a sqlkit.Querier, so it drops into the seeding
// helpers (InsertRows, InsertID, Fixture.Load, …) and starts query chains
// (s.Select()…). The transaction begins lazily on the first statement, so a case
// that runs nothing costs nothing. Pass sqlkit.SessionOption values
// (WithTxOptions, …) to control isolation level or read-only intent.
//
// Tx isolates through a real transaction per connection, which scales to
// parallel cases; Snapshot isolates through savepoints on one shared, serial
// session. Reach for Tx when cases run in parallel or each wants a clean slate,
// and for Snapshot when many serial cases reuse one expensive seed.
func (d *DB) Tx(t testing.TB, opts ...sqlkit.SessionOption) *sqlkit.Session {
	t.Helper()
	s := d.db.NewSession(opts...)
	t.Cleanup(func() {
		// Roll back the case's writes. A session that ran nothing, or that the
		// test already finished itself, reports ErrTxDone, which is not a failure.
		if err := s.Rollback(); err != nil && !errors.Is(err, stdsql.ErrTxDone) {
			t.Errorf("sqltest: roll back test transaction: %v", err)
		}
	})
	return s
}

// InsertRows seeds rows into table through auto-commit (the underlying
// database), failing the test on error. It is shorthand for InsertRows(t,
// d.Database(), table, rows...); seed into a Tx session instead when cases run in
// parallel, so the rows roll back with the case rather than persisting.
func (d *DB) InsertRows(t testing.TB, table sqlkit.Table, rows ...any) {
	t.Helper()
	InsertRows(t, d.db, table, rows...)
}

// InsertID inserts a single row through auto-commit and returns its
// database-generated primary key. It is shorthand for InsertID(t, d.Database(),
// table, row); see that function for the cross-dialect key read-back.
func (d *DB) InsertID(t testing.TB, table sqlkit.Table, row any) int64 {
	t.Helper()
	return InsertID(t, d.db, table, row)
}

// Load seeds a fixture through auto-commit. It is shorthand for f.Load(t,
// d.Database()); load into a Tx session instead when cases run in parallel.
func (d *DB) Load(t testing.TB, f *Fixture) {
	t.Helper()
	f.Load(t, d.db)
}
