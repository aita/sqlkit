package analysis_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// TestFingerprintUnifiesPlaceholders checks the parsed path: a recompiled query
// renders every bind as "?", so queries differing only in their values (or
// placeholder numbering) share a fingerprint with no numbered placeholder left.
func TestFingerprintUnifiesPlaceholders(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q1 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(1)), sql.Eq(sql.Col("name"), sql.Val("a"))))
	q2 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(999)), sql.Eq(sql.Col("name"), sql.Val("z"))))

	f1, ok1 := analysis.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analysis.Fingerprint(q2, sql.PostgreSQLDialect{})
	if !ok1 || !ok2 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok1, ok2)
	}
	if f1 != f2 {
		t.Errorf("fingerprints differ for value-only difference:\n %q\n %q", f1, f2)
	}
	if strings.ContainsAny(f1, "$") {
		t.Errorf("fingerprint kept a numbered placeholder: %q", f1)
	}
	if !strings.Contains(f1, "?") {
		t.Errorf("fingerprint has no ? placeholder: %q", f1)
	}
}

// TestFingerprintCollapsesRawPlaceholders covers the raw-text path that the
// builder's sql.Val tests miss. The postgres frontend lowers $N placeholders to
// sql.Raw("$N") fragments, which are re-emitted verbatim and never flow through
// Placeholder. Two queries differing only in placeholder numbering must still
// share a fingerprint, with no numbered placeholder surviving.
func TestFingerprintCollapsesRawPlaceholders(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	// sql.Raw("$1") mimics what the postgres frontend produces: a raw passthrough
	// fragment, not a bind value that would route through Placeholder().
	q1 := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Raw("$1")))
	q2 := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Raw("$2")))

	f1, ok1 := analysis.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analysis.Fingerprint(q2, sql.PostgreSQLDialect{})
	if !ok1 || !ok2 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok1, ok2)
	}
	if f1 != f2 {
		t.Errorf("fingerprints differ for $1 vs $2:\n %q\n %q", f1, f2)
	}
	if strings.ContainsAny(f1, "$") {
		t.Errorf("fingerprint kept a numbered placeholder: %q", f1)
	}
	if !strings.Contains(f1, "?") {
		t.Errorf("fingerprint has no ? placeholder: %q", f1)
	}
}

// TestFingerprintCollapsesLimitOffset checks that LIMIT/OFFSET counts, which the
// compiler emits as plain integer literals (not placeholders), are normalized so
// that paginated reads differing only in page size or offset share a fingerprint.
func TestFingerprintCollapsesLimitOffset(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q1 := sql.Select(sql.Col("id")).From(users).Limit(10).Offset(0)
	q2 := sql.Select(sql.Col("id")).From(users).Limit(20).Offset(100)

	f1, ok1 := analysis.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analysis.Fingerprint(q2, sql.PostgreSQLDialect{})
	if !ok1 || !ok2 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok1, ok2)
	}
	if f1 != f2 {
		t.Errorf("fingerprints differ for LIMIT/OFFSET-only difference:\n %q\n %q", f1, f2)
	}
	if !strings.Contains(f1, "LIMIT ?") || !strings.Contains(f1, "OFFSET ?") {
		t.Errorf("LIMIT/OFFSET operands not collapsed: %q", f1)
	}

	// LIMIT alone and OFFSET alone must each collapse too.
	lim10, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Limit(10), sql.PostgreSQLDialect{})
	lim20, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Limit(20), sql.PostgreSQLDialect{})
	if lim10 != lim20 {
		t.Errorf("LIMIT 10 vs 20 differ:\n %q\n %q", lim10, lim20)
	}
	off0, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Offset(0), sql.PostgreSQLDialect{})
	off100, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Offset(100), sql.PostgreSQLDialect{})
	if off0 != off100 {
		t.Errorf("OFFSET 0 vs 100 differ:\n %q\n %q", off0, off100)
	}
}

