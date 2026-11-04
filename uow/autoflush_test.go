package uow_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// emptyUsers scripts an empty users result, so a Find reads no rows.
func emptyUsers() func(string, []any) (testutil.Result, error) {
	return func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id", "email", "name"}}, nil
	}
}

func TestAutoflushFlushesStagedWorkBeforeRead(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: emptyUsers()}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithAutoflush(true))
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	var users []userRow
	if err := scope.Find(ctx(), newUsers(), &users); err != nil {
		t.Fatalf("find: %v", err)
	}
	// The staged INSERT is flushed before the SELECT that Find issues.
	insertAt, selectAt := -1, -1
	for i, s := range fake.Statements() {
		switch strings.Fields(s)[0] {
		case "INSERT":
			if insertAt < 0 {
				insertAt = i
			}
		case "SELECT":
			if selectAt < 0 {
				selectAt = i
			}
		}
	}
	if insertAt < 0 || selectAt < 0 || insertAt > selectAt {
		t.Fatalf("want INSERT before SELECT, got %v", fake.Statements())
	}
}

func TestWithoutAutoflushReadLeavesWorkBuffered(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: emptyUsers()}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB())) // autoflush off (default)
	if err := scope.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
		t.Fatal(err)
	}
	var users []userRow
	if err := scope.Find(ctx(), newUsers(), &users); err != nil {
		t.Fatalf("find: %v", err)
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs before an explicit flush, want 0: %v", got, fake.Statements())
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := verbs(fake.Execs()); len(got) != 1 || got[0] != "INSERT" {
		t.Fatalf("verbs = %v, want one INSERT at the explicit flush", got)
	}
}

func TestAutoflushIdleReadIsNoop(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "Alice")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithAutoflush(true))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(1)); err != nil {
		t.Fatal(err)
	}
	// Nothing was pending, so no flush ran — only the Get's own SELECT.
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0 on an idle autoflush read", got)
	}
	if got := len(fake.Queries()); got != 1 {
		t.Fatalf("got %d queries, want exactly the Get select", got)
	}
}

func TestAutoflushFlushesDirtyManagedBeforeRead(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: usersResult(1, "a@x", "Alice")}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithAutoflush(true))
	var u userRow
	if _, err := scope.Get(ctx(), newUsers(), &u, int64(1)); err != nil {
		t.Fatal(err)
	}
	u.Name = "Bob" // the managed row is now dirty
	var again userRow
	if _, err := scope.Get(ctx(), newUsers(), &again, int64(1)); err != nil {
		t.Fatal(err)
	}
	if got := verbs(fake.Execs()); len(got) != 1 || got[0] != "UPDATE" {
		t.Fatalf("verbs = %v, want the dirty UPDATE flushed by autoflush", got)
	}
	// Statement order: SELECT (first Get), UPDATE (autoflush), SELECT (second Get).
	stmts := fake.Statements()
	if len(stmts) != 3 || strings.Fields(stmts[1])[0] != "UPDATE" {
		t.Fatalf("statements = %v, want SELECT, UPDATE, SELECT", stmts)
	}
}
