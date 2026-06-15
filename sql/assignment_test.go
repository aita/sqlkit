package sql

import "testing"

func TestOnConflictExpressionCompile(t *testing.T) {
	q := InsertInto(Table{Name: "users"}).Columns("email").Values("a@x").
		OnConflictExpr(Func("lower", Col("email"))).
		DoUpdate(Set("seen", Col("now()")))
	got, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `INSERT INTO "users" ("email") VALUES ($1) ON CONFLICT ((lower("email"))) DO UPDATE SET "seen" = "now()"`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 1 || args[0] != "a@x" {
		t.Fatalf("args = %#v, want [a@x]", args)
	}
}

func TestUpdateSubscriptAssignmentCompile(t *testing.T) {
	q := Update(Table{Name: "t"}).Set(SetAt(Subscript(Col("tags"), 1), "x")).Where(Eq(Col("id"), 5))
	got, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `UPDATE "t" SET "tags"[$1] = $2 WHERE ("id" = $3)`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 3 || args[0] != 1 || args[1] != "x" || args[2] != 5 {
		t.Fatalf("args = %#v, want [1 x 5]", args)
	}
}

// Each conflict-target setter replaces any earlier target, so the last one wins
// (the fluent API cannot express a mixed column/expression target).
func TestOnConflictTargetLastWins(t *testing.T) {
	q := InsertInto(Table{Name: "t"}).Columns("a").Values(1).
		OnConflict("a").OnConflictExpr(Col("b")).DoNothing()
	got, _, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `INSERT INTO "t" ("a") VALUES ($1) ON CONFLICT (("b")) DO NOTHING`
	if got != want {
		t.Fatalf("ToSQL() = %q, want %q", got, want)
	}
}