// TestFingerprintNoNewCollisions guards against over-collapse: structurally
// different queries must keep distinct fingerprints even after the $N and
// LIMIT/OFFSET normalization passes. Clauses that change the shape of the query
// (presence vs absence of LIMIT/OFFSET, IN vs NOT IN, UNION vs UNION ALL, a
// different column/table/operator) must still differ.
func TestFingerprintNoNewCollisions(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	posts := sql.Table{Name: sql.NewIdent("posts")}
	base := func() sql.SelectBuilder { return sql.Select(sql.Col("id")).From(users) }

	cases := map[string]sql.Query{
		"in":            base().Where(sql.In(sql.Col("id"), 1, 2, 3)),
		"not_in":        base().Where(sql.NotIn(sql.Col("id"), 1, 2, 3)),
		"eq":            base().Where(sql.Eq(sql.Col("id"), 1)),
		"ne":            base().Where(sql.Ne(sql.Col("id"), 1)),
		"gt":            base().Where(sql.Gt(sql.Col("id"), 1)),
		"other_col":     base().Where(sql.Eq(sql.Col("name"), "x")),
		"other_table":   sql.Select(sql.Col("id")).From(posts).Where(sql.Eq(sql.Col("id"), 1)),
		"union":         base().Union(sql.Select(sql.Col("id")).From(posts)),
		"union_all":     base().UnionAll(sql.Select(sql.Col("id")).From(posts)),
		"eq_with_limit": base().Where(sql.Eq(sql.Col("id"), 1)).Limit(10),
		"limit_only":    base().Limit(10),
		"offset_only":   base().Offset(10),
		// Note: a raw $N placeholder is intentionally NOT listed here — it
		// canonicalizes to the same "?" as the "eq" bind value, which is the
		// desired collapse (see TestFingerprintCollapsesRawPlaceholders), not a
		// spurious collision.
	}

	seen := make(map[string]string, len(cases))
	for name, q := range cases {
		f, ok := analysis.Fingerprint(q, sql.PostgreSQLDialect{})
		if !ok {
			t.Fatalf("Fingerprint(%s) ok = false", name)
		}
		if prev, dup := seen[f]; dup {
			t.Errorf("collision: %q and %q both fingerprint to %q", prev, name, f)
		}
		seen[f] = name
	}
}

// TestFingerprintCollapsesInList checks that an IN (...) list of bound values is
// count-insensitive: queries differing only in the number of IN items share a
// fingerprint, and the list renders as a single "?".
func TestFingerprintCollapsesInList(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q3 := sql.Select(sql.Col("id")).From(users).Where(sql.In(sql.Col("id"), 1, 2, 3))
	q5 := sql.Select(sql.Col("id")).From(users).Where(sql.In(sql.Col("id"), 1, 2, 3, 4, 5))

	f3, ok3 := analysis.Fingerprint(q3, sql.PostgreSQLDialect{})
	f5, ok5 := analysis.Fingerprint(q5, sql.PostgreSQLDialect{})
	if !ok3 || !ok5 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok3, ok5)
	}
	if f3 != f5 {
		t.Errorf("fingerprints differ for IN item count:\n %q\n %q", f3, f5)
	}
	if strings.Count(f3, "?") != 1 {
		t.Errorf("IN list not collapsed to a single placeholder: %q", f3)
	}

	// An IN list of non-value expressions (column references) must NOT collapse,
	// since those distinguish genuinely different statements.
	cols := sql.Select(sql.Col("id")).From(users).
		Where(sql.In(sql.Col("id"), sql.Col("a"), sql.Col("b")))
	f, _ := analysis.Fingerprint(cols, sql.PostgreSQLDialect{})
	if !strings.Contains(f, `"a", "b"`) {
		t.Errorf("IN of column refs was collapsed or altered: %q", f)
	}
}

// TestFingerprintCollapsesValuesRows checks that an INSERT and a standalone
// VALUES query are insensitive to the number of bound-value rows: batches of
// different sizes share a fingerprint.
func TestFingerprintCollapsesValuesRows(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}

	ins2 := sql.InsertInto(users).Columns("id", "name").Values(1, "a").Values(2, "b")
	ins3 := sql.InsertInto(users).Columns("id", "name").Values(1, "a").Values(2, "b").Values(3, "c")
	fi2, ok2 := analysis.Fingerprint(ins2, sql.PostgreSQLDialect{})
	fi3, ok3 := analysis.Fingerprint(ins3, sql.PostgreSQLDialect{})
	if !ok2 || !ok3 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok2, ok3)
	}
	if fi2 != fi3 {
		t.Errorf("fingerprints differ for INSERT row count:\n %q\n %q", fi2, fi3)
	}
	if strings.Count(fi2, "(?, ?)") != 1 || strings.Contains(fi2, "), (") {
		t.Errorf("INSERT rows not collapsed to one row: %q", fi2)
	}

	v1 := sql.Values([]sql.Expression{sql.Val(1), sql.Val(2)})
	v2 := v1.Row(3, 4)
	fv1, _ := analysis.Fingerprint(v1, sql.PostgreSQLDialect{})
	fv2, _ := analysis.Fingerprint(v2, sql.PostgreSQLDialect{})
	if fv1 != fv2 {
		t.Errorf("fingerprints differ for VALUES row count:\n %q\n %q", fv1, fv2)
	}
}

