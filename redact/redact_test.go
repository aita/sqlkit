package redact_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/redact"
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
	m := redact.New(redact.Columns("users.name"))

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
	m := redact.New(redact.Columns("users.name"))
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
	m := redact.New(redact.Columns("users.name"))

	q := sql.Select(sql.Star()).From(accounts).
		Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("subquery masked args = %v, want [*****]", got)
	}
}

// TestNumbersAndPlaceholdersNotMasked pins that only string literals are masked.
func TestNumbersAndPlaceholdersNotMasked(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redact.New(redact.Columns("users.id"))
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
	m := redact.New(redact.CommentMarker(md, "pii"))
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
	m := redact.New(redact.Columns())
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"taro"}) {
		t.Errorf("empty policy changed args = %v, want [taro]", got)
	}
}

func TestWithMaskCustomValue(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redact.New(redact.Columns("users.name"), redact.WithMask("REDACTED"))
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"REDACTED"}) {
		t.Errorf("custom mask args = %v, want [REDACTED]", got)
	}
}
