package sql

import "testing"

func TestMergeCompile(t *testing.T) {
	q := MergeInto(Table{Name: "accounts"}).As("t").
		Using(SourceAlias(Table{Name: "staging"}, "s")).
		On(Eq(Col("t.id"), Col("s.id"))).
		When(WhenMatched().ThenUpdate(Set("balance", Col("s.balance")))).
		When(WhenNotMatched().ThenInsert([]string{"id", "balance"}, Col("s.id"), Col("s.balance")))

	got, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `MERGE INTO "accounts" AS "t" USING "staging" AS "s" ON ("t"."id" = "s"."id") ` +
		`WHEN MATCHED THEN UPDATE SET "balance" = "s"."balance" ` +
		`WHEN NOT MATCHED THEN INSERT ("id", "balance") VALUES ("s"."id", "s"."balance")`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
}

func TestMergeConditionAndDeleteDoNothing(t *testing.T) {
	q := MergeInto(Table{Name: "t"}).
		Using(SourceAlias(Table{Name: "src"}, "s")).
		On(Eq(Col("t.k"), Col("s.k"))).
		When(WhenMatched().And(Gt(Col("s.n"), 0)).ThenDelete()).
		When(WhenMatched().ThenDoNothing()).
		When(WhenNotMatchedBySource().ThenDelete())

	got, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `MERGE INTO "t" USING "src" AS "s" ON ("t"."k" = "s"."k") ` +
		`WHEN MATCHED AND ("s"."n" > $1) THEN DELETE ` +
		`WHEN MATCHED THEN DO NOTHING ` +
		`WHEN NOT MATCHED BY SOURCE THEN DELETE`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 1 || args[0] != 0 {
		t.Fatalf("args = %#v, want [0]", args)
	}
}

func TestMergeUnsupportedDialect(t *testing.T) {
	q := MergeInto(Table{Name: "t"}).
		Using(Table{Name: "s"}).
		On(Eq(Col("t.id"), Col("s.id"))).
		When(WhenMatched().ThenDelete())
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil {
		t.Fatal("MERGE on MySQL succeeded, want unsupported error")
	}
	if _, _, err := q.ToSQL(SQLiteDialect{}); err == nil {
		t.Fatal("MERGE on SQLite succeeded, want unsupported error")
	}
}

func TestMergeValidation(t *testing.T) {
	base := MergeInto(Table{Name: "t"}).Using(Table{Name: "s"}).On(Eq(Col("t.id"), Col("s.id")))
	if _, _, err := base.When(WhenNotMatched().ThenUpdate(Set("x", 1))).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("WHEN NOT MATCHED ... UPDATE succeeded, want error")
	}
	if _, _, err := base.When(WhenMatched().ThenInsert([]string{"x"}, 1)).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("WHEN MATCHED ... INSERT succeeded, want error")
	}
	if _, _, err := MergeInto(Table{Name: "t"}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("MERGE without USING/ON/WHEN succeeded, want error")
	}
}
