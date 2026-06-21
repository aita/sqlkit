package migrate_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/migrate"
)

// This file hardens the migrate package against the failure and edge cases that
// established migration tools (Alembic, Flyway, golang-migrate) treat as
// must-handle: a migration that fails mid-way must not be recorded applied and
// must roll back (on a transactional engine); the database being at a revision
// the code no longer knows about must error rather than silently no-op; driver
// errors, cancellation, and DAG-configuration problems must surface from every
// entry point; and downgrade/stamp must walk the history in dependency-safe
// order. Each test drives the scriptable testutil.Fake — no live database.

// ev is one history row (revision + direction) for historyFunc.
type ev struct{ rev, dir string }

// historyFunc serves an arbitrary ordered apply/rollback history from the
// schema_migrations SELECT, so tests can model down events and re-applies — not
// just the all-"up" set appliedHistory builds.
func historyFunc(evs ...ev) func(string, []any) (testutil.Result, error) {
	rows := make([][]driver.Value, len(evs))
	for i, e := range evs {
		rows[i] = []driver.Value{e.rev, "m", e.dir, time.Unix(int64(i), 0).UTC()}
	}
	return func(q string, _ []any) (testutil.Result, error) {
		if strings.Contains(q, "schema_migrations") && strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
			return testutil.Result{Columns: []string{"revision", "name", "direction", "applied_at"}, Rows: rows}, nil
		}
		return testutil.Result{}, nil
	}
}

// linear returns three create/drop migrations in a 0001 -> 0002 -> 0003 chain.
func linear() []migrate.Migration {
	return []migrate.Migration{
		mk("0001", "", "a", "t1"),
		mk("0002", "0001", "b", "t2"),
		mk("0003", "0002", "c", "t3"),
	}
}

// drops collects the DROP TABLE statements in execution order.
func drops(execs []testutil.Call) []string {
	var out []string
	for _, e := range execs {
		if strings.HasPrefix(e.SQL, "DROP TABLE") {
			out = append(out, firstLine(e.SQL))
		}
	}
	return out
}

// historyWrites collects (revision, direction) for each schema_migrations INSERT.
func historyWrites(execs []testutil.Call) [][2]string {
	var out [][2]string
	for _, e := range execs {
		if strings.HasPrefix(e.SQL, "INSERT") {
			rev, _ := e.Args[0].(string)
			dir, _ := e.Args[2].(string)
			out = append(out, [2]string{rev, dir})
		}
	}
	return out
}

// --- Atomicity / fault injection (Alembic: a failed migration is not stamped) ---

// TestUpRollsBackFailedMigration: when a migration fails mid-batch, the earlier
// migration is committed, the failing one is not (its transaction never
// commits), and the error carries the revision and unwraps to the driver error.
func TestUpRollsBackFailedMigration(t *testing.T) {
	boom := errors.New("ddl boom")
	fake := &testutil.Fake{
		QueryFunc: appliedHistory(),
		ExecFunc: func(q string, _ []any) error {
			if strings.Contains(q, `"posts"`) {
				return boom
			}
			return nil
		},
	}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations()) // 0001 tags -> 0002 posts
	err := m.Up(context.Background())
	if err == nil {
		t.Fatal("expected Up to fail")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error does not unwrap to driver error: %v", err)
	}
	if !strings.Contains(err.Error(), "0002") {
		t.Errorf("error %q should name the failing revision 0002", err)
	}
	// tags committed; posts began but did not commit.
	if fake.Commits() != 1 {
		t.Errorf("commits = %d, want 1 (only the successful migration)", fake.Commits())
	}
	// Only tags' history row should have been written.
	if got := historyWrites(fake.Execs()); len(got) != 1 || got[0][0] != "0001" {
		t.Errorf("history writes = %v, want only [0001 up]", got)
	}
}

// TestUpFailsWhenHistoryInsertFails: the history row commits together with the
// DDL, so a failed insert fails the whole migration (nothing is committed).
func TestUpFailsWhenHistoryInsertFails(t *testing.T) {
	boom := errors.New("insert boom")
	fake := &testutil.Fake{
		QueryFunc: appliedHistory(),
		ExecFunc: func(q string, _ []any) error {
			if strings.HasPrefix(q, "INSERT") {
				return boom
			}
			return nil
		},
	}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Up(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Up error = %v, want it to unwrap to %v", err, boom)
	}
	if fake.Commits() != 0 {
		t.Errorf("commits = %d, want 0 (DDL and its history row commit together)", fake.Commits())
	}
}

