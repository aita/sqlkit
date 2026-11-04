package uow_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

type nullableUserRow struct {
	ID    int64
	Email string
	Name  *string
}

// A nil pointer field binds as NULL. In a batched VALUES-join UPDATE an
// all-NULL column has no type the engine can infer (PostgreSQL: "column is of
// type bigint but expression is of type text"), so such rows must take the
// per-row UPDATE path — as an untyped nil already did — rather than the bulk one.
func TestBulkUpdateTypedNilFallsBackToPerRow(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true)))
	if err := scope.Update(newUsers(),
		nullableUserRow{ID: 1, Email: "a@x"},
		nullableUserRow{ID: 2, Email: "b@x"},
	); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 2 {
		t.Fatalf("updates = %d, want 2 per-row updates: %v", len(execs), fake.Statements())
	}
	for _, e := range execs {
		if strings.Contains(e.SQL, "VALUES") {
			t.Fatalf("typed-nil row went through the VALUES join: %q", e.SQL)
		}
	}
}
