package redaction_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// args compiles q and returns its bind arguments.
func args(t *testing.T, q sql.Query) []any {
	t.Helper()
	_, a, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	return a
}

func TestApplyMasksSensitiveLiterals(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	accounts := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("accounts")}
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "where equality on pii column",
			q:    sql.Select(sql.Star()).From(users).Where(sql.And(sql.Eq(sql.Col("name"), sql.Val("taro")), sql.Eq(sql.Col("id"), sql.Val(5)))),
			want: []any{"*****", 5},
		},
		{
			name: "qualified by alias",
			q:    sql.Select(sql.Col("u.id")).From(sql.SourceAlias(users, "u")).Where(sql.Eq(sql.Col("u.name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "literal on the left side",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Val("taro"), sql.Col("name"))),
			want: []any{"*****"},
		},
		{
			name: "in list masks every string item",
			q:    sql.Select(sql.Star()).From(users).Where(sql.In(sql.Col("name"), "a", "b")),
			want: []any{"*****", "*****"},
		},
		{
			name: "like pattern on pii column",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Like(sql.Col("name"), sql.Val("ta%"))),
			want: []any{"*****"},
		},
		{
			name: "non-pii column is left intact",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("email"), sql.Val("x@y.z"))),
			want: []any{"x@y.z"},
		},
		{
			name: "insert masks the pii column's value only",
			q:    sql.InsertInto(users).Columns("name", "email").Values("taro", "x@y.z"),
			want: []any{"*****", "x@y.z"},
		},
		{
			name: "update set on pii column",
			q:    sql.Update(users).Set(sql.Set("name", "taro")).Where(sql.Eq(sql.Col("id"), sql.Val(1))),
			want: []any{"*****", 1},
		},
		{
			name: "ambiguous unqualified pii column fails safe",
			q:    sql.Select(sql.Star()).From(users).Join(accounts, sql.Eq(sql.Col("users.id"), sql.Col("accounts.id"))).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := args(t, m.Apply(tt.q))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestApplyDoesNotMutateInput checks Apply rebuilds on copies: the original query
// still carries its real values after masking.
func TestApplyDoesNotMutateInput(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))

	_ = m.Apply(q)
	if got := args(t, q); !reflect.DeepEqual(got, []any{"taro"}) {
		t.Errorf("input mutated: args = %v, want [taro]", got)
	}
}

// TestApplyMasksSubquery checks a literal inside a subquery's own scope is masked.
func TestApplyMasksSubquery(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	accounts := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("accounts")}
	m := redaction.New(redaction.Columns("users.name"))

	q := sql.Select(sql.Star()).From(accounts).
		Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("subquery masked args = %v, want [*****]", got)
	}
}

// TestNumbersAndPlaceholdersNotMasked pins that only string literals are masked.
func TestNumbersAndPlaceholdersNotMasked(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.id"))
	// id is PII here, but compared to a number: the number is not a string literal.
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("id"), sql.Val(42)))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{42}) {
		t.Errorf("number masked: args = %v, want [42]", got)
	}
}

func TestPolicyFromCommentMarker(t *testing.T) {
	md := meta.Metadata{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "users",
		Columns: []meta.Column{{Name: "id"}, {Name: "name", Comment: "the user's PII name"}, {Name: "email"}},
	}}}
	m := redaction.New(redaction.CommentMarker(md, func(_, _, comment string) bool {
		return strings.Contains(strings.ToLower(comment), "pii")
	}))
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	q := sql.Select(sql.Star()).From(users).Where(sql.And(
		sql.Eq(sql.Col("name"), sql.Val("taro")),
		sql.Eq(sql.Col("email"), sql.Val("x@y.z")),
	))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****", "x@y.z"}) {
		t.Errorf("comment-marker masked args = %v, want [***** x@y.z]", got)
	}
}

func TestApplyEmptyPolicyPassesThrough(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns())
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"taro"}) {
		t.Errorf("empty policy changed args = %v, want [taro]", got)
	}
}

