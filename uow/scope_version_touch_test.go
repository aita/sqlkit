package uow_test

// Version/touch column handling in the snapshot-diff (diffAssignments, managed
// entities) and detached (updateAssignments, Update()) UPDATE paths: both must
// treat a `sqlkit:",version"` field as a bump-and-guard rather than a plain
// diffed column, and a `sqlkit:",touch"` field as a CURRENT_TIMESTAMP() rather
// than a copy of the struct's current value — the same special-casing
// update.go's SetStruct already applies to the direct builder path (see
// structAssignments). On the managed path both ride along with a real column
// change only: a clean entity flushes nothing, preserving uow's dirty-tracking
// contract. Split out from scope_test.go per the version/touch-tag concern
// (see TestManagedDirtyUpdatesOnlyChangedColumns and
// TestExplicitUpdateWritesAllColumns there for the plain-column case this
// mirrors).

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// accountsTable/accountRow: a table with a version column (optimistic locking)
// and a touch column (app-maintained "last updated" timestamp).

type accountsTable struct {
	info    sqlkit.TableInfo
	ID      sqlkit.Column[int64]
	Balance sqlkit.Column[int64]
	Version sqlkit.Column[int64]
	Updated sqlkit.Column[string]
}

func newAccounts() accountsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "accounts", Checksum: "accounts-checksum"}
	return accountsTable{
		info:    info,
		ID:      sqlkit.NewColumn[int64](info, "id"),
		Balance: sqlkit.NewColumn[int64](info, "balance"),
		Version: sqlkit.NewColumn[int64](info, "version"),
		Updated: sqlkit.NewColumn[string](info, "updated_at"),
	}
}
func (t accountsTable) Info() sqlkit.TableInfo             { return t.info }
func (t accountsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t accountsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.Balance, t.Version, t.Updated}
}
func (t accountsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t accountsTable) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

type accountRow struct {
	ID      int64
	Balance int64
	Version int64  `sqlkit:",version"`
	Updated string `sqlkit:"updated_at,touch"`
}

func accountResult(id, balance, version int64) func(string, []any) (testutil.Result, error) {
	return func(string, []any) (testutil.Result, error) {
		return testutil.Result{
			Columns: []string{"id", "balance", "version", "updated_at"},
			Rows:    [][]driver.Value{{id, balance, version, "2020-01-01T00:00:00Z"}},
		}, nil
	}
}

// --- managed (Get + mutate + Flush) path: diffAssignments ---

func TestManagedVersionBumpsAndGuards(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7)}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	found, err := scope.Get(ctx(), newAccounts(), &a, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	a.Balance = 150
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1 UPDATE: %v", len(execs), fake.Statements())
	}
	sqlText := execs[0].SQL
	if !strings.Contains(sqlText, `"version" = ("version" + `) {
		t.Fatalf("UPDATE SET missing version bump: %q", sqlText)
	}
	if strings.Count(sqlText, `"version"`) < 2 {
		t.Fatalf("UPDATE missing version guard predicate (want version referenced in both SET and WHERE): %q", sqlText)
	}
	// args: balance, version+1 addend(1), then WHERE id=1, version=7(guard).
	found7 := false
	for _, arg := range execs[0].Args {
		if arg == int64(7) {
			found7 = true
		}
	}
	if !found7 {
		t.Fatalf("update args = %v, want the current version (7) as the guard value", execs[0].Args)
	}
}

func TestManagedVersionConflictReturnsErrVersionConflict(t *testing.T) {
	zero := int64(0)
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7), RowsAffected: &zero}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	found, err := scope.Get(ctx(), newAccounts(), &a, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	a.Balance = 150
	err = scope.Flush(ctx())
	if !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Flush() error = %v, want ErrVersionConflict", err)
	}
}

func TestManagedTouchAlwaysSetsCurrentTimestamp(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7)}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	found, err := scope.Get(ctx(), newAccounts(), &a, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	a.Balance = 150 // touch field (Updated) left untouched by the caller
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1 UPDATE: %v", len(execs), fake.Statements())
	}
	if !strings.Contains(execs[0].SQL, `"updated_at" = CURRENT_TIMESTAMP`) {
		t.Fatalf("UPDATE SET missing touch column: %q", execs[0].SQL)
	}
}

func TestManagedUnchangedVersionTouchFlushesNothing(t *testing.T) {
	// No real column changed: the version bump and touch stamp only ride along
	// with an actual change, so a pure Get+Flush must stay a no-op (uow's
	// dirty-tracking contract) — no UPDATE, no version advance, no touch.
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7)}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	found, err := scope.Get(ctx(), newAccounts(), &a, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0 (unchanged managed row must not UPDATE): %v", got, fake.Statements())
	}
}

func TestManagedVersionGuardOnlyWithRealChange(t *testing.T) {
	// Even with RowsAffected forced to 0 (which would trip the version guard if
	// an UPDATE were issued), a clean entity flushes nothing and reports no
	// spurious ErrVersionConflict.
	zero := int64(0)
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7), RowsAffected: &zero}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	found, err := scope.Get(ctx(), newAccounts(), &a, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("Flush() = %v, want nil (no UPDATE, so no guard to conflict)", err)
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0: %v", got, fake.Statements())
	}
}

