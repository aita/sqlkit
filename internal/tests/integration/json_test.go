package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestJSONNegativeIndex checks that a negative JSON array index counts from
// the end on every engine: PostgreSQL's ->> -1, MySQL's $[last] path.
func TestJSONNegativeIndex(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		doc := sql.Raw(`CAST('[1, 2, 5]' AS JSON)`)
		if eng.name == "postgres" {
			doc = sql.Raw(`'[1, 2, 5]'::jsonb`)
		}
		for idx, want := range map[int]string{-1: "5", -2: "2", 0: "1"} {
			text, args, err := sql.Select(sql.As(sql.JSONGetText(doc, idx), "v")).ToSQL(eng.dialect)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err := db.SQL().QueryRowContext(context.Background(), text, args...).Scan(&got); err != nil {
				t.Fatalf("%s: %v", text, err)
			}
			if got != want {
				t.Errorf("%s = %q, want %q", text, got, want)
			}
		}
	})
}