// TestFingerprintMultiSyntax exercises the parsed path across a broad set of SQL
// shapes under both shipped dialects. Each case pairs two queries that differ
// only in their bound values or in the arity of a value list, so a correct
// fingerprint must map them together; it must also be deterministic and never
// leak a numbered placeholder. This guards the normalization passes against
// regressions on syntax beyond the simple SELECT the focused tests use.
func TestFingerprintMultiSyntax(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	posts := sql.Table{Name: sql.NewIdent("posts")}
	sel := func() sql.SelectBuilder { return sql.Select(sql.Col("id")).From(users) }
	on := sql.Eq(sql.Col("posts.user_id"), sql.Col("users.id"))
	sub := func(v int) sql.Query {
		return sql.Select(sql.Col("user_id")).From(posts).Where(sql.Eq(sql.Col("views"), v))
	}

	cteA := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), 1)).CTE("c")
	cteB := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), 9)).CTE("c")

	cases := []struct {
		name string
		a, b sql.Query
	}{
		{"eq", sel().Where(sql.Eq(sql.Col("id"), 1)), sel().Where(sql.Eq(sql.Col("id"), 2))},
		{"and_or",
			sel().Where(sql.Or(sql.And(sql.Eq(sql.Col("id"), 1), sql.Gt(sql.Col("age"), 18)), sql.Eq(sql.Col("name"), "a"))),
			sel().Where(sql.Or(sql.And(sql.Eq(sql.Col("id"), 9), sql.Gt(sql.Col("age"), 65)), sql.Eq(sql.Col("name"), "z")))},
		{"in_arity", sel().Where(sql.In(sql.Col("id"), 1, 2, 3)), sel().Where(sql.In(sql.Col("id"), 1, 2, 3, 4, 5, 6))},
		{"not_in_arity", sel().Where(sql.NotIn(sql.Col("id"), 1, 2)), sel().Where(sql.NotIn(sql.Col("id"), 1, 2, 3, 4))},
		{"between", sel().Where(sql.Between(sql.Col("age"), 1, 10)), sel().Where(sql.Between(sql.Col("age"), 20, 90))},
		{"like", sel().Where(sql.Like(sql.Col("name"), "a%")), sel().Where(sql.Like(sql.Col("name"), "zzz%"))},
		{"join",
			sel().Join(posts, on).Where(sql.Eq(sql.Col("posts.views"), 1)),
			sel().Join(posts, on).Where(sql.Eq(sql.Col("posts.views"), 42))},
		{"left_join",
			sel().LeftJoin(posts, on).Where(sql.Gt(sql.Col("id"), 1)),
			sel().LeftJoin(posts, on).Where(sql.Gt(sql.Col("id"), 7))},
		{"group_having",
			sel().GroupBy(sql.Col("name")).Having(sql.Gt(sql.CountAll(), 1)),
			sel().GroupBy(sql.Col("name")).Having(sql.Gt(sql.CountAll(), 100))},
		{"order_limit_offset",
			sel().OrderBy(sql.Asc(sql.Col("id"))).Limit(10).Offset(0),
			sel().OrderBy(sql.Asc(sql.Col("id"))).Limit(50).Offset(200)},
		{"union",
			sel().Where(sql.Eq(sql.Col("id"), 1)).Union(sql.Select(sql.Col("id")).From(posts).Where(sql.Eq(sql.Col("views"), 1))),
			sel().Where(sql.Eq(sql.Col("id"), 5)).Union(sql.Select(sql.Col("id")).From(posts).Where(sql.Eq(sql.Col("views"), 9)))},
		{"subquery_in", sel().Where(sql.InQuery(sql.Col("id"), sub(1))), sel().Where(sql.InQuery(sql.Col("id"), sub(2)))},
		{"exists", sel().Where(sql.Exists(sub(1))), sel().Where(sql.Exists(sub(2)))},
		{"func_call",
			sel().Where(sql.Eq(sql.Func("coalesce", sql.Col("age"), 0), 1)),
			sel().Where(sql.Eq(sql.Func("coalesce", sql.Col("age"), 0), 99))},
		{"case",
			sel().Where(sql.Eq(sql.Case().When(sql.Eq(sql.Col("status"), "a"), sql.Val(1)).Else(sql.Val(0)), 1)),
			sel().Where(sql.Eq(sql.Case().When(sql.Eq(sql.Col("status"), "x"), sql.Val(5)).Else(sql.Val(9)), 5))},
		{"insert_arity",
			sql.InsertInto(users).Columns("id", "name").Values(1, "a").Values(2, "b"),
			sql.InsertInto(users).Columns("id", "name").Values(1, "a").Values(2, "b").Values(3, "c").Values(4, "d")},
		{"update",
			sql.Update(users).Set(sql.Set("name", "a")).Where(sql.Eq(sql.Col("id"), 1)),
			sql.Update(users).Set(sql.Set("name", "zzz")).Where(sql.Eq(sql.Col("id"), 9))},
		{"delete", sql.DeleteFrom(users).Where(sql.Eq(sql.Col("id"), 1)), sql.DeleteFrom(users).Where(sql.Eq(sql.Col("id"), 8))},
		{"cte",
			sql.Select(sql.Col("id")).WithCTE(cteA).From(cteA.SQLSource()),
			sql.Select(sql.Col("id")).WithCTE(cteB).From(cteB.SQLSource())},
	}

	dialects := map[string]sql.Dialect{
		"postgres": sql.PostgreSQLDialect{},
		"mysql":    sql.MySQLDialect{},
	}
	for _, tc := range cases {
		for dn, d := range dialects {
			fa, oka := analysis.Fingerprint(tc.a, d)
			fb, okb := analysis.Fingerprint(tc.b, d)
			if !oka || !okb {
				t.Errorf("%s/%s: Fingerprint ok = %v, %v, want both true", tc.name, dn, oka, okb)
				continue
			}
			if fa != fb {
				t.Errorf("%s/%s: fingerprints differ for value/arity-only change:\n %q\n %q", tc.name, dn, fa, fb)
			}
			if fa == "" {
				t.Errorf("%s/%s: empty fingerprint", tc.name, dn)
			}
			if strings.ContainsAny(fa, "$") {
				t.Errorf("%s/%s: leaked numbered placeholder: %q", tc.name, dn, fa)
			}
			if again, _ := analysis.Fingerprint(tc.a, d); again != fa {
				t.Errorf("%s/%s: not deterministic:\n %q\n %q", tc.name, dn, fa, again)
			}
		}
	}
}

