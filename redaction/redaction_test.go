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
// run through the masker, and only the sensitive column's literal is masked.
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
// each pass through the masker.
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

// TestApplyNilQuery pins the nil-query and nil-policy guards.
func TestApplyNilQuery(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"))
	if got := m.Apply(nil); got != nil {
		t.Errorf("Apply(nil) = %v, want nil", got)
	}
}
