package integration

import (
	"context"
	"strconv"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
	"github.com/aita/sqlkit/sql"
)

// These tests run WITH (common table expression) queries against the real
// engines. The compiler renders the WITH prefix and, for recursive CTEs, the
// RECURSIVE keyword; the engines then have to accept and evaluate them. Both
// PostgreSQL and MySQL 8 support WITH and WITH RECURSIVE, so the same query runs
// on both.

func TestCTE(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// A non-recursive CTE: name the published posts, then select their titles
		// back out of the named query. The result must match a direct filter.
		published := db.Select(blogdb.Posts.Title).From(blogdb.Posts).
			Where(blogdb.Posts.Published.Eq(true)).
			CTE("published")
		var titles []string
		if err := db.Select(published.Col("title")).From(published).
			WithCTE(published).
			OrderBy(sql.Asc(sql.Col("title"))).
			All(ctx).Scan(&titles); err != nil {
			t.Fatalf("with cte: %v", err)
		}
		if len(titles) != 2 || titles[0] != "Bob's first" || titles[1] != "Hello" {
			t.Fatalf("cte titles = %#v, want the two published titles", titles)
		}
	})
}

func TestRecursiveCTE(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		// A self-referencing category tree, created and torn down by the test so it
		// does not need a place in the shared catalog. BIGINT/VARCHAR render the
		// same on both engines, and explicit ids avoid the serial/auto-increment
		// split, so one DDL string works for PostgreSQL and MySQL alike.
		createCategoryTree(t, db)
		// root(1) -> child(2) -> grandchild(3); other(4) is a separate root.
		for _, c := range []struct {
			id, parent int64
			name       string
			root       bool
		}{
			{1, 0, "root", true},
			{2, 1, "child", false},
			{3, 2, "grandchild", false},
			{4, 0, "other", true},
		} {
			parent := "NULL"
			args := []any{c.id, c.name}
			if !c.root {
				parent = placeholder(eng, 3)
				args = []any{c.id, c.name, c.parent}
			}
			stmt := "INSERT INTO categories (id, name, parent_id) VALUES (" +
				placeholder(eng, 1) + ", " + placeholder(eng, 2) + ", " + parent + ")"
			if _, err := db.SQL().ExecContext(ctx, stmt, args...); err != nil {
				t.Fatalf("seed categories: %v", err)
			}
		}

		// WITH RECURSIVE descends from the root (id 1) through parent_id, so the
		// anchor selects the root and the recursive term joins children back to the
		// growing "tree". The subtree under root 1 is {1, 2, 3}; other(4) is excluded.
		base := db.Select(sql.Col("categories.id"), sql.Col("categories.name")).
			From(sql.Tbl("categories")).
			Where(sqlkit.Cond(sql.Eq(sql.Col("categories.id"), sql.Int(1))))
		step := db.Select(sql.Col("categories.id"), sql.Col("categories.name")).
			From(sql.Tbl("categories")).
			Join(sql.Tbl("tree"), sqlkit.Cond(sql.Eq(sql.Col("categories.parent_id"), sql.Col("tree.id"))))
		tree := base.UnionAll(step).CTE("tree", sql.Recursive(), sql.Columns("id", "name"))

		type node struct {
			ID   int64
			Name string
		}
		var got []node
		if err := db.Select(tree.Col("id"), tree.Col("name")).From(tree).
			WithCTE(tree).
			OrderBy(sql.Asc(sql.Col("id"))).
			All(ctx).Scan(&got); err != nil {
			t.Fatalf("recursive cte: %v", err)
		}
		want := []node{{1, "root"}, {2, "child"}, {3, "grandchild"}}
		if len(got) != len(want) {
			t.Fatalf("recursive descent = %#v, want %#v", got, want)
		}
		for i, n := range got {
			if n != want[i] {
				t.Fatalf("recursive descent = %#v, want %#v", got, want)
			}
		}
	})
}

// createCategoryTree creates a throwaway self-referencing categories table and
// drops it on cleanup. The applySchema run before each subtest only manages the
// shared catalog, so this side table is the test's own responsibility.
func createCategoryTree(t *testing.T, db *sqlkit.Database) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS categories"); err != nil {
		t.Fatalf("drop categories: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx,
		"CREATE TABLE categories (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL, parent_id BIGINT)"); err != nil {
		t.Fatalf("create categories: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.SQL().ExecContext(context.Background(), "DROP TABLE IF EXISTS categories")
	})
}

// placeholder renders the engine's positional placeholder for argument n
// (PostgreSQL uses $1, $2, …; MySQL uses ?).
func placeholder(eng engine, n int) string {
	if eng.name == "postgres" {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}