// FuzzFingerprintRaw drives the parser-free fallback with arbitrary text. The
// pass is regex-only, so its hazards are panics and non-convergence; the fuzzer
// asserts neither happens — FingerprintRaw must not panic, and it must be
// idempotent, since a fully normalized statement re-normalizes to itself. A
// counterexample to idempotence is a normalization bug (a literal, placeholder,
// or value list that one pass leaves in a form the next pass still rewrites).
func FuzzFingerprintRaw(f *testing.F) {
	seeds := []string{
		"SELECT * FROM users WHERE id = 1",
		"select * from users where id in (1, 2, 3)",
		"INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c')",
		"UPDATE users SET name = 'x' WHERE id = $1",
		"DELETE FROM users WHERE id IN ($1, $2)",
		"SELECT a,b ,  c FROM t WHERE x = 'it''s' AND y IN ( 1 ,2, 3 )",
		"SELECT * FROM t LIMIT 10 OFFSET 20",
		"VALUES (1),(2),(3)",
		"",
		"'unterminated",
		"in(1,2,3) values(1),(2)",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := analysis.FingerprintRaw(s)
		if again := analysis.FingerprintRaw(got); again != got {
			t.Errorf("FingerprintRaw not idempotent:\n in:    %q\n once:  %q\n twice: %q", s, got, again)
		}
	})
}

