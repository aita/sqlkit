package sql

import (
	"strings"
	"testing"
)

func TestOuterAndCrossJoins(t *testing.T) {
	base := Select(Col("u.id")).From(SourceAlias(Table{Name: "users"}, "u"))

	assertSQL(t, base.RightJoin(SourceAlias(Table{Name: "orgs"}, "o"), Eq(Col("u.org_id"), Col("o.id"))),
		`SELECT "u"."id" FROM "users" AS "u" RIGHT JOIN "orgs" AS "o" ON ("u"."org_id" = "o"."id")`, nil)

	assertSQL(t, base.FullJoin(SourceAlias(Table{Name: "orgs"}, "o"), Eq(Col("u.org_id"), Col("o.id"))),
		`SELECT "u"."id" FROM "users" AS "u" FULL JOIN "orgs" AS "o" ON ("u"."org_id" = "o"."id")`, nil)

	assertSQL(t, base.CrossJoin(SourceAlias(Table{Name: "orgs"}, "o")),
		`SELECT "u"."id" FROM "users" AS "u" CROSS JOIN "orgs" AS "o"`, nil)
}

func TestSelectDistinct(t *testing.T) {
	query := Select(Col("email")).Distinct().From(Table{Name: "users"})

	assertSQL(t, query, `SELECT DISTINCT "email" FROM "users"`, nil)
}

func TestForUpdateAndShare(t *testing.T) {
	base := Select(Col("id")).From(Table{Name: "jobs"})

	assertSQL(t, base.ForUpdate(), `SELECT "id" FROM "jobs" FOR UPDATE`, nil)
	assertSQL(t, base.ForShare(), `SELECT "id" FROM "jobs" FOR SHARE`, nil)
	assertSQL(t, base.ForNoKeyUpdate(), `SELECT "id" FROM "jobs" FOR NO KEY UPDATE`, nil)
	assertSQL(t, base.ForKeyShare(), `SELECT "id" FROM "jobs" FOR KEY SHARE`, nil)
}

func TestLockWaitModifiers(t *testing.T) {
	base := Select(Col("id")).From(Table{Name: "jobs"})

	assertSQL(t, base.ForUpdate(SkipLocked()),
		`SELECT "id" FROM "jobs" FOR UPDATE SKIP LOCKED`, nil)
	assertSQL(t, base.ForUpdate(NoWait()),
		`SELECT "id" FROM "jobs" FOR UPDATE NOWAIT`, nil)
}

func TestLockOf(t *testing.T) {
	q := Select(Col("jobs.id")).From(Table{Name: "jobs"}).
		Join(Table{Name: "queues"}, Eq(Col("jobs.queue_id"), Col("queues.id"))).
		ForUpdate(LockOf("jobs"), SkipLocked())
	assertSQL(t, q,
		`SELECT "jobs"."id" FROM "jobs" JOIN "queues" ON ("jobs"."queue_id" = "queues"."id") FOR UPDATE OF "jobs" SKIP LOCKED`,
		nil)
}

func TestLockComesAfterLimitOffset(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "jobs"}).
		OrderBy(Asc(Col("id"))).Limit(10).Offset(5).ForUpdate(SkipLocked())
	assertSQL(t, q,
		`SELECT "id" FROM "jobs" ORDER BY "id" LIMIT 10 OFFSET 5 FOR UPDATE SKIP LOCKED`, nil)
}

func TestLockMySQL(t *testing.T) {
	assertMySQL(t, Select(Col("id")).From(Table{Name: "jobs"}).ForUpdate(SkipLocked()),
		"SELECT `id` FROM `jobs` FOR UPDATE SKIP LOCKED", nil)
	assertMySQL(t, Select(Col("id")).From(Table{Name: "jobs"}).ForShare(),
		"SELECT `id` FROM `jobs` FOR SHARE", nil)

	// MySQL has no NO KEY UPDATE / KEY SHARE.
	if _, _, err := Select(Col("id")).From(Table{Name: "jobs"}).ForKeyShare().ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "FOR UPDATE and FOR SHARE") {
		t.Fatalf("ToSQL(mysql) error = %v, want strength rejection", err)
	}
}

func TestLockRejectedOnStandard(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "jobs"}).ForUpdate()
	d := StandardSQLDialect{}
	if _, _, err := q.ToSQL(d); err == nil || !strings.Contains(err.Error(), "row-locking") {
		t.Fatalf("ToSQL(%s) error = %v, want row-locking rejection", d.Name(), err)
	}
}

func TestSelectQueryToSQL(t *testing.T) {
	query := Select(Col("public.users.id"), Col("public.users.email")).
		From(users).
		Where(Eq(Col("id"), 42))

	assertSQL(t, query,
		`SELECT "public"."users"."id", "public"."users"."email" FROM "public"."users" WHERE ("id" = $1)`,
		[]any{42})
}

