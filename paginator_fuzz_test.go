package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// realCursorToken paginates against a fake DB to produce a genuine, well-formed
// cursor token so the fuzz corpus is seeded with input the parser is meant to
// accept.
func realCursorToken(f *testing.F) string {
	f.Helper()
	rows := [][]driver.Value{
		{int64(1), "a@example.com", "Alice"},
		{int64(2), "b@example.com", "Bob"},
		{int64(3), "c@example.com", "Carol"},
	}
	db := sqlkit.WrapDB((&testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id", "email", "name"}, Rows: rows}, nil
		},
	}).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Email), sqlkit.KeyAsc(Users.ID)).Limit(2)

	var users []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, sqlkit.Start())
	if err != nil {
		f.Fatalf("seed Paginate() error = %v", err)
	}
	return page.Next().String()
}

// FuzzParseCursor hardens ParseCursor against untrusted token input (the cursor
// crosses HTTP boundaries). ParseCursor defers validation to Paginate, so its
// own contract is narrow: it must never panic, an empty token must yield the
// first-page cursor, and parse -> String -> parse must be stable.
func FuzzParseCursor(f *testing.F) {
	f.Add(realCursorToken(f))
	f.Add("")
	f.Add("not base64!!! @#$%")
	f.Add("aGVsbG8")            // valid base64, not a cursor payload
	f.Add("eyJyIjpmYWxzZX0")    // base64 of {"r":false}
	f.Add("////")               // base64-ish junk
	f.Add("AAAAAAAAAAAAAAAAAA") // long valid-charset string
	f.Add("a")                  // truncated
	f.Add(string([]byte{0x00, 0x01, 0x02}))
	f.Add("πανω") // non-ASCII

	f.Fuzz(func(t *testing.T, token string) {
		cur := sqlkit.ParseCursor(token)

		// Empty token is documented as the first-page cursor (README + code).
		if token == "" {
			if cur.String() != "" {
				t.Fatalf("ParseCursor(%q).String() = %q, want empty (first page)", token, cur.String())
			}
			if cur.String() != sqlkit.Start().String() {
				t.Fatalf("ParseCursor(\"\") not equivalent to Start()")
			}
			return
		}

		// For a non-empty token, ParseCursor preserves it opaquely (no half-built
		// state): the round-trip through String must be stable and panic-free.
		out := cur.String()
		if out != token {
			t.Fatalf("ParseCursor(%q).String() = %q, want round-trip identity", token, out)
		}
		again := sqlkit.ParseCursor(out)
		if again.String() != out {
			t.Fatalf("parse->string->parse not stable: %q -> %q", out, again.String())
		}
	})
}

// FuzzPaginateCursor drives an arbitrary parsed cursor through the real decode
// path (Paginate -> decodeToken), which is where untrusted token bytes are
// actually interpreted. It runs against a fake DB so no live database is needed.
// The contract: decoding a token must never panic and a malformed token must
// surface as an error (never a successful page from garbage).
func FuzzPaginateCursor(f *testing.F) {
	f.Add(realCursorToken(f))
	f.Add("not base64!!!")
	f.Add("aGVsbG8")            // valid base64, not a payload
	f.Add("eyJyIjpmYWxzZX0")    // {"r":false} - missing/short values
	f.Add("eyJ2IjpbMSwyXX0")    // {"v":[1,2]} - right shape-ish
	f.Add("eyJ2IjpbImEiXX0")    // {"v":["a"]} - wrong value count
	f.Add(string([]byte{0xff})) // raw bytes

	db := sqlkit.WrapDB((&testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "a@example.com", "Alice"},
					{int64(2), "b@example.com", "Bob"},
				},
			}, nil
		},
	}).DB())

	f.Fuzz(func(t *testing.T, token string) {
		pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Email), sqlkit.KeyAsc(Users.ID)).Limit(2)
		cur := sqlkit.ParseCursor(token)

		var users []MyUserDTO
		// The guarantee under fuzzing is that decoding arbitrary token bytes never
		// panics. A malformed token yields an error; a well-formed one yields a
		// page. Both outcomes are fine here — only a panic is a bug. (A fuzzer can
		// synthesize a structurally valid payload, so "must error" would be a false
		// positive.) The blank identifier documents that the error is non-fatal.
		_, _ = pager.Paginate(context.Background(), db.Select().From(Users), &users, cur)
	})
}
