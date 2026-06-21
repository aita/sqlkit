package sql

import "testing"

func TestMergeStmt(t *testing.T) {
	p := MergeInto(Table{Name: NewIdent("accounts")}).As("t").
		Using(SourceAlias(Table{Name: NewIdent("staging")}, "s")).
		On(Eq(Col("t.id"), Col("s.id"))).
		When(WhenMatched().ThenUpdate(Set("balance", Col("s.balance")))).
		When(WhenNotMatched().ThenInsert([]string{"id"}, Col("s.id"))).
		Stmt()

	if p.Target.Name.String() != "accounts" || p.TargetAlias.String() != "t" {
		t.Errorf("Target/alias = %q/%q, want accounts/t", p.Target.Name, p.TargetAlias)
	}
	if p.Using == nil || p.On == nil {
		t.Fatal("Using/On missing from MergeStmt")
	}
	if len(p.When) != 2 {
		t.Fatalf("When = %d clauses, want 2", len(p.When))
	}
	if p.When[0].Match != "MATCHED" || p.When[0].Action != "UPDATE" || len(p.When[0].Sets) != 1 {
		t.Errorf("When[0] = %+v, want MATCHED UPDATE with one assignment", p.When[0])
	}
	if p.When[1].Match != "NOT MATCHED" || p.When[1].Action != "INSERT" ||
		len(p.When[1].InsertColumns) != 1 || len(p.When[1].InsertValues) != 1 {
		t.Errorf("When[1] = %+v, want NOT MATCHED INSERT with one column/value", p.When[1])
	}
}

// Walk descends into a MERGE's USING subquery.
func TestMergeWalk(t *testing.T) {
	using := Select(Col("id"), Col("balance")).From(Table{Name: NewIdent("staging")})
	q := MergeInto(Table{Name: NewIdent("accounts")}).
		Using(Alias(using, "s")).
		On(Eq(Col("accounts.id"), Col("s.id"))).
		When(WhenMatched().ThenDelete())

	visited := 0
	Inspect(Unwrap(q).(Node), func(n Node) bool {
		if _, ok := n.(Query); ok {
			visited++
		}
		return true
	})
	if visited != 2 {
		t.Errorf("Walk over MERGE visited %d queries, want 2 (merge + USING subquery)", visited)
	}
}

func TestMergeCompile(t *testing.T) {
	q := MergeInto(Table{Name: NewIdent("accounts")}).As("t").
		Using(SourceAlias(Table{Name: NewIdent("staging")}, "s")).
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
	q := MergeInto(Table{Name: NewIdent("t")}).
		Using(SourceAlias(Table{Name: NewIdent("src")}, "s")).
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
	q := MergeInto(Table{Name: NewIdent("t")}).
		Using(Table{Name: NewIdent("s")}).
		On(Eq(Col("t.id"), Col("s.id"))).
		When(WhenMatched().ThenDelete())
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil {
		t.Fatal("MERGE on MySQL succeeded, want unsupported error")
	}
}

func TestMergeValidation(t *testing.T) {
	base := MergeInto(Table{Name: NewIdent("t")}).Using(Table{Name: NewIdent("s")}).On(Eq(Col("t.id"), Col("s.id")))
	if _, _, err := base.When(WhenNotMatched().ThenUpdate(Set("x", 1))).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("WHEN NOT MATCHED ... UPDATE succeeded, want error")
	}
	if _, _, err := base.When(WhenMatched().ThenInsert([]string{"x"}, 1)).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("WHEN MATCHED ... INSERT succeeded, want error")
	}
	if _, _, err := MergeInto(Table{Name: NewIdent("t")}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("MERGE without USING/ON/WHEN succeeded, want error")
	}
}

// TestMergeCoverage drives the MERGE compile branches (every WHEN action) plus
// the builder plumbing (WithCTE, Returning, compile, setPos).
func TestMergeCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	tgt := Table{Name: NewIdent("t")}
	src := SourceAlias(Table{Name: NewIdent("s")}, "s")
	cte := Select(Col("id")).From(orgs).CTE("c")
	m := MergeInto(tgt).As("t").Using(src).On(Eq(Col("t.id"), Col("s.id"))).
		WithCTE(cte).
		When(
			WhenMatched().And(Gt(Col("s.v"), 0)).ThenUpdate(Set("v", Col("s.v"))),
			WhenMatched().ThenDelete(),
			WhenMatched().ThenDoNothing(),
			WhenNotMatched().ThenInsert([]string{"id", "v"}, Col("s.id"), 1),
			WhenNotMatched().ThenDoNothing(),
			WhenNotMatchedBySource().ThenDelete(),
		).
		Returning(Col("t.id"))
	if _, _, err := m.ToSQL(pg); err != nil {
		t.Fatalf("merge ToSQL: %v", err)
	}
	_ = m.Stmt()
	if _, _, err := m.compile(NewCompiler(pg)); err != nil {
		t.Fatalf("merge compile: %v", err)
	}
	if sp, ok := Query(m).(interface{ setPos(int) Query }); ok {
		sp.setPos(2)
	}
}