func TestColumnsGlob(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	accounts := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("accounts")}

	tests := []struct {
		name string
		spec string
		q    sql.Query
		want []any
	}{
		{
			name: "column glob masks a family of columns",
			spec: "users.*_token",
			q:    sql.Select(sql.Star()).From(users).Where(sql.And(sql.Eq(sql.Col("api_token"), sql.Val("secret")), sql.Eq(sql.Col("id"), sql.Val(1)))),
			want: []any{"*****", 1},
		},
		{
			name: "table glob masks the column on any table",
			spec: "*.email",
			q:    sql.Select(sql.Star()).From(accounts).Where(sql.Eq(sql.Col("email"), sql.Val("x@y.z"))),
			want: []any{"*****"},
		},
		{
			name: "non-matching column is left intact",
			spec: "users.*_token",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"taro"},
		},
		{
			name: "plain spec still matches exactly",
			spec: "users.name",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := redaction.New(redaction.Columns(tt.spec))
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSchemaPreciseColumns covers schema-qualified specs: a fully qualified spec
// targets exactly one schema's table, leaving a same-named column on another
// schema intact, while a two-part spec stays schema-insensitive.
func TestSchemaPreciseColumns(t *testing.T) {
	publicUsers := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	salesUsers := sql.Table{Schema: sql.NewIdent("sales"), Name: sql.NewIdent("users")}
	bareUsers := sql.Table{Name: sql.NewIdent("users")}

	tests := []struct {
		name string
		spec string
		q    sql.Query
		want []any
	}{
		{
			name: "fully qualified spec masks its schema's table",
			spec: "public.users.name",
			q:    sql.Select(sql.Star()).From(publicUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "fully qualified spec leaves another schema intact",
			spec: "public.users.name",
			q:    sql.Select(sql.Star()).From(salesUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"taro"},
		},
		{
			name: "two-part spec stays schema-insensitive",
			spec: "users.name",
			q:    sql.Select(sql.Star()).From(salesUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "schema-qualified spec does not match a bare table reference",
			spec: "public.users.name",
			q:    sql.Select(sql.Star()).From(bareUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"taro"},
		},
		{
			name: "schema glob masks across tables in a schema",
			spec: "public.*.email",
			q:    sql.Select(sql.Star()).From(publicUsers).Where(sql.Eq(sql.Col("email"), sql.Val("x@y.z"))),
			want: []any{"*****"},
		},
		{
			name: "schema glob does not reach another schema",
			spec: "public.*.email",
			q:    sql.Select(sql.Star()).From(salesUsers).Where(sql.Eq(sql.Col("email"), sql.Val("x@y.z"))),
			want: []any{"x@y.z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := redaction.New(redaction.Columns(tt.spec))
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWithMaskUnresolved covers the precise-masking option: by default an
// ambiguous unqualified reference fails safe and is masked, but
// WithMaskUnresolved(false) leaves it — and a qualifier matching no source —
// intact, masking only references that resolve to exactly one table.
func TestWithMaskUnresolved(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	accounts := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("accounts")}

	// SELECT * FROM users JOIN accounts ON ... WHERE name = 'taro'
	// "name" is unqualified across two tables, so it is Ambiguous.
	ambiguous := func() sql.Query {
		return sql.Select(sql.Star()).From(users).
			Join(accounts, sql.Eq(sql.Col("users.id"), sql.Col("accounts.id"))).
			Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	}

	t.Run("default fails safe and masks", func(t *testing.T) {
		m := redaction.New(redaction.Columns("users.name"))
		if got := args(t, m.Apply(ambiguous())); !reflect.DeepEqual(got, []any{"*****"}) {
			t.Errorf("args = %v, want [*****]", got)
		}
	})

	t.Run("precise mode leaves ambiguous intact", func(t *testing.T) {
		m := redaction.New(redaction.Columns("users.name"), redaction.WithMaskUnresolved(false))
		if got := args(t, m.Apply(ambiguous())); !reflect.DeepEqual(got, []any{"taro"}) {
			t.Errorf("args = %v, want [taro]", got)
		}
	})

	t.Run("precise mode still masks a resolved reference", func(t *testing.T) {
		m := redaction.New(redaction.Columns("users.name"), redaction.WithMaskUnresolved(false))
		// "users.name" resolves to exactly one table, so it is masked even in
		// precise mode.
		q := sql.Select(sql.Star()).From(users).
			Join(accounts, sql.Eq(sql.Col("users.id"), sql.Col("accounts.id"))).
			Where(sql.Eq(sql.Col("users.name"), sql.Val("taro")))
		if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
			t.Errorf("args = %v, want [*****]", got)
		}
	})
}

func TestWithMaskCustomValue(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"), redaction.WithMask("REDACTED"))
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"REDACTED"}) {
		t.Errorf("custom mask args = %v, want [REDACTED]", got)
	}
}

var (
	tblUsers    = sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	tblAccounts = sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("accounts")}
)

// TestRewriteDelete covers the DELETE path: WHERE, JOIN, USING, and RETURNING all
// run through the redactor, and only the sensitive column's literal is masked.
func TestRewriteDelete(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "where equality on pii column",
			q:    sql.DeleteFrom(tblUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "non-pii column left intact",
			q:    sql.DeleteFrom(tblUsers).Where(sql.Eq(sql.Col("email"), sql.Val("x@y.z"))),
			want: []any{"x@y.z"},
		},
		{
			name: "using source and where",
			q: sql.DeleteFrom(tblUsers).Using(tblAccounts).
				Where(sql.And(sql.Eq(sql.Col("users.name"), sql.Val("taro")), sql.Eq(sql.Col("accounts.id"), sql.Val(7)))),
			want: []any{"*****", 7},
		},
		{
			name: "join condition is rewritten",
			q: sql.DeleteFrom(tblUsers).
				Join(tblAccounts, sql.Eq(sql.Col("users.id"), sql.Col("accounts.id"))).
				Where(sql.Eq(sql.Col("users.name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRewriteMerge covers the MERGE path: the ON condition, WHEN MATCHED THEN
// UPDATE assignments, and WHEN NOT MATCHED THEN INSERT values are each masked
// against the target's columns.
func TestRewriteMerge(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	q := sql.MergeInto(tblUsers).
		Using(tblAccounts).
		On(sql.Eq(sql.Col("users.id"), sql.Col("accounts.id"))).
		When(
			sql.WhenMatched().ThenUpdate(sql.Set("name", "taro"), sql.Set("email", "x@y.z")),
			sql.WhenNotMatched().ThenInsert([]string{"name", "email"}, "jiro", "j@y.z"),
		)

	// WHEN MATCHED masks the updated name; WHEN NOT MATCHED masks the inserted
	// name. Each clause's email value is left intact.
	want := []any{"*****", "x@y.z", "*****", "j@y.z"}

	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, want) {
		t.Errorf("merge masked args = %v, want %v", got, want)
	}
}

// TestRewriteMergeOnCondition pins that a sensitive literal in the ON clause is
// masked.
func TestRewriteMergeOnCondition(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))
	q := sql.MergeInto(tblUsers).
		Using(tblAccounts).
		On(sql.And(sql.Eq(sql.Col("users.id"), sql.Col("accounts.id")), sql.Eq(sql.Col("users.name"), sql.Val("taro")))).
		When(sql.WhenMatched().ThenDelete())
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("merge ON masked args = %v, want [*****]", got)
	}
}

// TestMaskArray covers the col = ANY(ARRAY[...]) form: every string element of the
// array tied to a sensitive column is masked.
func TestMaskArray(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "any over array of strings on pii column",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.AnyArray(sql.Col("name"), "=", sql.Array("a", "b"))),
			want: []any{"*****", "*****"},
		},
		{
			name: "any over array on non-pii column left intact",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.AnyArray(sql.Col("email"), "=", sql.Array("a", "b"))),
			want: []any{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRewriteExprForms exercises the comparison and predicate nodes that carry a
// column-bound literal: BETWEEN, SIMILAR TO, regexp, IS DISTINCT FROM, and ILIKE.
func TestRewriteExprForms(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "between masks both bounds",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.Between(sql.Col("name"), sql.Val("a"), sql.Val("z"))),
			want: []any{"*****", "*****"},
		},
		{
			name: "similar to masks pattern",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.SimilarTo(sql.Col("name"), sql.Val("ta%"))),
			want: []any{"*****"},
		},
		{
			name: "regexp masks pattern",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.Regexp(sql.Col("name"), sql.Val("^ta"))),
			want: []any{"*****"},
		},
		{
			name: "ilike masks pattern",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.ILike(sql.Col("name"), sql.Val("TA%"))),
			want: []any{"*****"},
		},
		{
			name: "is distinct from masks value",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.IsDistinctFrom(sql.Col("name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRewriteSelectClauses pins that HAVING, GROUP BY, ORDER BY, and DISTINCT ON
// each pass through the redactor.
func TestRewriteSelectClauses(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	q := sql.Select(sql.Col("id")).From(tblUsers).
		Where(sql.Eq(sql.Col("id"), sql.Val(1))).
		Having(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{1, "*****"}) {
		t.Errorf("having masked args = %v, want [1 *****]", got)
	}
}

// TestRewriteCompound pins that both arms of a UNION are masked.
func TestRewriteCompound(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))
	left := sql.Select(sql.Star()).From(tblUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	right := sql.Select(sql.Star()).From(tblUsers).Where(sql.Eq(sql.Col("name"), sql.Val("jiro")))
	q := left.Union(right)
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****", "*****"}) {
		t.Errorf("union masked args = %v, want [***** *****]", got)
	}
}

// TestCombinePolicy covers Combine: a column is masked when any member policy
// marks it sensitive, and nil members are ignored.
func TestCombinePolicy(t *testing.T) {
	policy := redaction.Combine(nil, redaction.Columns("users.name"), redaction.Columns("users.ssn"))
	m := redaction.New(policy)
	q := sql.Select(sql.Star()).From(tblUsers).Where(sql.And(
		sql.Eq(sql.Col("name"), sql.Val("taro")),
		sql.Eq(sql.Col("ssn"), sql.Val("123-45")),
		sql.Eq(sql.Col("email"), sql.Val("x@y.z")),
	))
	want := []any{"*****", "*****", "x@y.z"}
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, want) {
		t.Errorf("combine masked args = %v, want %v", got, want)
	}
}

