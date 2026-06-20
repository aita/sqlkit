package sql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

// secret is a named string type and token a struct that wraps a string through
// its String method — two shapes Val cannot detect, so the inline mode relies on
// an explicit Quote to single-quote them. secure opts in through the Quoter
// interface, so Val marks it without an explicit Quote.
type secret string

type token struct{ v string }

func (t token) String() string { return t.v }

type secure struct{ v string }

func (s secure) SQLQuoted() bool { return true }
func (s secure) String() string  { return s.v }

func TestCompileInline(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}

	tests := []struct {
		name string
		q    sql.Query
		want string
	}{
		{
			name: "string and number literals",
			q:    sql.Select(sql.Star()).From(users).Where(sql.And(sql.Eq(sql.Col("email"), "a@b.c"), sql.Eq(sql.Col("id"), 5))),
			want: `SELECT * FROM "users" WHERE (("email" = 'a@b.c') AND ("id" = 5))`,
		},
		{
			name: "in list quotes every string item",
			q:    sql.Select(sql.Star()).From(users).Where(sql.In(sql.Col("name"), "a", "b")),
			want: `SELECT * FROM "users" WHERE ("name" IN ('a', 'b'))`,
		},
		{
			name: "insert inlines each value",
			q:    sql.InsertInto(users).Columns("name", "id").Values("taro", 1),
			want: `INSERT INTO "users" ("name", "id") VALUES ('taro', 1)`,
		},
		{
			name: "update set and where",
			q:    sql.Update(users).Set(sql.Set("name", "taro")).Where(sql.Eq(sql.Col("id"), 1)),
			want: `UPDATE "users" SET "name" = 'taro' WHERE ("id" = 1)`,
		},
		{
			name: "embedded quote is doubled",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), "O'Brien")),
			want: `SELECT * FROM "users" WHERE ("name" = 'O''Brien')`,
		},
		{
			name: "nil renders NULL and bool verbatim",
			q:    sql.Select(sql.Star()).From(users).Where(sql.And(sql.Eq(sql.Col("a"), nil), sql.Eq(sql.Col("b"), true))),
			want: `SELECT * FROM "users" WHERE (("a" = NULL) AND ("b" = true))`,
		},
		{
			name: "named string type quoted explicitly with Quote",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("token"), sql.Val(secret("xyz")).Quote())),
			want: `SELECT * FROM "users" WHERE ("token" = 'xyz')`,
		},
		{
			name: "string-wrapping struct quoted via Quote and String",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("token"), sql.Val(token{"xyz"}).Quote())),
			want: `SELECT * FROM "users" WHERE ("token" = 'xyz')`,
		},
		{
			name: "Quoter is quoted automatically without Quote",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("token"), secure{"xyz"})),
			want: `SELECT * FROM "users" WHERE ("token" = 'xyz')`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sql.CompileInline(sql.PostgreSQLDialect{}, tt.q)
			if err != nil {
				t.Fatalf("CompileInline: %v", err)
			}
			if got != tt.want {
				t.Errorf("CompileInline =\n  %s\nwant\n  %s", got, tt.want)
			}
		})
	}
}

// TestCompileInlineLeavesToSQLParameterized pins that the inline mode is opt-in:
// ToSQL still binds placeholders and returns args.
func TestCompileInlineLeavesToSQLParameterized(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), "taro"))

	text, args, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if want := `SELECT * FROM "users" WHERE ("name" = $1)`; text != want {
		t.Errorf("ToSQL text = %q, want %q", text, want)
	}
	if len(args) != 1 || args[0] != "taro" {
		t.Errorf("ToSQL args = %#v, want [taro]", args)
	}
}

func TestCompileInlineNilQuery(t *testing.T) {
	if _, err := sql.CompileInline(sql.PostgreSQLDialect{}, nil); err == nil {
		t.Fatal("CompileInline(nil) succeeded, want error")
	}
}