// TestFingerprintInSubqueryPreserved checks that the arity collapse only touches
// value lists: an IN (SELECT …) subquery is left whole (it never starts with the
// canonical "?", so the IN pass cannot match it), while a value list nested
// inside that subquery still collapses. NOT IN (SELECT …) is preserved likewise.
func TestFingerprintInSubqueryPreserved(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	posts := sql.Table{Name: sql.NewIdent("posts")}
	d := sql.PostgreSQLDialect{}

	sub := sql.Select(sql.Col("user_id")).From(posts).Where(sql.Eq(sql.Col("views"), 5))
	inSubq, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Where(sql.InQuery(sql.Col("id"), sub)), d)
	if !strings.Contains(inSubq, "IN (SELECT") {
		t.Errorf("IN subquery was collapsed or altered: %q", inSubq)
	}

	notInSubq, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Where(sql.NotInQuery(sql.Col("id"), sub)), d)
	if !strings.Contains(notInSubq, "NOT IN (SELECT") {
		t.Errorf("NOT IN subquery was collapsed or altered: %q", notInSubq)
	}

	// A value list inside the subquery must still collapse, even though the outer
	// IN (SELECT …) is preserved.
	innerList := sql.Select(sql.Col("user_id")).From(posts).Where(sql.In(sql.Col("tag"), 7, 8, 9, 10))
	nested, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Where(sql.InQuery(sql.Col("id"), innerList)), d)
	if !strings.Contains(nested, "IN (SELECT") || strings.Contains(nested, "?, ?") {
		t.Errorf("nested subquery handling wrong (outer kept, inner collapsed expected): %q", nested)
	}
}

// FuzzFingerprintParsed drives the parsed path with fuzzed values and IN arity,
// asserting its core invariants as properties rather than fixed examples:
//   - a numbered placeholder never leaks (every bind becomes "?"),
//   - the fingerprint is deterministic,
//   - two statements of the same shape share a fingerprint regardless of their
//     bound values or IN-list length, and
//   - two statements of different shape (one with an IN clause, one without) do
//     not collide.
//
// Values flow through the AST as binds, so even hostile strings (quotes, "$3",
// keywords) render as "?" and cannot perturb the result — exactly the guarantee
// under test.
func FuzzFingerprintParsed(f *testing.F) {
	f.Add(1, 2, "a", "z", 3, 5)
	f.Add(0, 0, "", "", 0, 0)
	f.Add(-7, 99, "O'Brien", "x$3y", 1, 8)

	clamp := func(n int) int {
		n %= 8
		if n < 0 {
			n = -n
		}
		return n
	}
	users := sql.Table{Name: sql.NewIdent("users")}
	build := func(id int, name string, inCount int) sql.Query {
		where := sql.And(sql.Eq(sql.Col("id"), id), sql.Eq(sql.Col("name"), name))
		if n := clamp(inCount); n > 0 {
			items := make([]any, n)
			for i := range items {
				items[i] = id + i
			}
			where = sql.And(where, sql.In(sql.Col("tag"), items...))
		}
		return sql.Select(sql.Col("id")).From(users).Where(where)
	}

	f.Fuzz(func(t *testing.T, idA, idB int, nameA, nameB string, inA, inB int) {
		d := sql.PostgreSQLDialect{}
		fa, oka := analysis.Fingerprint(build(idA, nameA, inA), d)
		fb, okb := analysis.Fingerprint(build(idB, nameB, inB), d)
		if !oka || !okb {
			t.Fatalf("Fingerprint ok = %v, %v, want both true", oka, okb)
		}
		if strings.ContainsAny(fa, "$") {
			t.Errorf("leaked numbered placeholder: %q", fa)
		}
		if again, _ := analysis.Fingerprint(build(idA, nameA, inA), d); again != fa {
			t.Errorf("not deterministic:\n %q\n %q", fa, again)
		}
		sameShape := (clamp(inA) > 0) == (clamp(inB) > 0)
		if sameShape && fa != fb {
			t.Errorf("value/arity-only difference changed fingerprint:\n %q\n %q", fa, fb)
		}
		if !sameShape && fa == fb {
			t.Errorf("different shapes collided onto %q", fa)
		}
	})
}

