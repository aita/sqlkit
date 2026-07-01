package uow_test

// Version/touch column handling in the snapshot-diff (diffAssignments, managed
// entities) and detached (updateAssignments, Update()) UPDATE paths: both must
// treat a `sqlkit:",version"` field as a bump-and-guard rather than a plain
// diffed column, and a `sqlkit:",touch"` field as an unconditional
// CURRENT_TIMESTAMP() rather than a copy of the struct's current value — the
// same special-casing update.go's SetStruct already applies to the direct
// builder path (see structAssignments). Split out from scope_test.go per the
// version/touch-tag concern (see TestManagedDirtyUpdatesOnlyChangedColumns and
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

func TestManagedTouchOnlyStillUpdates(t *testing.T) {
	// Nothing but the touch column "changes" (it never diffs); the UPDATE must
	// still fire rather than being dropped as a no-op.
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
	if got := len(fake.Execs()); got != 1 {
		t.Fatalf("got %d execs, want 1 UPDATE (version bump always fires): %v", got, fake.Statements())
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