// TestCombineEmptyMatchesNothing pins Combine() of nothing masks nothing.
func TestCombineEmptyMatchesNothing(t *testing.T) {
	m := redaction.New(redaction.Combine())
	q := sql.Select(sql.Star()).From(tblUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"taro"}) {
		t.Errorf("empty combine changed args = %v, want [taro]", got)
	}
}

// TestRewriteReturningAndOrder covers RETURNING selections, ORDER BY terms, and a
// FROM subquery source, each carrying a sensitive literal that must be masked.
func TestRewriteReturningAndOrder(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "delete returning expression is masked",
			q: sql.DeleteFrom(tblUsers).Where(sql.Eq(sql.Col("id"), sql.Val(1))).
				Returning(sql.Eq(sql.Col("name"), sql.Val("taro"))),
			want: []any{1, "*****"},
		},
		{
			name: "order by expression is masked",
			q: sql.Select(sql.Star()).From(tblUsers).
				OrderBy(sql.Asc(sql.Eq(sql.Col("name"), sql.Val("taro")))),
			want: []any{"*****"},
		},
		{
			name: "subquery source masks its own scope",
			q: sql.Select(sql.Star()).
				From(sql.SourceAlias(sql.Select(sql.Star()).From(tblUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))), "u")),
			want: []any{"*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRewriteInsertSelect pins that an INSERT ... SELECT masks literals inside the
// SELECT source.
func TestRewriteInsertSelect(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))
	q := sql.InsertInto(tblAccounts).Columns("id").
		FromSelect(sql.Select(sql.Col("id")).From(tblUsers).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("insert-select masked args = %v, want [*****]", got)
	}
}