// TestFingerprintRawMultiSyntax broadens the parser-free fallback beyond the
// simple statements TestFingerprintRaw covers, pinning how it normalizes IN and
// VALUES lists, function-call arguments (which must NOT collapse), irregular
// spacing, and mixed keyword case across a range of statement shapes.
func TestFingerprintRawMultiSyntax(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"in_list", "SELECT id FROM t WHERE id IN (1, 2, 3)", "SELECT id FROM t WHERE id IN (?)"},
		{"in_list_no_spaces", "SELECT id FROM t WHERE id in(1,2,3)", "SELECT id FROM t WHERE id in (?)"},
		{"not_in_list", "DELETE FROM t WHERE id NOT IN ($1, $2, $3)", "DELETE FROM t WHERE id NOT IN (?)"},
		{"insert_rows", "INSERT INTO t (a,b) VALUES (1,2),(3,4),(5,6)", "INSERT INTO t (a, b) VALUES (?, ?)"},
		{"values_query", "values (1),(2),(3)", "values (?)"},
		{"func_args_kept", "SELECT coalesce(a, 1), b FROM t", "SELECT coalesce(a, ?), b FROM t"},
		{"two_in_lists", "SELECT * FROM t WHERE x IN (1,2) OR y IN (3,4,5)", "SELECT * FROM t WHERE x IN (?) OR y IN (?)"},
		{"single_in_unchanged", "SELECT * FROM t WHERE id IN (1)", "SELECT * FROM t WHERE id IN (?)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analysis.FingerprintRaw(tt.in); got != tt.want {
				t.Errorf("FingerprintRaw(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFingerprintNil(t *testing.T) {
	if _, ok := analysis.Fingerprint(nil, sql.PostgreSQLDialect{}); ok {
		t.Error("Fingerprint(nil) ok = true, want false")
	}
}

func TestFingerprintRaw(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "string and number literals",
			in:   "SELECT * FROM users WHERE id = 123 AND name = 'ryo'",
			want: "SELECT * FROM users WHERE id = ? AND name = ?",
		},
		{
			name: "postgres placeholders",
			in:   "SELECT * FROM users WHERE id = $1 AND name = $2",
			want: "SELECT * FROM users WHERE id = ? AND name = ?",
		},
		{
			name: "question placeholders",
			in:   "SELECT * FROM users WHERE id = ? AND name = ?",
			want: "SELECT * FROM users WHERE id = ? AND name = ?",
		},
		{
			name: "collapses whitespace",
			in:   "SELECT   *\n  FROM users\t WHERE id = 1",
			want: "SELECT * FROM users WHERE id = ?",
		},
		{
			// A doubled quote escapes a quote within the literal; it must collapse
			// to a single "?" so it groups with any other value for the column.
			name: "escaped doubled quote in string literal",
			in:   "SELECT * FROM users WHERE name = 'O''Brien'",
			want: "SELECT * FROM users WHERE name = ?",
		},
		{
			// Padding inside the parentheses must not defeat the IN arity collapse:
			// the list still reduces to a single "?" so it groups with the unpadded
			// form and with lists of other lengths.
			name: "in list with parenthesis padding",
			in:   "SELECT * FROM users WHERE id IN ( 1, 2, 3 )",
			want: "SELECT * FROM users WHERE id IN (?)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analysis.FingerprintRaw(tt.in); got != tt.want {
				t.Errorf("FingerprintRaw(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectKind(t *testing.T) {
	tests := []struct {
		in   string
		want analysis.StmtKind
	}{
		{"SELECT 1", analysis.StmtSelect},
		{"  select * from t", analysis.StmtSelect},
		{"WITH x AS (SELECT 1) SELECT * FROM x", analysis.StmtSelect},
		{"INSERT INTO t VALUES (1)", analysis.StmtInsert},
		{"UPDATE t SET x = 1", analysis.StmtUpdate},
		{"DELETE FROM t", analysis.StmtDelete},
		{"CREATE TABLE t (id int)", analysis.StmtDDL},
		{"-- a comment\nSELECT 1", analysis.StmtSelect},
		{"/* c */ DELETE FROM t", analysis.StmtDelete},
		{"EXPLAIN SELECT 1", analysis.StmtUnknown},
	}
	for _, tt := range tests {
		if got := analysis.DetectKind(tt.in); got != tt.want {
			t.Errorf("DetectKind(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
