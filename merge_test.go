package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

func TestMergeToSQL(t *testing.T) {
	src := sqlkit.Select(Users.ID, Users.Name).From(Users).As("src")
	q := sqlkit.Merge(Users).
		Using(src).
		On(sqlkit.Cond(sql.Eq(Users.ID, src.Col("id")))).
		When(sqlkit.WhenMatched().ThenUpdate(sql.Set("name", src.Col("name")))).
		When(sqlkit.WhenNotMatched().ThenInsert([]string{"id", "name"}, 1, "x"))

	sqlText, _, err := q.ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := "MERGE INTO"; len(sqlText) < len(want) || sqlText[:len(want)] != want {
		t.Fatalf("ToSQL() = %q, want it to start with %q", sqlText, want)
	}
}

func TestMergeExecBindsToDatabase(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	src := sqlkit.Select(Users.ID, Users.Name).From(Users).As("src")
	_, err := db.Merge(Users).
		Using(src).
		On(sqlkit.Cond(sql.Eq(Users.ID, src.Col("id")))).
		When(sqlkit.WhenMatched().ThenDelete()).
		Exec(context.Background())
	if err != nil {
		t.Fatalf("Merge Exec error = %v", err)
	}
}

func TestMergeUnboundExecErrors(t *testing.T) {
	_, err := sqlkit.Merge(Users).Exec(context.Background())
	if err == nil {
		t.Fatal("expected error executing an unbound MERGE")
	}
}
