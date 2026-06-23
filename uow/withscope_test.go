package uow_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

func TestWithScopeFlushesAndCommits(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	err := uow.WithScope(ctx(), db, func(s *uow.Scope) error {
		return s.Add(newUsers(), userRow{Email: "a@x"})
	})
	if err != nil {
		t.Fatalf("with scope: %v", err)
	}
	if got := verbs(fake.Execs()); len(got) != 1 || got[0] != "INSERT" {
		t.Fatalf("verbs = %v, want one INSERT flushed by WithScope", got)
	}
	if fake.Commits() != 1 {
		t.Fatalf("commits = %d, want 1", fake.Commits())
	}
}

func TestWithScopeRollsBackOnError(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	sentinel := errors.New("boom")
	err := uow.WithScope(ctx(), db, func(s *uow.Scope) error {
		if err := s.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
			return err
		}
		return sentinel // return before WithScope's closing flush
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the sentinel", err)
	}
	if fake.Commits() != 0 {
		t.Fatalf("commits = %d, want 0 on error", fake.Commits())
	}
	if got := len(fake.Execs()); got != 0 {
		t.Fatalf("got %d execs, want 0 (staged work is not flushed on error)", got)
	}
}

func TestWithScopeSupportsNestedSavepoint(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	err := uow.WithScope(ctx(), db, func(s *uow.Scope) error {
		child := s.NewScope()
		if err := child.Add(newUsers(), userRow{Email: "child@x"}); err != nil {
			return err
		}
		return child.Flush(ctx())
	})
	if err != nil {
		t.Fatalf("with scope: %v", err)
	}
	if !strings.Contains(strings.Join(fake.Statements(), "\n"), "SAVEPOINT") {
		t.Fatalf("expected a SAVEPOINT within the session-backed scope: %v", fake.Statements())
	}
	if fake.Commits() != 1 {
		t.Fatalf("commits = %d, want 1", fake.Commits())
	}
}

func TestWithScopePassesOptions(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: emptyUsers()}
	db := sqlkit.WrapDB(fake.DB())
	err := uow.WithScope(ctx(), db, func(s *uow.Scope) error {
		if err := s.Add(newUsers(), userRow{Email: "a@x"}); err != nil {
			return err
		}
		// With autoflush the staged insert is flushed before this read.
		var users []userRow
		return s.Find(ctx(), newUsers(), &users)
	}, uow.WithAutoflush(true))
	if err != nil {
		t.Fatalf("with scope: %v", err)
	}
	insertAt, selectAt := -1, -1
	for i, st := range fake.Statements() {
		switch strings.Fields(st)[0] {
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
		t.Fatalf("want autoflush INSERT before SELECT, got %v", fake.Statements())
	}
}
