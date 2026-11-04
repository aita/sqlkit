package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestArray covers PostgreSQL's array support, which MySQL lacks entirely: the
// ARRAY[...] constructor (ArrayExpression) and the "x = ANY(array)" quantified
// comparison (QuantifiedArrayExpression). The compiler renders these only for a
// dialect implementing ArrayDialect, so the test runs on PostgreSQL and skips
// MySQL, where the same expressions are rejected at compile time.
func TestArray(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if eng.name != "postgres" {
			t.Skipf("%s has no array type", eng.name)
		}
		ctx := context.Background()
		createArrayTable(t, db)

		// Seed rows whose tags overlap, so a membership test selects a known subset.
		for _, lit := range []string{"(1, 'a', '{10,20}')", "(2, 'b', '{20,30}')", "(3, 'c', '{30,40}')"} {
			if _, err := db.SQL().ExecContext(ctx, "INSERT INTO arr_test (id, name, tags) VALUES "+lit); err != nil {
				t.Fatalf("seed arr_test: %v", err)
			}
		}

		// Membership against a column array: 20 = ANY(tags) matches rows 1 and 2.
		var member []int64
		if err := db.Select(sql.Col("id")).From(sql.Tbl("arr_test")).
			Where(sqlkit.Cond(sql.AnyArray(sql.Int(20), "=", sql.Col("tags")))).
			OrderBy(sql.Asc(sql.Col("id"))).
			All(ctx).Scan(&member); err != nil {
			t.Fatalf("= ANY(tags): %v", err)
		}
		if len(member) != 2 || member[0] != 1 || member[1] != 2 {
			t.Fatalf("20 = ANY(tags) ids = %#v, want [1 2]", member)
		}

		// ARRAY[...] constructor on the right: name = ANY(ARRAY['a','c']) matches
		// rows 1 and 3. A text array sidesteps the element-type inference that an
		// untyped integer ARRAY[...] of bind params would trip over.
		var ctor []int64
		if err := db.Select(sql.Col("id")).From(sql.Tbl("arr_test")).
			Where(sqlkit.Cond(sql.AnyArray(sql.Col("name"), "=", sql.Array("a", "c")))).
			OrderBy(sql.Asc(sql.Col("id"))).
			All(ctx).Scan(&ctor); err != nil {
			t.Fatalf("= ANY(ARRAY[...]): %v", err)
		}
		if len(ctor) != 2 || ctor[0] != 1 || ctor[1] != 3 {
			t.Fatalf("name = ANY(ARRAY['a','c']) ids = %#v, want [1 3]", ctor)
		}
	})
}

func createArrayTable(t *testing.T, db *sqlkit.Database) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS arr_test"); err != nil {
		t.Fatalf("drop arr_test: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, "CREATE TABLE arr_test (id BIGINT PRIMARY KEY, name TEXT NOT NULL, tags INT[])"); err != nil {
		t.Fatalf("create arr_test: %v", err)
	}
	t.Cleanup(func() { _, _ = db.SQL().ExecContext(context.Background(), "DROP TABLE IF EXISTS arr_test") })
}