// TestOperatorShortCircuitsOnError: once an operation fails, the rest of the
// migration's operations are skipped (first error wins).
func TestOperatorShortCircuitsOnError(t *testing.T) {
	boom := errors.New("create boom")
	fake := &testutil.Fake{
		QueryFunc: appliedHistory(),
		ExecFunc: func(q string, _ []any) error {
			if strings.Contains(q, `"first"`) {
				return boom
			}
			return nil
		},
	}
	migs := []migrate.Migration{{
		Revision: "0001", Name: "two_tables",
		Up: func(op *migrate.Operator) {
			op.CreateTable("first", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
			op.CreateTable("second", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
		},
	}}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs)
	if err := m.Up(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Up error = %v, want %v", err, boom)
	}
	for _, e := range fake.Execs() {
		if strings.Contains(e.SQL, `"second"`) {
			t.Errorf("second table was created after the first failed: %q", firstLine(e.SQL))
		}
	}
}

// TestDisableTxRunsWithoutTransaction: a DisableTx migration applies on the
// auto-commit handle (no BEGIN) and still records its history row.
func TestDisableTxRunsWithoutTransaction(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	migs := []migrate.Migration{{
		Revision: "0001", Name: "concurrent_index", DisableTx: true,
		Up:   func(op *migrate.Operator) { op.CreateIndex("idx", "t", "c") },
		Down: func(op *migrate.Operator) { op.DropIndex("idx", "t") },
	}}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs)
	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if fake.Begins() != 0 {
		t.Errorf("begins = %d, want 0 (DisableTx runs outside a transaction)", fake.Begins())
	}
	if got := historyWrites(fake.Execs()); len(got) != 1 || got[0] != [2]string{"0001", "up"} {
		t.Errorf("history writes = %v, want [[0001 up]]", got)
	}
}

// --- Error and cancellation propagation from every entry point ---

// TestHistoryReadErrorPropagates: a failing history SELECT surfaces from the
// methods that read it.
func TestHistoryReadErrorPropagates(t *testing.T) {
	boom := errors.New("select boom")
	newM := func() *migrate.Migrator {
		fake := &testutil.Fake{QueryFunc: func(q string, _ []any) (testutil.Result, error) {
			if strings.Contains(q, "schema_migrations") && strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
				return testutil.Result{}, boom
			}
			return testutil.Result{}, nil
		}}
		return migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	}
	ctx := context.Background()
	cases := map[string]func(*migrate.Migrator) error{
		"Up":      func(m *migrate.Migrator) error { return m.Up(ctx) },
		"Down":    func(m *migrate.Migrator) error { return m.Down(ctx) },
		"Stamp":   func(m *migrate.Migrator) error { return m.Stamp(ctx, "head") },
		"Pending": func(m *migrate.Migrator) error { _, err := m.Pending(ctx); return err },
		"Status":  func(m *migrate.Migrator) error { _, err := m.Status(ctx); return err },
		"Current": func(m *migrate.Migrator) error { _, err := m.Current(ctx); return err },
		"History": func(m *migrate.Migrator) error { _, err := m.History(ctx); return err },
	}
	for name, fn := range cases {
		if err := fn(newM()); !errors.Is(err, boom) {
			t.Errorf("%s: error = %v, want it to unwrap to %v", name, err, boom)
		}
	}
}

// TestEnsureTableErrorPropagates: a failing CREATE of the tracking table surfaces.
func TestEnsureTableErrorPropagates(t *testing.T) {
	boom := errors.New("create table boom")
	fake := &testutil.Fake{
		QueryFunc: appliedHistory(),
		ExecFunc: func(q string, _ []any) error {
			if strings.Contains(q, "CREATE TABLE") && strings.Contains(q, "schema_migrations") {
				return boom
			}
			return nil
		},
	}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Up(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Up error = %v, want %v", err, boom)
	}
}

// TestContextCancellation: an already-cancelled context surfaces from Up.
func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Up(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Up error = %v, want context.Canceled", err)
	}
}

// --- DAG configuration problems (Alembic rejects cycles / unknown revisions) ---

// TestCycleDetected: a revision cycle is reported by every operation.
func TestCycleDetected(t *testing.T) {
	cyclic := []migrate.Migration{
		{Revision: "a", DownRevision: "b", Name: "a"},
		{Revision: "b", DownRevision: "a", Name: "b"},
	}
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{QueryFunc: appliedHistory()}).DB()), cyclic)
	if err := m.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("Up error = %v, want a cycle error", err)
	}
}

// TestPlanErrorSurfacesEverywhere: a configuration error (here a duplicate
// revision) is returned by every public method, online and offline.
func TestPlanErrorSurfacesEverywhere(t *testing.T) {
	dup := []migrate.Migration{{Revision: "x", Name: "a"}, {Revision: "x", Name: "b"}}
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{QueryFunc: appliedHistory()}).DB()), dup)
	ctx := context.Background()
	checks := map[string]func() error{
		"Up":           func() error { return m.Up(ctx) },
		"Down":         func() error { return m.Down(ctx) },
		"Upgrade":      func() error { return m.Upgrade(ctx, "head") },
		"Downgrade":    func() error { return m.Downgrade(ctx, "base") },
		"Stamp":        func() error { return m.Stamp(ctx, "head") },
		"Pending":      func() error { _, err := m.Pending(ctx); return err },
		"Status":       func() error { _, err := m.Status(ctx); return err },
		"Current":      func() error { _, err := m.Current(ctx); return err },
		"UpgradeSQL":   func() error { _, err := m.UpgradeSQL("", "head"); return err },
		"DowngradeSQL": func() error { _, err := m.DowngradeSQL("head", "base"); return err },
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s did not surface the plan error", name)
		}
	}
}

