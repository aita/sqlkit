package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestEnum covers PostgreSQL's native enum type. The builder's CreateEnum /
// DropType DDL must execute on a real server, and a column of the new type has to
// accept and compare its values. MySQL models enums as an inline column
// constraint rather than a standalone CREATE TYPE, so this PostgreSQL-only path
// skips there.
func TestEnum(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if eng.name != "postgres" {
			t.Skipf("%s has no standalone CREATE TYPE ... AS ENUM", eng.name)
		}
		ctx := context.Background()

		// Build and execute the enum type DDL through sqlkit, then a table that
		// uses it. Cleanup drops the table before the type (the column depends on it).
		_, _ = db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS moods")
		_, _ = db.SQL().ExecContext(ctx, "DROP TYPE IF EXISTS mood")
		if _, err := db.Exec(ctx, sql.CreateEnum("mood", "happy", "sad", "ok")); err != nil {
			t.Fatalf("create enum: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.SQL().ExecContext(context.Background(), "DROP TABLE IF EXISTS moods")
			_, _ = db.Exec(context.Background(), sql.DropType("mood").IfExists())
		})
		if _, err := db.SQL().ExecContext(ctx, "CREATE TABLE moods (id BIGINT PRIMARY KEY, m mood NOT NULL)"); err != nil {
			t.Fatalf("create moods: %v", err)
		}

		for _, lit := range []string{"(1, 'happy')", "(2, 'sad')", "(3, 'happy')"} {
			if _, err := db.SQL().ExecContext(ctx, "INSERT INTO moods (id, m) VALUES "+lit); err != nil {
				t.Fatalf("seed moods: %v", err)
			}
		}

		// Compare the enum column against a value: the two 'happy' rows come back.
		var happy []int64
		if err := db.Select(sql.Col("id")).From(sql.Tbl("moods")).
			Where(sqlkit.Cond(sql.Eq(sql.Col("m"), sql.Val("happy")))).
			OrderBy(sql.Asc(sql.Col("id"))).
			All(ctx).Scan(&happy); err != nil {
			t.Fatalf("enum compare: %v", err)
		}
		if len(happy) != 2 || happy[0] != 1 || happy[1] != 3 {
			t.Fatalf("m = 'happy' ids = %#v, want [1 3]", happy)
		}
	})
}