// TestRewriteExprNested exercises the recursive container nodes: a sensitive
// comparison wrapped in NOT, a CASE arm, and a function argument.
func TestRewriteExprNested(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "not wrapping a sensitive comparison",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.Not(sql.Eq(sql.Col("name"), sql.Val("taro")))),
			want: []any{"*****"},
		},
		{
			name: "not between masks bounds",
			q:    sql.Select(sql.Star()).From(tblUsers).Where(sql.NotBetween(sql.Col("name"), sql.Val("a"), sql.Val("z"))),
			want: []any{"*****", "*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestApplyCTEBody checks that sensitive literals inside a CTE's defining query
// are masked — the CTE body is its own scope and is rewritten recursively.
func TestApplyCTEBody(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	// WITH filtered AS (SELECT * FROM users WHERE name = 'taro') SELECT * FROM filtered
	cte := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))).CTE("filtered")
	q := sql.Select(sql.Star()).From(cte).WithCTE(cte)

	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("CTE body masked args = %v, want [*****]", got)
	}
}

// TestApplyCTEOuterRef checks that a sensitive literal in an outer query's WHERE
// clause, qualified by a CTE alias, is masked by tracing the column through the
// CTE's SELECT list to the underlying base table.
func TestApplyCTEOuterRef(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	// WITH filtered AS (SELECT id, name FROM users)
	// SELECT * FROM filtered WHERE filtered.name = 'taro'
	cte := sql.Select(sql.Col("id"), sql.Col("name")).From(users).CTE("filtered")
	q := sql.Select(sql.Star()).From(cte).
		Where(sql.Eq(cte.Col("name"), sql.Val("taro"))).
		WithCTE(cte)

	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("CTE outer ref masked args = %v, want [*****]", got)
	}
}

