package migrate_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/migrate"
)

// lockingFake wraps a testutil.Fake with real mutual exclusion around the
// advisory-lock query (SELECT pg_advisory_lock / GET_LOCK), so two Migrators
// racing over one shared Fake reproduce genuine lock contention instead of
// merely running the same canned responses back to back. It also tracks the
// applied revisions itself (rather than a fixed canned set) so a second
// Migrator that only starts once the first has fully committed observes the
// first one's write — the scenario this test exists to prove.
type lockingFake struct {
	lockMu sync.Mutex // the simulated advisory lock itself: held from lock query to unlock query

	stateMu sync.Mutex // guards applied, independent of the simulated advisory lock
	applied map[string]bool
}

func newLockingFake() *lockingFake {
	return &lockingFake{applied: map[string]bool{}}
}

func (lf *lockingFake) queryFunc(q string, _ []any) (testutil.Result, error) {
	switch {
	case strings.Contains(q, "pg_advisory_lock") || strings.Contains(q, "GET_LOCK"):
		lf.lockMu.Lock()
		return testutil.Result{Columns: []string{"lock"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(q, "pg_advisory_unlock") || strings.Contains(q, "RELEASE_LOCK"):
		lf.lockMu.Unlock()
		return testutil.Result{Columns: []string{"unlock"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(q, "schema_migrations") && strings.HasPrefix(strings.TrimSpace(q), "SELECT"):
		lf.stateMu.Lock()
		defer lf.stateMu.Unlock()
		var rows [][]driver.Value
		for rev := range lf.applied {
			rows = append(rows, []driver.Value{rev, "m", "up", time.Unix(0, 0).UTC()})
		}
		return testutil.Result{Columns: []string{"revision", "name", "direction", "applied_at"}, Rows: rows}, nil
	default:
		return testutil.Result{}, nil
	}
}

func (lf *lockingFake) execFunc(q string, args []any) error {
	if strings.HasPrefix(q, "INSERT") && strings.Contains(q, "schema_migrations") {
		lf.stateMu.Lock()
		defer lf.stateMu.Unlock()
		if rev, ok := args[0].(string); ok {
			lf.applied[rev] = true
		}
	}
	return nil
}

// oneMigration is a single-revision migration set shared by both Migrators in
// the race below, so they contend over the exact same revision.
func oneMigration() []migrate.Migration {
	return []migrate.Migration{
		{
			Revision: "0001",
			Name:     "create_widgets",
			Up: func(op *migrate.Operator) {
				op.CreateTable("widgets", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
			},
			Down: func(op *migrate.Operator) { op.DropTable("widgets") },
		},
	}
}

// TestConcurrentUpSerializesOnAdvisoryLock reproduces the double-apply race:
// two Migrator instances sharing one backend (two pods mid-rollout) both call
// Up() concurrently against the same pending revision. Without the advisory
// lock in acquireLock, both would read "0001 not applied" before either
// commits and both would run its CREATE TABLE + history insert. With it, the
// second Migrator's appliedRevisions read cannot proceed until the first has
// released the lock (after its own apply committed), so it observes "0001
// applied" and skips — the table is created exactly once.
func TestConcurrentUpSerializesOnAdvisoryLock(t *testing.T) {
	lf := newLockingFake()
	fake := &testutil.Fake{QueryFunc: lf.queryFunc, ExecFunc: lf.execFunc}

	db1 := sqlkit.WrapDB(fake.DB())
	db2 := sqlkit.WrapDB(fake.DB())
	m1 := migrate.New(db1, oneMigration())
	m2 := migrate.New(db2, oneMigration())

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = m1.Up(context.Background()) }()
	go func() { defer wg.Done(); errs[1] = m2.Up(context.Background()) }()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Up() [migrator %d] error = %v", i, err)
		}
	}

	var creates int
	for _, e := range fake.Execs() {
		if strings.HasPrefix(firstLine(e.SQL), `CREATE TABLE "widgets"`) {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("widgets created %d times, want exactly 1 (the advisory lock should have serialized the two Up() calls)", creates)
	}

	var inserts int
	for _, e := range fake.Execs() {
		if strings.HasPrefix(e.SQL, "INSERT") && strings.Contains(e.SQL, "schema_migrations") {
			inserts++
		}
	}
	if inserts != 1 {
		t.Errorf("schema_migrations history rows written %d times, want exactly 1", inserts)
	}
}

// TestUpgradeLocksAndUnlocksAroundApply asserts the lock/unlock calls actually
// bracket the read-and-apply sequence for a single Migrator: acquired before
// the history SELECT, held across the migration's own INSERT, and released
// only once the migration commits. This nails down the ordering the
// concurrency test above depends on, without needing two goroutines.
func TestUpgradeLocksAndUnlocksAroundApply(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, oneMigration())

	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	var order []string
	for _, s := range fake.Statements() {
		switch {
		case strings.Contains(s, "pg_advisory_lock"):
			order = append(order, "lock")
		case strings.Contains(s, "pg_advisory_unlock"):
			order = append(order, "unlock")
		case strings.HasPrefix(strings.TrimSpace(s), "SELECT") && strings.Contains(s, "schema_migrations"):
			order = append(order, "read-history")
		case strings.HasPrefix(s, `CREATE TABLE "widgets"`):
			order = append(order, "apply-ddl")
		case strings.HasPrefix(s, "INSERT") && strings.Contains(s, "schema_migrations"):
			order = append(order, "record-history")
		}
	}
	want := []string{"lock", "read-history", "apply-ddl", "record-history", "unlock"}
	if !equalStrings(order, want) {
		t.Errorf("statement order = %v, want %v", order, want)
	}
}

// TestUpgradeLockReleasedOnApplyFailure: a failed migration still releases
// the lock rather than leaking it and starving every future runner.
func TestUpgradeLockReleasedOnApplyFailure(t *testing.T) {
	boom := "ddl boom"
	fake := &testutil.Fake{
		QueryFunc: appliedHistory(),
		ExecFunc: func(q string, _ []any) error {
			if strings.Contains(q, `"widgets"`) {
				return errBoom(boom)
			}
			return nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, oneMigration())

	if err := m.Up(context.Background()); err == nil {
		t.Fatal("expected Up to fail")
	}

	var lockCount, unlockCount int
	for _, s := range fake.Statements() {
		if strings.Contains(s, "pg_advisory_lock") {
			lockCount++
		}
		if strings.Contains(s, "pg_advisory_unlock") {
			unlockCount++
		}
	}
	if lockCount != 1 || unlockCount != 1 {
		t.Errorf("lock calls = %d, unlock calls = %d, want 1 and 1 (lock released even on failure)", lockCount, unlockCount)
	}
}

// TestUpgradeMySQLUsesGetLock checks the MySQL dialect issues GET_LOCK /
// RELEASE_LOCK rather than the PostgreSQL advisory-lock functions.
func TestUpgradeMySQLUsesGetLock(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))
	m := migrate.New(db, oneMigration())

	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	var sawGetLock, sawReleaseLock, sawPgLock bool
	for _, s := range fake.Statements() {
		if strings.Contains(s, "GET_LOCK") {
			sawGetLock = true
		}
		if strings.Contains(s, "RELEASE_LOCK") {
			sawReleaseLock = true
		}
		if strings.Contains(s, "pg_advisory") {
			sawPgLock = true
		}
	}
	if !sawGetLock || !sawReleaseLock {
		t.Errorf("MySQL Up() should call GET_LOCK/RELEASE_LOCK: statements=%v", fake.Statements())
	}
	if sawPgLock {
		t.Errorf("MySQL Up() should not call pg_advisory_lock/unlock: statements=%v", fake.Statements())
	}
}

// errBoom is a trivial error type so ExecFunc can return a distinct failure
// without importing errors just for one sentinel in this file.
type errBoom string

func (e errBoom) Error() string { return string(e) }
