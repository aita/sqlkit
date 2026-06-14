package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

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

func TestSimilarTo(t *testing.T) {
	assertSQL(t,
		Select(Col("id")).From(Table{Name: "t"}).Where(SimilarTo(Col("name"), Val("%(foo|bar)%"))),
		`SELECT "id" FROM "t" WHERE ("name" SIMILAR TO $1)`, []any{"%(foo|bar)%"})

	assertSQL(t,
		Select(Col("id")).From(Table{Name: "t"}).Where(NotSimilarTo(Col("name"), Val("a%"))),
		`SELECT "id" FROM "t" WHERE ("name" NOT SIMILAR TO $1)`, []any{"a%"})

	assertStandard(t,
		Select(Col("id")).From(Table{Name: "t"}).Where(SimilarTo(Col("name"), Val("a%"))),
		`SELECT "id" FROM "t" WHERE ("name" SIMILAR TO ?)`, []any{"a%"})
}

func TestSimilarToRejectedOnMySQL(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "t"}).Where(SimilarTo(Col("name"), Val("a%")))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "SIMILAR TO") {
		t.Fatalf("ToSQL(mysql) error = %v, want SIMILAR TO rejection", err)
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