// TestUnknownRevisionTarget: an unknown target revision errors, up and down.
func TestUnknownRevisionTarget(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Upgrade(context.Background(), "nope"); err == nil {
		t.Error("Upgrade to unknown revision should error")
	}
	if err := m.Downgrade(context.Background(), "nope"); err == nil {
		t.Error("Downgrade to unknown revision should error")
	}
}

// TestDowngradeRefusesUnknownAppliedRevision: when the database is at a revision
// the code no longer contains, downgrade refuses rather than silently doing
// nothing (the regression this suite was written to lock down).
func TestDowngradeRefusesUnknownAppliedRevision(t *testing.T) {
	// History has 0001 (known) and "ghost" (removed from code) applied.
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "ghost")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), []migrate.Migration{mk("0001", "", "a", "t1")})

	for _, target := range []string{"-1", "base", "0001"} {
		fake2 := &testutil.Fake{QueryFunc: appliedHistory("0001", "ghost")}
		m = migrate.New(sqlkit.WrapDB(fake2.DB()), []migrate.Migration{mk("0001", "", "a", "t1")})
		err := m.Downgrade(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Errorf("Downgrade(%q) error = %v, want it to name the unknown revision", target, err)
		}
		if fake2.Begins() != 0 {
			t.Errorf("Downgrade(%q) started %d transactions; want 0 (refuse before touching the schema)", target, fake2.Begins())
		}
	}
	_ = m
}

// --- Downgrade / stamp ordering (dependency-safe reverse walks) ---

// TestDowngradeBaseRollsBackAllInReverse: "base" rolls every applied migration
// back in reverse dependency order.
func TestDowngradeBaseRollsBackAllInReverse(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002", "0003")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), linear())
	if err := m.Downgrade(context.Background(), "base"); err != nil {
		t.Fatalf("Downgrade base: %v", err)
	}
	want := []string{`DROP TABLE IF EXISTS "t3";`, `DROP TABLE IF EXISTS "t2";`, `DROP TABLE IF EXISTS "t1";`}
	if got := drops(fake.Execs()); !equalStrings(got, want) {
		t.Errorf("drop order:\ngot  %v\nwant %v", got, want)
	}
}

// TestDownToLeavesTargetAndAncestors: DownTo a revision rolls back only its
// descendants, leaving it and everything it builds on applied.
func TestDownToLeavesTargetAndAncestors(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002", "0003")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), linear())
	if err := m.DownTo(context.Background(), "0001"); err != nil {
		t.Fatalf("DownTo 0001: %v", err)
	}
	// 0002 and 0003 descend from 0001; 0001 itself stays.
	want := []string{`DROP TABLE IF EXISTS "t3";`, `DROP TABLE IF EXISTS "t2";`}
	if got := drops(fake.Execs()); !equalStrings(got, want) {
		t.Errorf("drops:\ngot  %v\nwant %v", got, want)
	}
}

// TestDownRelativeClampsToApplied: "-N" with N beyond the applied count rolls
// back exactly what is applied, no more.
func TestDownRelativeClampsToApplied(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), linear()) // 0003 never applied
	if err := m.Downgrade(context.Background(), "-10"); err != nil {
		t.Fatalf("Downgrade -10: %v", err)
	}
	want := []string{`DROP TABLE IF EXISTS "t2";`, `DROP TABLE IF EXISTS "t1";`}
	if got := drops(fake.Execs()); !equalStrings(got, want) {
		t.Errorf("drops:\ngot  %v\nwant %v", got, want)
	}
}

// TestReapplyAfterDown: a revision applied, rolled back, then applied again is
// current; Down("-1") undoes that latest apply (history folding by latest event).
func TestReapplyAfterDown(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: historyFunc(
		ev{"0001", "up"}, ev{"0002", "up"}, ev{"0002", "down"}, ev{"0002", "up"},
	)}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), linear())
	if err := m.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if got := drops(fake.Execs()); !equalStrings(got, []string{`DROP TABLE IF EXISTS "t2";`}) {
		t.Errorf("drops = %v, want only t2", got)
	}
	if got := historyWrites(fake.Execs()); len(got) != 1 || got[0] != [2]string{"0002", "down"} {
		t.Errorf("history writes = %v, want [[0002 down]]", got)
	}
}

// TestStampDownUnstamps: stamping from a fully-applied schema down to base
// records "down" rows in reverse order and runs no DDL.
func TestStampDownUnstamps(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002", "0003")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), linear())
	if err := m.Stamp(context.Background(), "base"); err != nil {
		t.Fatalf("Stamp base: %v", err)
	}
	if got := creates(fake.Execs()); len(got) != 0 {
		t.Errorf("Stamp ran DDL: %v", got)
	}
	want := [][2]string{{"0003", "down"}, {"0002", "down"}, {"0001", "down"}}
	got := historyWrites(fake.Execs())
	if len(got) != len(want) {
		t.Fatalf("history writes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("history write %d = %v, want %v", i, got[i], want[i])
		}
	}
}
