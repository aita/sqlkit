package sql

import "testing"

func TestSetOpPrecedenceMixed(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})
	cq := Select(Col("id")).From(Table{Name: NewIdent("c")})

	// (a UNION b) INTERSECT c must keep the UNION grouped, since INTERSECT binds
	// tighter; without parens PostgreSQL would read a UNION (b INTERSECT c).
	assertSQL(t, a.Union(b).Intersect(cq),
		`((SELECT "id" FROM "a") UNION (SELECT "id" FROM "b")) INTERSECT (SELECT "id" FROM "c")`, nil)

	// Same-operator chains stay unparenthesized.
	assertSQL(t, a.Union(b).Union(cq),
		`(SELECT "id" FROM "a") UNION (SELECT "id" FROM "b") UNION (SELECT "id" FROM "c")`, nil)
}

func TestUnion(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(Eq(Col("active"), Val(true)))
	b := Select(Col("id")).From(Table{Name: NewIdent("admins")})

	query := a.Union(b).OrderBy(Asc(Col("id"))).Limit(10).Offset(5)

	assertSQL(t, query,
		`(SELECT "id" FROM "users" WHERE ("active" = $1)) UNION (SELECT "id" FROM "admins") ORDER BY "id" LIMIT 10 OFFSET 5`,
		[]any{true})
}

// TestUnionMySQLOffsetWithoutLimit covers a set operation with OFFSET but no
// LIMIT on MySQL: MySQL rejects a bare OFFSET, so the compiler must emit the
// "all rows" sentinel LIMIT first, matching the SELECT compiler.
func TestUnionMySQLOffsetWithoutLimit(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})

	assertMySQL(t, a.Union(b).Offset(10),
		"(SELECT `id` FROM `a`) UNION (SELECT `id` FROM `b`) LIMIT 18446744073709551615 OFFSET 10",
		nil)
}

func TestUnionAllChained(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})
	c := Select(Col("id")).From(Table{Name: NewIdent("c")})

	query := a.UnionAll(b).UnionAll(c)

	assertSQL(t, query,
		`(SELECT "id" FROM "a") UNION ALL (SELECT "id" FROM "b") UNION ALL (SELECT "id" FROM "c")`,
		nil)
}

func TestIntersectAndExcept(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})

	assertSQL(t, a.Intersect(b), `(SELECT "id" FROM "a") INTERSECT (SELECT "id" FROM "b")`, nil)
	assertSQL(t, a.Except(b), `(SELECT "id" FROM "a") EXCEPT (SELECT "id" FROM "b")`, nil)
}

func TestIntersectAndExceptAll(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})

	assertSQL(t, a.IntersectAll(b), `(SELECT "id" FROM "a") INTERSECT ALL (SELECT "id" FROM "b")`, nil)
	assertSQL(t, a.ExceptAll(b), `(SELECT "id" FROM "a") EXCEPT ALL (SELECT "id" FROM "b")`, nil)
}

func TestIntersectAndExceptChained(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})
	c := Select(Col("id")).From(Table{Name: NewIdent("c")})

	assertSQL(t, a.Intersect(b).Intersect(c),
		`(SELECT "id" FROM "a") INTERSECT (SELECT "id" FROM "b") INTERSECT (SELECT "id" FROM "c")`, nil)
	assertSQL(t, a.Except(b).Except(c),
		`(SELECT "id" FROM "a") EXCEPT (SELECT "id" FROM "b") EXCEPT (SELECT "id" FROM "c")`, nil)
}

func TestUnionAsSubquery(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})

	query := Select(CountAll()).From(Alias(a.Union(b), "u"))

	assertSQL(t, query,
		`SELECT COUNT(*) FROM ((SELECT "id" FROM "a") UNION (SELECT "id" FROM "b")) AS "u"`,
		nil)
}

func TestUnionPlaceholderNumbering(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: NewIdent("a")}).Where(Eq(Col("x"), Int(1)))
	b := Select(Col("id")).From(Table{Name: NewIdent("b")}).Where(Eq(Col("y"), Int(2)))

	assertSQL(t, a.Union(b),
		`(SELECT "id" FROM "a" WHERE ("x" = $1)) UNION (SELECT "id" FROM "b" WHERE ("y" = $2))`,
		[]any{1, 2})
}

// TestCompoundCoverage covers the set-operation variants, the compound used as a
// derived source, the builder plumbing, and the operand/order error branches.
func TestCompoundCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	a := Select(Col("id")).From(Table{Name: NewIdent("a")})
	b := Select(Col("id")).From(Table{Name: NewIdent("b")})
	mustOK := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mustOK(a.UnionAll(b))
	mustOK(a.IntersectAll(b))
	mustOK(a.ExceptAll(b))
	mustOK(a.Union(b).IntersectAll(b))
	mustOK(a.Union(b).ExceptAll(b))
	mustOK(Select(Col("id")).From(Alias(a.Union(b), "u")))
	mustOK(a.Union(rawTestQuery{}))                           // operand compiled via the ToSQL fallback
	mustOK(a.Union(b).OrderBy(Asc(Col("id")), Asc(Col("v")))) // multi-term compound ORDER BY

	c := a.Union(b)
	_ = c.Stmt()
	_ = c.SQLSource()
	_ = c.Stmt().SQLSource()
	if sp, ok := Query(c).(interface{ setPos(int) Query }); ok {
		sp.setPos(2)
	}
	if sp, ok := Query(c.Stmt()).(interface{ setPos(int) Query }); ok {
		sp.setPos(2)
	}

	bad := Select(UnaryExpression{Operand: Col("x")}).From(orgs)
	if _, _, err := a.Union(bad).ToSQL(pg); err == nil {
		t.Fatal("bad right operand should error")
	}
	if _, _, err := bad.Union(a).ToSQL(pg); err == nil {
		t.Fatal("bad left operand should error")
	}
	if _, _, err := a.Union(b).OrderBy(Asc(UnaryExpression{Operand: Col("x")})).ToSQL(pg); err == nil {
		t.Fatal("bad compound order expr should error")
	}
	// NULLS ordering on a dialect that does not support it errors while rendering
	// the compound's ORDER BY modifiers.
	if _, _, err := a.Union(b).OrderBy(OrderTerm{Expr: Col("id"), Nulls: "FIRST"}).ToSQL(MySQLDialect{}); err == nil {
		t.Fatal("compound NULLS ordering on MySQL should error")
	}
}