// --- detached (Update()) path: updateAssignments ---

func TestDetachedUpdateVersionBumpsAndGuards(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Update(newAccounts(), accountRow{ID: 3, Balance: 50, Version: 4, Updated: "stale"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1 UPDATE: %v", len(execs), fake.Statements())
	}
	sqlText := execs[0].SQL
	if !strings.Contains(sqlText, `"version" = ("version" + `) {
		t.Fatalf("UPDATE SET missing version bump: %q", sqlText)
	}
	found4 := false
	for _, arg := range execs[0].Args {
		if arg == int64(4) {
			found4 = true
		}
	}
	if !found4 {
		t.Fatalf("update args = %v, want the struct's version (4) as the guard value", execs[0].Args)
	}
}

func TestDetachedUpdateVersionConflictReturnsErrVersionConflict(t *testing.T) {
	zero := int64(0)
	fake := &testutil.Fake{RowsAffected: &zero}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Update(newAccounts(), accountRow{ID: 3, Balance: 50, Version: 4, Updated: "stale"}); err != nil {
		t.Fatal(err)
	}
	err := scope.Flush(ctx())
	if !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Flush() error = %v, want ErrVersionConflict", err)
	}
}

func TestDetachedUpdateTouchAlwaysSetsCurrentTimestamp(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Update(newAccounts(), accountRow{ID: 3, Balance: 50, Version: 4, Updated: "stale"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1 UPDATE: %v", len(execs), fake.Statements())
	}
	if !strings.Contains(execs[0].SQL, `"updated_at" = CURRENT_TIMESTAMP`) {
		t.Fatalf("UPDATE SET missing touch column (must not copy the stale struct value): %q", execs[0].SQL)
	}
	if strings.Contains(execs[0].SQL, "$") {
		// updated_at must not be a bound parameter carrying the struct's stale value.
		for _, arg := range execs[0].Args {
			if arg == "stale" {
				t.Fatalf("update bound the stale struct value for the touch column: args = %v", execs[0].Args)
			}
		}
	}
}

// guardArg returns the last argument of an UPDATE, which is the version guard
// value (the WHERE predicates come after the SET values: id, then version).
func guardArg(t *testing.T, args []any) any {
	t.Helper()
	if len(args) == 0 {
		t.Fatal("UPDATE has no args")
	}
	return args[len(args)-1]
}

// After a successful guarded flush the database row is at version v+1; the
// struct (and its refreshed snapshot) must follow, so a second mutate+Flush on
// the same scope guards on v+1 instead of reporting a spurious conflict.
func TestManagedVersionSecondFlushGuardsOnBumpedVersion(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7)}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	if found, err := scope.Get(ctx(), newAccounts(), &a, int64(1)); err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	a.Balance = 150
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	if a.Version != 8 {
		t.Fatalf("after first flush a.Version = %d, want 8 (bumped like the database row)", a.Version)
	}
	a.Balance = 200
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 2 {
		t.Fatalf("got %d execs, want 2 UPDATEs: %v", len(execs), fake.Statements())
	}
	if g := guardArg(t, execs[0].Args); g != int64(7) {
		t.Fatalf("first UPDATE guard = %v, want 7", g)
	}
	if g := guardArg(t, execs[1].Args); g != int64(8) {
		t.Fatalf("second UPDATE guard = %v, want 8 (the bumped version)", g)
	}
	if a.Version != 9 {
		t.Fatalf("after second flush a.Version = %d, want 9", a.Version)
	}
}

// The managed guard is the version the entity was loaded with, not whatever
// the struct holds: code that still bumps Version by hand (the only option
// while uow ignored the tag) must not turn every flush into a conflict.
func TestManagedVersionGuardUsesSnapshotNotStruct(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7)}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	if found, err := scope.Get(ctx(), newAccounts(), &a, int64(1)); err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	a.Balance = 150
	a.Version++ // legacy hand bump
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if g := guardArg(t, fake.Execs()[0].Args); g != int64(7) {
		t.Fatalf("UPDATE guard = %v, want the loaded version 7", g)
	}
	if a.Version != 8 {
		t.Fatalf("a.Version = %d, want 8 (database value after the bump)", a.Version)
	}
}

// A failed flush leaves the struct's version untouched, so a retry guards on
// the same version the database still holds.
func TestManagedVersionConflictLeavesVersionUnchanged(t *testing.T) {
	zero := int64(0)
	fake := &testutil.Fake{QueryFunc: accountResult(1, 100, 7), RowsAffected: &zero}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var a accountRow
	if found, err := scope.Get(ctx(), newAccounts(), &a, int64(1)); err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	a.Balance = 150
	if err := scope.Flush(ctx()); !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Flush() error = %v, want ErrVersionConflict", err)
	}
	if a.Version != 7 {
		t.Fatalf("a.Version = %d after a failed flush, want 7 (unchanged)", a.Version)
	}
}

// A detached Update of a row passed by pointer gets its version bumped too, so
// updating the same struct again guards on the new version.
func TestDetachedUpdateVersionWrittenBack(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	a := &accountRow{ID: 1, Balance: 150, Version: 7}
	if err := scope.Update(newAccounts(), a); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if a.Version != 8 {
		t.Fatalf("a.Version = %d, want 8", a.Version)
	}
}
