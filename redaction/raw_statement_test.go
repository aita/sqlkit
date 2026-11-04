package redaction_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// A raw statement (RawSQL, or anything a parser frontend keeps raw) that names
// a sensitive column must not pass through with its literals intact: the
// package's documented fail-safe masks every string literal and string bind
// argument of such a statement, as it already did for a Raw(...) fragment.
func TestRawStatementMentioningSensitiveColumnIsMasked(t *testing.T) {
	r := redaction.New(redaction.Columns("users.ssn"))
	cases := []sql.Query{
		sql.RawSQL("SELECT * FROM users WHERE ssn = 'SECRET'"),
		sql.RawSQL("SELECT count(*) FILTER (WHERE u.ssn = 'SECRET') FROM users u"),
		sql.RawSQL("INSERT INTO public.users (id, ssn) VALUES (1, 'SECRET')"),
		sql.RawSQL("UPDATE users SET ssn = $1", "SECRET"),
	}
	for _, q := range cases {
		text, args, err := r.Apply(q).ToSQL(sql.PostgreSQLDialect{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "SECRET") {
			t.Errorf("literal leaked: %s", text)
		}
		for _, a := range args {
			if a == "SECRET" {
				t.Errorf("bind argument leaked: %v", args)
			}
		}
	}
}

// A raw statement that names no sensitive column is left untouched.
func TestRawStatementWithoutSensitiveColumnUntouched(t *testing.T) {
	r := redaction.New(redaction.Columns("users.ssn"))
	const q = "SELECT * FROM users WHERE name = 'bob'"
	text, _, _ := r.Apply(sql.RawSQL(q)).ToSQL(sql.PostgreSQLDialect{})
	if text != q {
		t.Fatalf("got %q, want the statement unchanged", text)
	}
}
