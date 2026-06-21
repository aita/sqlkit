package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
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
	fake := &testutil.Fake{}
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

// TestSessionMergeBindsToTransaction covers Session.Merge: a MERGE bound to the
// session runs through its transaction.
func TestSessionMergeBindsToTransaction(t *testing.T) {
	fake := &testutil.Fake{}
	session := newSession(t, fake)

	src := sqlkit.Select(Users.ID, Users.Name).From(Users).As("src")
	_, err := session.Merge(Users).
		Using(src).
		On(sqlkit.Cond(sql.Eq(Users.ID, src.Col("id")))).
		When(sqlkit.WhenMatched().ThenDelete()).
		Exec(context.Background())
	if err != nil {
		t.Fatalf("Session.Merge Exec error = %v", err)
	}
	if fake.Begins() != 1 {
		t.Fatalf("begins = %d, want 1 (session transaction)", fake.Begins())
	}
}

// TestMergeAsWithCTEReturning covers As (target alias), WithCTE (leading WITH),
// WhenNotMatchedBySource, and Returning, none of which the basic MERGE tests
// reach.
func TestMergeAsWithCTEReturning(t *testing.T) {
	cte := sqlkit.Select(Users.ID).From(Users).CTE("active")
	src := sqlkit.Select(Users.ID, Users.Name).From(Users).As("src")
	q := sqlkit.Merge(Users).As("t").
		Using(src).
		On(sqlkit.Cond(sql.Eq(Users.ID, src.Col("id")))).
		When(sqlkit.WhenNotMatchedBySource().ThenDelete()).
		WithCTE(cte).
		Returning(Users.ID)

	assertToSQL(t, q,
		`WITH "active" AS (SELECT "public"."users"."id" FROM "public"."users") MERGE INTO "public"."users" AS "t" USING (SELECT "public"."users"."id", "public"."users"."name" FROM "public"."users") AS "src" ON ("public"."users"."id" = "src"."id") WHEN NOT MATCHED BY SOURCE THEN DELETE RETURNING "public"."users"."id"`,
		nil)
}
