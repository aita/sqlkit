package redaction_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// piiSecret is the sentinel literal the generator always ties to the sensitive
// column users.name. Because every generated reference denotes that column, the
// secret must never survive into the rendered SQL or its bind arguments — any
// occurrence is a leak.
const piiSecret = "PIISECRET"

// fuzzBytes is a tiny byte-driven chooser the generator reads to pick shapes, so
// the libFuzzer mutation of the input bytes explores the space of queries.
type fuzzBytes struct {
	b []byte
	i int
}

func (g *fuzzBytes) next() byte {
	if g.i >= len(g.b) {
		g.i++
		return 0
	}
	v := g.b[g.i]
	g.i++
	return v
}

func (g *fuzzBytes) choice(n int) int { return int(g.next()) % n }

func (g *fuzzBytes) bool() bool { return g.next()&1 == 1 }

var (
	fuzzUsers  = sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	fuzzOrders = sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("orders")}
)

// wrapNameRef returns an expression that reads the users.name column referenced by
// base (e.g. "name", "u.name", "t.name"), optionally wrapped so the wrapper still
// reads the sensitive column.
func wrapNameRef(g *fuzzBytes, base string) sql.Expression {
	ref := sql.Col(base)
	switch g.choice(6) {
	case 0:
		return ref
	case 1:
		return sql.Func("lower", ref)
	case 2:
		return sql.Concat(ref, sql.Val("x"))
	case 3:
		return sql.Cast(ref, "text")
	case 4:
		return sql.Raw(base)
	default:
		return sql.Raw("lower(" + base + ")")
	}
}

// predicate ties ref (which reads users.name) to the secret through a comparison,
// optionally negated, so the secret is always compared to the sensitive column.
func predicate(g *fuzzBytes, ref sql.Expression) sql.Expression {
	var p sql.Expression
	switch g.choice(12) {
	case 0:
		p = sql.Eq(ref, sql.Val(piiSecret))
	case 1:
		p = sql.Eq(sql.Val(piiSecret), ref)
	case 2:
		p = sql.In(ref, piiSecret, "other")
	case 3:
		p = sql.Like(ref, sql.Val(piiSecret))
	case 4:
		p = sql.Between(ref, sql.Val(piiSecret), sql.Val("z"))
	case 5:
		p = sql.IsDistinctFrom(ref, sql.Val(piiSecret))
	case 6:
		// Row-wise membership: (ref, id) IN ((secret, 1), (other, 2)).
		p = sql.In(sql.Row(ref, sql.Col("id")), sql.Row(sql.Val(piiSecret), sql.Val(1)), sql.Row(sql.Val("other"), sql.Val(2)))
	case 7:
		// Row-wise IS DISTINCT FROM: (ref, id) IS DISTINCT FROM (secret, 1).
		p = sql.IsDistinctFrom(sql.Row(ref, sql.Col("id")), sql.Row(sql.Val(piiSecret), sql.Val(1)))
	case 8:
		// Quantified array, ARRAY[...] constructor: ref = ANY(ARRAY[secret, x]).
		p = sql.AnyArray(ref, "=", sql.Array(sql.Val(piiSecret), sql.Val("x")))
	case 9:
		// Quantified array, cast of an array literal: ref = ANY('{secret}'::text[]).
		p = sql.AnyArray(ref, "=", sql.Cast(sql.Val("{"+piiSecret+"}"), "text[]"))
	case 10:
		// Simple CASE: CASE ref WHEN secret THEN 1 END compared for equality.
		p = sql.Eq(sql.CaseOf(ref).When(sql.Val(piiSecret), 1).Else(0), sql.Val(1))
	default:
		p = sql.ILike(ref, sql.Val(piiSecret))
	}
	if g.bool() {
		p = sql.Not(p)
	}
	if g.bool() {
		p = sql.And(p, sql.Eq(sql.Col("id"), sql.Val(1)))
	}
	return p
}