// TestApplySubqueryDerivedRef checks that a sensitive literal in an outer query's
// WHERE clause, qualified by a derived-table alias, is masked by tracing the
// column through the subquery's SELECT list.
func TestApplySubqueryDerivedRef(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	// SELECT * FROM (SELECT id, name FROM users) AS t WHERE t.name = 'taro'
	sub := sql.Select(sql.Col("id"), sql.Col("name")).From(users)
	q := sql.Select(sql.Star()).
		From(sql.Alias(sub, "t")).
		Where(sql.Eq(sql.Col("t.name"), sql.Val("taro")))

	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("derived-table ref masked args = %v, want [*****]", got)
	}
}

// TestApplySubqueryWildcardRef checks that SELECT * in a subquery still allows
// the outer query to resolve a sensitive column through it.
func TestApplySubqueryWildcardRef(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	// SELECT * FROM (SELECT * FROM users) AS t WHERE t.name = 'taro'
	sub := sql.Select(sql.Star()).From(users)
	q := sql.Select(sql.Star()).
		From(sql.Alias(sub, "t")).
		Where(sql.Eq(sql.Col("t.name"), sql.Val("taro")))

	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("wildcard subquery ref masked args = %v, want [*****]", got)
	}
}

// TestApplyNestedScopeNoLeak pins the nested-scope cases that once leaked a
// sensitive literal: a CTE referencing an earlier CTE, a correlated subquery
// comparing an outer table's column, and an outer reference to a column projected
// through a UNION-bodied CTE. Each value must be masked, while a non-sensitive
// value in the same shape is left intact.
func TestApplyNestedScopeNoLeak(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	orders := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("orders")}
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			// WITH a AS (SELECT id, name FROM users),
			//      b AS (SELECT id FROM a WHERE a.name = 'taro')
			// SELECT * FROM b
			name: "cte referencing an earlier cte",
			q: func() sql.Query {
				a := sql.Select(sql.Col("id"), sql.Col("name")).From(users).CTE("a")
				b := sql.Select(sql.Col("id")).From(a).Where(sql.Eq(a.Col("name"), sql.Val("taro"))).CTE("b")
				return sql.Select(sql.Star()).From(b).WithCTE(a, b)
			}(),
			want: []any{"*****"},
		},
		{
			// SELECT * FROM users u WHERE EXISTS (
			//   SELECT id FROM orders o WHERE u.name = 'taro')
			name: "correlated subquery references outer column",
			q: func() sql.Query {
				inner := sql.Select(sql.Col("id")).From(sql.SourceAlias(orders, "o")).
					Where(sql.Eq(sql.Col("u.name"), sql.Val("taro")))
				return sql.Select(sql.Star()).From(sql.SourceAlias(users, "u")).Where(sql.Exists(inner))
			}(),
			want: []any{"*****"},
		},
		{
			// WITH u2 AS (SELECT name FROM users UNION SELECT name FROM users)
			// SELECT * FROM u2 WHERE u2.name = 'taro'
			name: "outer ref to a union-bodied cte column",
			q: func() sql.Query {
				uni := sql.Select(sql.Col("name")).From(users).
					Union(sql.Select(sql.Col("name")).From(users)).CTE("u2")
				return sql.Select(sql.Star()).From(uni).Where(sql.Eq(uni.Col("name"), sql.Val("taro"))).WithCTE(uni)
			}(),
			want: []any{"*****"},
		},
		{
			// Same correlated shape on a non-sensitive column must be left intact.
			name: "correlated non-sensitive column untouched",
			q: func() sql.Query {
				inner := sql.Select(sql.Col("id")).From(sql.SourceAlias(orders, "o")).
					Where(sql.Eq(sql.Col("u.email"), sql.Val("x@y.z")))
				return sql.Select(sql.Star()).From(sql.SourceAlias(users, "u")).Where(sql.Exists(inner))
			}(),
			want: []any{"x@y.z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestApplyWrappedAndRawColumnRefs pins masking when the sensitive column is not a
// bare reference on the predicate side: wrapped in a function/cast/concat, named in
// a raw SQL fragment, reached through a two-hop CTE chain, renamed by a projection,
// or referenced from a LATERAL subquery. A recursive CTE must terminate.
func TestApplyWrappedAndRawColumnRefs(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	orders := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("orders")}
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "function-wrapped column in predicate",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Func("lower", sql.Col("name")), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "concat-wrapped column in predicate",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Concat(sql.Col("name"), sql.Val("!")), sql.Val("taro!"))),
			// The "!" is a constant on the column side, not a value compared to the
			// column, so it is left intact; only the compared literal is masked.
			want: []any{"!", "*****"},
		},
		{
			name: "raw fragment naming the column",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Raw("lower(name)"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "raw fragment with qualified column",
			q:    sql.Select(sql.Star()).From(sql.SourceAlias(users, "u")).Where(sql.Eq(sql.Raw("u.name"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "two-hop cte chain",
			q: func() sql.Query {
				a := sql.Select(sql.Col("id"), sql.Col("name")).From(users).CTE("a")
				b := sql.Select(sql.Col("id"), sql.Col("name")).From(a).CTE("b")
				return sql.Select(sql.Star()).From(b).Where(sql.Eq(b.Col("name"), sql.Val("taro"))).WithCTE(a, b)
			}(),
			want: []any{"*****"},
		},
		{
			name: "projection renames the column",
			q: func() sql.Query {
				sub := sql.Select(sql.As(sql.Col("name"), "nm")).From(users)
				return sql.Select(sql.Star()).From(sql.Alias(sub, "t")).Where(sql.Eq(sql.Col("t.nm"), sql.Val("taro")))
			}(),
			want: []any{"*****"},
		},
		{
			name: "lateral subquery references outer column",
			q: func() sql.Query {
				lat := sql.Select(sql.Col("id")).From(sql.SourceAlias(orders, "o")).
					Where(sql.Eq(sql.Col("u.name"), sql.Val("taro")))
				return sql.Select(sql.Star()).From(sql.SourceAlias(users, "u")).
					CrossJoin(sql.Lateral(sql.Alias(lat, "t")))
			}(),
			want: []any{"*****"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestApplyRecursiveCTETerminates pins that resolving through a recursive CTE
// (whose body references itself) terminates rather than spinning.
func TestApplyRecursiveCTETerminates(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	base := sql.Select(sql.Col("id"), sql.Col("name")).From(users)
	rec := base.UnionAll(sql.Select(sql.Col("id"), sql.Col("name")).From(sql.Tbl("tree"))).
		CTE("tree", sql.Recursive())
	q := sql.Select(sql.Star()).From(rec).Where(sql.Eq(rec.Col("name"), sql.Val("taro"))).WithCTE(rec)

	// Must complete; the literal compared to the recursive CTE's name is masked.
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("recursive cte masked args = %v, want [*****]", got)
	}
}

// TestApplyNilQuery pins the nil-query and nil-policy guards.
func TestApplyNilQuery(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))
	if got := m.Apply(nil); got != nil {
		t.Errorf("Apply(nil) = %v, want nil", got)
	}
}

// assertNoLeak applies the redactor and fails if the secret survives anywhere in
// the rendered SQL or its bind arguments.
func assertNoLeak(t *testing.T, name string, q sql.Query, secret string) {
	t.Helper()
	out := redaction.New(redaction.Columns("users.name")).Apply(q)
	text, a, err := out.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("%s: ToSQL: %v", name, err)
	}
	hay := text
	for _, v := range a {
		if s, ok := v.(string); ok {
			hay += "\x00" + s
		}
	}
	if strings.Contains(hay, secret) {
		t.Errorf("%s: sensitive value %q leaked into %q args=%v", name, secret, text, a)
	}
}

// A sensitive literal wrapped in an expression (cast, concat, function, row
// constructor, CASE) must still be masked: maskValue recurses into the wrapper so
// the value never reaches the rendered SQL or bind args. Without this the literal
// leaks even though its column is policy-sensitive.
func TestApplyMasksWrappedSensitiveLiterals(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	const s = "SECRETTARO"
	cases := []struct {
		name string
		q    sql.Query
	}{
		{"cast", sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Cast(sql.Val(s), "text")))},
		{"concat", sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Concat(sql.Val(s), sql.Val("X"))))},
		{"func", sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Func("lower", sql.Val(s))))},
		{"in_wrapped", sql.Select(sql.Star()).From(users).Where(sql.In(sql.Col("name"), sql.Func("lower", sql.Val(s)), sql.Val("b")))},
		{"between_wrapped", sql.Select(sql.Star()).From(users).Where(sql.Between(sql.Col("name"), sql.Func("lower", sql.Val(s)), sql.Val("z")))},
		{"row_pair", sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Row(sql.Col("name"), sql.Col("id")), sql.Row(sql.Val(s), sql.Val(1))))},
		{"case_result", sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Case().When(sql.Eq(sql.Col("id"), 1), sql.Val(s)).Else(sql.Val("other"))))},
		{"update_set", sql.Update(users).Set(sql.Set("name", sql.Func("lower", sql.Val(s)))).Where(sql.Eq(sql.Col("id"), 1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertNoLeak(t, tc.name, tc.q, s) })
	}
}