func TestSelectQueryAcceptsAggregateSelection(t *testing.T) {
	query := Select(Count(Col("users.id"))).From(orgs)
	assertSQL(t, query, `SELECT COUNT("users"."id") FROM "orgs"`, nil)
}

func TestSelectQueryAcceptsJoin(t *testing.T) {
	query := Select(Col("users.email"), Col("orgs.name")).
		From(Table{Name: "users"}).
		Join(orgs, Eq(Col("users.org_id"), Col("orgs.id")))

	assertSQL(t, query,
		`SELECT "users"."email", "orgs"."name" FROM "users" JOIN "orgs" ON ("users"."org_id" = "orgs"."id")`,
		nil)
}

func TestNameSourceHelper(t *testing.T) {
	query := Select(Col("t.id")).From(Tbl("t")).Join(Tbl("u"), Eq(Col("t.id"), Col("u.id")))
	assertSQL(t, query,
		`SELECT "t"."id" FROM "t" JOIN "u" ON ("t"."id" = "u"."id")`,
		nil)
}

func TestSelectQueryAcceptsSubqueryFrom(t *testing.T) {
	subquery := Select(Col("users.id")).From(Table{Name: "users"}).Where(Eq(Col("users.id"), Int(7)))
	query := Select(Col("active_users.id")).From(Alias(subquery, "active_users"))

	assertSQL(t, query,
		`SELECT "active_users"."id" FROM (SELECT "users"."id" FROM "users" WHERE ("users"."id" = $1)) AS "active_users"`,
		[]any{7})
}

func TestSelectQueryAcceptsSubqueryDirectly(t *testing.T) {
	subquery := Select(Col("users.id")).From(Table{Name: "users"})
	query := Select(CountAll()).From(subquery)

	assertSQL(t, query, `SELECT COUNT(*) FROM (SELECT "users"."id" FROM "users")`, nil)
}

func TestSubqueryPlaceholdersShareNumbering(t *testing.T) {
	subquery := Select(Col("users.id")).From(Table{Name: "users"}).Where(Eq(Col("users.org_id"), Int(7)))
	query := Select(CountAll()).
		From(Alias(subquery, "u")).
		Where(Eq(Col("u.id"), Int(42)))

	assertSQL(t, query,
		`SELECT COUNT(*) FROM (SELECT "users"."id" FROM "users" WHERE ("users"."org_id" = $1)) AS "u" WHERE ("u"."id" = $2)`,
		[]any{7, 42})
}

func TestSelectQueryClauses(t *testing.T) {
	query := Select(Col("users.org_id")).
		From(Table{Name: "users"}).
		Where(IsNotNull(Col("users.email"))).
		GroupBy(Col("users.org_id")).
		Having(Gt(Raw("COUNT(*)"), Int(10))).
		OrderBy(Desc(Col("users.org_id"))).
		Limit(5).
		Offset(20)

	assertSQL(t, query,
		`SELECT "users"."org_id" FROM "users" WHERE ("users"."email" IS NOT NULL) GROUP BY "users"."org_id" HAVING (COUNT(*) > $1) ORDER BY "users"."org_id" DESC LIMIT 5 OFFSET 20`,
		[]any{10})
}

func TestLogicalOperators(t *testing.T) {
	query := Select(Col("id")).
		From(Table{Name: "users"}).
		Where(And(
			Or(Eq(Col("a"), Int(1)), Eq(Col("b"), Int(2))),
			Not(In(Col("c"), Int(3), Int(4))),
			IsNull(Col("d")),
		))

	assertSQL(t, query,
		`SELECT "id" FROM "users" WHERE ((("a" = $1) OR ("b" = $2)) AND (NOT ("c" IN ($3, $4))) AND ("d" IS NULL))`,
		[]any{1, 2, 3, 4})
}

func TestRepeatedWhereCombinesWithAnd(t *testing.T) {
	query := Select(Col("id")).
		From(Table{Name: "users"}).
		Where(Eq(Col("a"), Int(1))).
		Where(Eq(Col("b"), Int(2)))

	assertSQL(t, query,
		`SELECT "id" FROM "users" WHERE (("a" = $1) AND ("b" = $2))`,
		[]any{1, 2})
}

func TestSourcelessSelect(t *testing.T) {
	assertSQL(t, Select(Raw("now()")), `SELECT now()`, nil)
	assertSQL(t, Select(Raw("1"), Raw("2")), `SELECT 1, 2`, nil)
}

