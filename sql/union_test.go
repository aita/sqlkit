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