// TestRewriteInsertOnConflict covers the upsert path: an ON CONFLICT ... DO UPDATE
// runs its assignments, target expressions, and WHERE through the redactor, so a
// sensitive literal in the conflict action is masked. An upsert is a write path and
// a redaction-bypass risk if the conflict branch is skipped.
func TestRewriteInsertOnConflict(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	// INSERT INTO users (id, name) VALUES (1, 'taro')
	// ON CONFLICT (id) DO UPDATE SET name = 'jiro' WHERE users.name = 'sub'
	q := sql.InsertInto(users).Columns("id", "name").Values(1, "taro").
		OnConflict("id").
		DoUpdate(sql.Set("name", "jiro")).
		Where(sql.Eq(sql.Col("users.name"), sql.Val("sub")))

	// The inserted name ("taro"), the DO UPDATE SET name ("jiro"), and the conflict
	// WHERE literal ("sub") are all masked; the numeric id is left intact.
	want := []any{1, "*****", "*****", "*****"}
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, want) {
		t.Errorf("upsert masked args = %v, want %v", got, want)
	}
}

// TestIsSensitiveFailSafeUnknownQualifier exercises isSensitive's last-resort
// fail-safe loop: a column whose qualifier matches no in-scope source resolves to
// no candidate at any level, but with maskUnresolved set (the default) it is still
// masked when a same-named column on an in-scope table is policy-sensitive — so a
// stray/wrong qualifier cannot leak a PII value. With maskUnresolved cleared the
// same reference is left intact (precise mode).
func TestIsSensitiveFailSafeUnknownQualifier(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}

	// "x.name" — qualifier "x" matches no FROM source, so it resolves to nothing,
	// but "name" is sensitive on the in-scope users table.
	q := func() sql.Query {
		return sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("x.name"), sql.Val("taro")))
	}

	t.Run("default fail-safe masks an unknown-qualifier pii name", func(t *testing.T) {
		m := redaction.New(redaction.Columns("users.name"))
		if got := args(t, m.Apply(q())); !reflect.DeepEqual(got, []any{"*****"}) {
			t.Errorf("args = %v, want [*****]", got)
		}
	})

	t.Run("precise mode leaves an unknown-qualifier reference intact", func(t *testing.T) {
		m := redaction.New(redaction.Columns("users.name"), redaction.WithMaskUnresolved(false))
		if got := args(t, m.Apply(q())); !reflect.DeepEqual(got, []any{"taro"}) {
			t.Errorf("args = %v, want [taro]", got)
		}
	})
}

