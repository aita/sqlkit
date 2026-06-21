package roundtrip_test

// Operator-precedence and grouping oracle. The round-trip tests prove the
// compiler never emits invalid SQL, but they cannot catch a parser that builds
// the *wrong* tree: the compiler fully parenthesizes its output, so a mis-grouped
// parse re-renders to stable (if wrong) SQL and round-trips cleanly. These tests
// close that gap by pinning the grouping directly — they parse an expression and
// assert the parenthesization the compiler emits, which mirrors the AST the
// parser built. A precedence or associativity bug in either frontend shows up
// here as a parenthesization mismatch.
//
// It is cross-cutting on purpose (both parser frontends and the compiler, both
// dialects), so it lives in its own file rather than a per-construct corpus.

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/aita/sqlkit/sqlparse/mysql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// renderSelectExpr parses "SELECT <expr> FROM t" and returns the rendered
// select-list expression, stripped of the surrounding clause. A parse failure or
// a raw fallback is returned verbatim so the assertion reports it.
func renderSelectExpr(p sqlparse.Parser, d sql.Dialect, expr string) string {
	q, err := sqlparse.ParseOne(p, "SELECT "+expr+" FROM t")
	if err != nil {
		return "PARSEFAIL: " + err.Error()
	}
	if _, ok := q.(sql.RawQuery); ok {
		return "RAW"
	}
	out, _, err := q.ToSQL(d)
	if err != nil {
		return "RENDERERR: " + err.Error()
	}
	out = strings.TrimPrefix(out, "SELECT ")
	if i := strings.Index(out, " FROM "); i >= 0 {
		out = out[:i]
	}
	return out
}

// renderWhereExpr parses "SELECT * FROM t WHERE <expr>" and returns the rendered
// WHERE expression. Boolean and comparison operators are exercised here because
// some frontends only model them in predicate position.
func renderWhereExpr(p sqlparse.Parser, d sql.Dialect, expr string) string {
	q, err := sqlparse.ParseOne(p, "SELECT * FROM t WHERE "+expr)
	if err != nil {
		return "PARSEFAIL: " + err.Error()
	}
	if _, ok := q.(sql.RawQuery); ok {
		return "RAW"
	}
	out, _, err := q.ToSQL(d)
	if err != nil {
		return "RENDERERR: " + err.Error()
	}
	if i := strings.Index(out, " WHERE "); i >= 0 {
		out = out[i+len(" WHERE "):]
	}
	return out
}

func TestPrecedencePostgres(t *testing.T) {
	d := sql.PostgreSQLDialect{}
	p := postgres.New()

	// Arithmetic and other value-level operators, asserted in select-list position.
	selectCases := []struct{ expr, want string }{
		{"a + b * c", `("a" + ("b" * "c"))`},
		{"a * b + c", `(("a" * "b") + "c")`},
		{"a - b - c", `(("a" - "b") - "c")`}, // left-associative
		{"a - b + c", `(("a" - "b") + "c")`}, // equal precedence, left to right
		{"a / b / c", `(("a" / "b") / "c")`}, // left-associative
		{"a + b - c * d", `(("a" + "b") - ("c" * "d"))`},
		{"a * (b + c)", `("a" * ("b" + "c"))`}, // explicit parens override
		{"(a + b) * c", `(("a" + "b") * "c")`},
		{"a || b || c", `(("a" || "b") || "c")`}, // concat is left-associative
	}
	for _, tc := range selectCases {
		if got := renderSelectExpr(p, d, tc.expr); got != tc.want {
			t.Errorf("select %q\n  got  %s\n  want %s", tc.expr, got, tc.want)
		}
	}

	// Boolean and comparison precedence, asserted in WHERE position.
	whereCases := []struct{ expr, want string }{
		{"a OR b AND c", `("a" OR ("b" AND "c"))`}, // AND binds tighter than OR
		{"a AND b OR c", `(("a" AND "b") OR "c")`},
		{"NOT a AND b", `((NOT "a") AND "b")`}, // NOT binds tighter than AND
		{"NOT a OR b", `((NOT "a") OR "b")`},
		{"NOT a = 1", `(NOT ("a" = $1))`}, // comparison binds tighter than NOT
		{"a = 1 AND b = 2", `(("a" = $1) AND ("b" = $2))`},
		{"a = 1 OR b = 2 AND c = 3", `(("a" = $1) OR (("b" = $2) AND ("c" = $3)))`},
		{"a = 1 AND b = 2 OR c = 3", `((("a" = $1) AND ("b" = $2)) OR ("c" = $3))`},
		{"a + 1 = b * 2", `(("a" + $1) = ("b" * $2))`}, // arithmetic binds tighter than comparison
		{"a = 1 AND b = 2 AND c = 3", `(("a" = $1) AND ("b" = $2) AND ("c" = $3))`},
		{"a BETWEEN 1 AND 2 AND b = 3", `(("a" BETWEEN $1 AND $2) AND ("b" = $3))`},
		{"a IN (1, 2) OR b = 3", `(("a" IN ($1, $2)) OR ("b" = $3))`},
		{"a IS NULL AND b IS NOT NULL", `(("a" IS NULL) AND ("b" IS NOT NULL))`},
	}
	for _, tc := range whereCases {
		if got := renderWhereExpr(p, d, tc.expr); got != tc.want {
			t.Errorf("where %q\n  got  %s\n  want %s", tc.expr, got, tc.want)
		}
	}
}

func TestPrecedenceMySQL(t *testing.T) {
	d := sql.MySQLDialect{}
	p := mysql.New()

	selectCases := []struct{ expr, want string }{
		{"a + b * c", "(`a` + (`b` * `c`))"},
		{"a * b + c", "((`a` * `b`) + `c`)"},
		{"a - b - c", "((`a` - `b`) - `c`)"},
		{"a | b & c", "(`a` | (`b` & `c`))"}, // & binds tighter than |
		{"a & b | c", "((`a` & `b`) | `c`)"},
		{"a + b & c", "((`a` + `b`) & `c`)"},   // arithmetic binds tighter than bitwise &
		{"a << b + c", "(`a` << (`b` + `c`))"}, // + binds tighter than shift
	}
	for _, tc := range selectCases {
		if got := renderSelectExpr(p, d, tc.expr); got != tc.want {
			t.Errorf("select %q\n  got  %s\n  want %s", tc.expr, got, tc.want)
		}
	}

	whereCases := []struct{ expr, want string }{
		{"a OR b AND c", "(`a` OR (`b` AND `c`))"},
		{"a AND b OR c", "((`a` AND `b`) OR `c`)"},
		{"a = 1 AND b = 2", "((`a` = ?) AND (`b` = ?))"},
		{"a = 1 OR b = 2 AND c = 3", "((`a` = ?) OR ((`b` = ?) AND (`c` = ?)))"},
		{"NOT a = 1", "(NOT (`a` = ?))"},
		{"a + 1 = b * 2", "((`a` + ?) = (`b` * ?))"},
		{"a XOR b AND c", "(`a` XOR (`b` AND `c`))"}, // AND binds tighter than XOR
	}
	for _, tc := range whereCases {
		if got := renderWhereExpr(p, d, tc.expr); got != tc.want {
			t.Errorf("where %q\n  got  %s\n  want %s", tc.expr, got, tc.want)
		}
	}
}