// buildQuery assembles a query that compares users.name to the secret through one
// of several scope shapes: a direct WHERE, a derived-table re-projection, a CTE
// (plain, UNION-bodied, or with a renamed output column), or a correlated
// subquery. The predicate itself spans the comparison forms in predicate, so the
// secret reaches the sensitive column through row-wise, quantified-array, and
// simple-CASE shapes as well as the scalar ones.
func buildQuery(g *fuzzBytes) sql.Query {
	switch g.choice(7) {
	case 0:
		// SELECT * FROM users WHERE <pred on name>
		return sql.Select(sql.Star()).From(fuzzUsers).Where(predicate(g, wrapNameRef(g, "name")))
	case 1:
		// SELECT * FROM users u WHERE <pred on u.name>
		return sql.Select(sql.Star()).From(sql.SourceAlias(fuzzUsers, "u")).Where(predicate(g, wrapNameRef(g, "u.name")))
	case 2:
		// SELECT * FROM (SELECT name FROM users) t WHERE <pred on t.name>
		inner := sql.Select(sql.Col("name")).From(fuzzUsers)
		return sql.Select(sql.Star()).From(sql.Alias(inner, "t")).Where(predicate(g, wrapNameRef(g, "t.name")))
	case 3:
		// WITH c AS (SELECT name FROM users) SELECT * FROM c WHERE <pred on c.name>
		cte := sql.Select(sql.Col("name")).From(fuzzUsers).CTE("c")
		return sql.Select(sql.Star()).From(cte).Where(predicate(g, wrapNameRef(g, "c.name"))).WithCTE(cte)
	case 4:
		// WITH c AS (SELECT name FROM users UNION SELECT name FROM users)
		// SELECT * FROM c WHERE <pred on c.name>
		cte := sql.Select(sql.Col("name")).From(fuzzUsers).
			Union(sql.Select(sql.Col("name")).From(fuzzUsers)).CTE("c")
		return sql.Select(sql.Star()).From(cte).Where(predicate(g, wrapNameRef(g, "c.name"))).WithCTE(cte)
	case 5:
		// WITH c(alias) AS (SELECT name FROM users)  -- renamed output column
		// SELECT * FROM c WHERE <pred on c.alias>
		cte := sql.Select(sql.Col("name")).From(fuzzUsers).CTE("c", sql.Columns("alias"))
		return sql.Select(sql.Star()).From(cte).Where(predicate(g, wrapNameRef(g, "c.alias"))).WithCTE(cte)
	default:
		// SELECT * FROM users u WHERE EXISTS (
		//   SELECT id FROM orders o WHERE <pred on u.name>)   -- correlated
		inner := sql.Select(sql.Col("id")).From(sql.SourceAlias(fuzzOrders, "o")).
			Where(predicate(g, wrapNameRef(g, "u.name")))
		return sql.Select(sql.Star()).From(sql.SourceAlias(fuzzUsers, "u")).Where(sql.Exists(inner))
	}
}

// rendered returns the SQL text with bind arguments inlined as a single haystack,
// so a leaked secret is found whether it rode in the text or in an arg.
func rendered(t *testing.T, q sql.Query) (string, bool) {
	t.Helper()
	text, args, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		return "", false
	}
	hay := text
	for _, v := range args {
		if s, ok := v.(string); ok {
			hay += "\x00" + s
		}
	}
	return hay, true
}

// FuzzRedactNoLeak drives the generator from the fuzz input: every query it builds
// compares users.name to the sentinel secret, so after redaction the secret must
// be gone. A surviving secret is a masking leak.
func FuzzRedactNoLeak(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{1, 2, 3})
	f.Add([]byte{5, 4, 3, 2, 1, 0})
	f.Add([]byte{6, 1, 1, 1, 6, 2, 3, 4, 5})
	for i := 0; i < 64; i++ {
		f.Add([]byte{byte(i), byte(i * 7), byte(i * 13), byte(i * 31)})
	}

	m := redaction.New(redaction.Columns("users.name"))
	f.Fuzz(func(t *testing.T, data []byte) {
		g := &fuzzBytes{b: data}
		q := buildQuery(g)

		// The original must contain the secret (sanity: the generator placed it).
		if hay, ok := rendered(t, q); !ok || !strings.Contains(hay, piiSecret) {
			return
		}
		out := m.Apply(q)
		hay, ok := rendered(t, out)
		if !ok {
			t.Fatalf("redacted query failed to compile")
		}
		if strings.Contains(hay, piiSecret) {
			text, args, _ := out.ToSQL(sql.PostgreSQLDialect{})
			t.Fatalf("LEAK: secret survived redaction\n  sql:  %s\n  args: %v", text, args)
		}
		// Redaction must also be idempotent and preserve compilability.
		twice, ok := rendered(t, m.Apply(out))
		if !ok || twice != hay {
			t.Fatalf("redaction not idempotent\n  once:  %s\n  twice: %s", hay, twice)
		}
	})
}