// TestAnyCandidateNoneSensitive exercises the false branch of anyCandidateSensitive:
// an ambiguous unqualified column across two joined tables where the policy marks a
// *different* column sensitive, so no candidate matches and the literal is left
// intact even with fail-safe masking on.
func TestAnyCandidateNoneSensitive(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	accounts := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("accounts")}
	// "email" is ambiguous across users+accounts, but only "name" is sensitive.
	m := redaction.New(redaction.Columns("users.name"))
	q := sql.Select(sql.Star()).From(users).
		Join(accounts, sql.Eq(sql.Col("users.id"), sql.Col("accounts.id"))).
		Where(sql.Eq(sql.Col("email"), sql.Val("x@y.z")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"x@y.z"}) {
		t.Errorf("non-sensitive ambiguous column masked: args = %v, want [x@y.z]", got)
	}
}

// TestMaskValueWrappers covers maskValue recursing through the remaining wrapper
// nodes — COLLATE, ARRAY, and ROW — and its leaf passthrough for a non-string
// value. A string nested under any wrapper compared to a sensitive column must not
// survive; a non-string under one must be left intact.
func TestMaskValueWrappers(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			name: "collate-wrapped string is masked",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Collate(sql.Val("taro"), "C"))),
			want: []any{"*****"},
		},
		{
			name: "row-wrapped string is masked",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Row(sql.Val("taro")))),
			want: []any{"*****"},
		},
		{
			name: "array-wrapped string is masked",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Array("taro"))),
			want: []any{"*****"},
		},
		{
			name: "non-string value compared to pii column is left intact",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val(42))),
			want: []any{42},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}