func TestSelectQualifiedStar(t *testing.T) {
	query := Select(StarOf("users"), Col("orgs.name")).
		From(users).
		Join(orgs, Eq(Col("users.org_id"), Col("orgs.id")))

	assertSQL(t, query,
		`SELECT "users".*, "orgs"."name" FROM "public"."users" JOIN "orgs" ON ("users"."org_id" = "orgs"."id")`,
		nil)
}

func TestSelectUnqualifiedStar(t *testing.T) {
	query := Select(Star()).From(users)

	assertSQL(t, query, `SELECT * FROM "public"."users"`, nil)
}

func TestSelectStarWithoutSourceFails(t *testing.T) {
	if _, _, err := Select().ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("SELECT * without FROM compiled, want error")
	}
}

func TestJoinWithoutSourceFails(t *testing.T) {
	query := Select(Col("id")).Join(Table{Name: "users"}, Eq(Col("a"), Col("b")))
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("JOIN without FROM compiled, want error")
	}
}

func TestJoinUsing(t *testing.T) {
	assertSQL(t,
		Select(Col("id")).From(Table{Name: "users"}).
			JoinUsing(Table{Name: "accounts"}, "user_id", "tenant_id"),
		`SELECT "id" FROM "users" JOIN "accounts" USING ("user_id", "tenant_id")`, nil)

	assertSQL(t,
		Select(Col("id")).From(Table{Name: "users"}).
			LeftJoinUsing(Table{Name: "accounts"}, "user_id"),
		`SELECT "id" FROM "users" LEFT JOIN "accounts" USING ("user_id")`, nil)
}

func TestNaturalJoin(t *testing.T) {
	assertSQL(t,
		Select(Col("id")).From(Table{Name: "users"}).NaturalJoin(Table{Name: "accounts"}),
		`SELECT "id" FROM "users" NATURAL JOIN "accounts"`, nil)

	assertSQL(t,
		Select(Col("id")).From(Table{Name: "users"}).NaturalLeftJoin(Table{Name: "accounts"}),
		`SELECT "id" FROM "users" NATURAL LEFT JOIN "accounts"`, nil)
}

func TestJoinRejectsConflictingConditions(t *testing.T) {
	c := NewCompiler(PostgreSQLDialect{})

	// A NATURAL join carrying USING is contradictory.
	natural := JoinClause{Kind: "JOIN", Source: Table{Name: "b"}, Natural: true, Using: []string{"id"}}
	if _, _, err := c.CompileJoin(natural); err == nil || !strings.Contains(err.Error(), "NATURAL") {
		t.Fatalf("CompileJoin(NATURAL+USING) error = %v, want NATURAL rejection", err)
	}

	// ON and USING are mutually exclusive.
	both := JoinClause{Kind: "JOIN", Source: Table{Name: "b"}, On: Eq(Col("a.id"), Col("b.id")), Using: []string{"id"}}
	if _, _, err := c.CompileJoin(both); err == nil || !strings.Contains(err.Error(), "ON and USING") {
		t.Fatalf("CompileJoin(ON+USING) error = %v, want ON/USING rejection", err)
	}
}

func TestDistinctOn(t *testing.T) {
	assertSQL(t,
		Select(Col("user_id"), Col("amount")).From(Table{Name: "orders"}).
			DistinctOn(Col("user_id")).
			OrderBy(Asc(Col("user_id")), Desc(Col("created_at"))),
		`SELECT DISTINCT ON ("user_id") "user_id", "amount" FROM "orders" ORDER BY "user_id", "created_at" DESC`,
		nil)
}

func TestDistinctOnRejectedOnMySQL(t *testing.T) {
	q := Select(Col("user_id")).From(Table{Name: "orders"}).DistinctOn(Col("user_id"))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "DISTINCT ON") {
		t.Fatalf("ToSQL(mysql) error = %v, want DISTINCT ON rejection", err)
	}
}

func TestSelectStmtFields(t *testing.T) {
	users := Table{Name: "users"}
	orders := Table{Name: "orders"}

	// Build with the fluent builder, then read the pure AST directly — the
	// statement's exported fields are the public, zero-copy introspection seam.
	c := Select(Col("id")).
		From(users).
		Join(orders, Eq(Col("users.id"), Col("orders.user_id"))).
		Where(Eq(Col("id"), Val(1))).
		Limit(10).
		Stmt()

	if c.From == nil || c.From.SQLSource().Name != "users" {
		t.Errorf("From = %v, want users", c.From)
	}
	if len(c.Joins) != 1 {
		t.Fatalf("Joins = %d, want 1", len(c.Joins))
	}
	if c.Where == nil {
		t.Error("Where = nil, want non-nil")
	}
	if !c.HasLimit || c.Limit != 10 {
		t.Errorf("Limit = %d (has=%v), want 10", c.Limit, c.HasLimit)
	}
	if c.HasOffset {
		t.Errorf("HasOffset = true, want false")
	}
}
