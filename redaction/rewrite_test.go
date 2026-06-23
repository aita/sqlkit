package redaction_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// secret is a recognizable sensitive literal that must never survive masking.
const secret = "PIISECRETXYZ"

// hidesSecret renders q for both dialects and fails if the secret literal leaks
// into the rendered SQL or the bind arguments.
func hidesSecret(t *testing.T, q sql.Query) {
	t.Helper()
	m := redaction.New(redaction.Columns("users.name"))
	masked := m.Apply(q)
	for _, d := range []sql.Dialect{sql.PostgreSQLDialect{}, sql.MySQLDialect{}} {
		s, a, err := masked.ToSQL(d)
		if err != nil {
			// Some constructs are PostgreSQL-only; a compile error for the other
			// dialect is fine, a leak is not.
			continue
		}
		if strings.Contains(s, secret) {
			t.Errorf("%T: secret leaked into SQL: %s", d, s)
		}
		for _, v := range a {
			if v == secret {
				t.Errorf("%T: secret leaked into args: %v (sql=%s)", d, a, s)
			}
		}
	}
}

// users is the table whose "name" column the policy marks sensitive.
var users = sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}

// sensitivePred is the predicate "name = secret"; it must be masked wherever it
// is buried in a statement.
func sensitivePred() sql.Expression {
	return sql.Eq(sql.Col("name"), sql.Val(secret))
}

// secretCase wraps the sensitive predicate in a CASE so it can sit anywhere a
// value-typed operand is expected without the predicate becoming the operand
// itself. It returns the concrete CaseExpression (which is both an Expression
// and a Selection) so it can also serve as a COUNT target.
func secretCase() sql.CaseExpression {
	return sql.Case().When(sensitivePred(), sql.Val(1)).Else(sql.Val(0))
}

// TestRewriteExprMasksNestedPredicate covers the rewriteExpr side: a sensitive
// "name = secret" predicate buried inside a wrapper expression that earlier
// versions of the redactor did not recurse into (a fail-open leak).
func TestRewriteExprMasksNestedPredicate(t *testing.T) {
	cases := map[string]sql.Query{
		"subscript operand":   sql.Select(sql.Subscript(sql.Array(secretCase()), 1)).From(users),
		"position substring":  sql.Select(sql.Position(secretCase(), sql.Val("x"))).From(users),
		"position string":     sql.Select(sql.Position(sql.Val("x"), secretCase())).From(users),
		"extract operand":     sql.Select(sql.Extract("year", secretCase())).From(users),
		"string_agg arg":      sql.Select(sql.StringAgg(secretCase(), ",")).From(users),
		"count target":        sql.Select(sql.Count(secretCase())).From(users),
		"window fn":           sql.Select(sql.Over(sql.Count(secretCase()))).From(users),
		"window partition":    sql.Select(sql.Over(sql.Func("sum", sql.Val(1))).PartitionBy(secretCase())).From(users),
		"json_object value":   sql.Select(sql.JSONObject("k", secretCase())).From(users),
		"json_access operand": sql.Select(sql.JSONGet(secretCase(), "k")).From(users),
		"group by rollup":     sql.Select(sql.Star()).From(users).GroupBy(sql.Rollup(secretCase())),
		"group by cube":       sql.Select(sql.Star()).From(users).GroupBy(sql.Cube(secretCase())),
		"group by sets":       sql.Select(sql.Star()).From(users).GroupBy(sql.GroupingSets([]sql.Expression{secretCase()})),
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) { hidesSecret(t, q) })
	}
}

// TestMaskValueMasksNestedLiteral covers the maskValue side: the secret literal
// sits on the value side of "name = <wrapper>(secret)", so the whole wrapper is
// sensitive and every string literal inside it must be masked.
func TestMaskValueMasksNestedLiteral(t *testing.T) {
	where := func(rhs sql.Expression) sql.Query {
		return sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), rhs))
	}
	cases := map[string]sql.Query{
		"subscript":   where(sql.Subscript(sql.Array(secret), 1)),
		"position":    where(sql.Position(secret, secret)),
		"extract":     where(sql.Extract("year", secret)),
		"string_agg":  where(sql.StringAgg(secret, ",")),
		"count":       where(sql.Count(sql.Case().When(sql.Eq(sql.Col("id"), sql.Val(1)), sql.Val("x")).Else(sql.Val(secret)))),
		"window":      where(sql.Over(sql.Func("max", sql.Val(secret)))),
		"json_object": where(sql.JSONObject("k", secret)),
		"json_access": where(sql.JSONGet(sql.Val(secret), "k")),
		"field":       where(sql.Field(sql.Array(secret), "x")),
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) { hidesSecret(t, q) })
	}
}
