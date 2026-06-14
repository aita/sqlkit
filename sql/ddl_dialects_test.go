package sql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

// TestSQLiteDDL covers SQLite's DDL rendering, which differs from PostgreSQL on
// the statements SQLite cannot express: dropping a single foreign key requires
// rebuilding the table (so it is a no-op here) and SQLite has no COMMENT syntax.
func TestSQLiteDDL(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	posts := sql.Table{Schema: "public", Name: "posts"}

	if got, want := mustDDL(t, sql.DropTable(users).IfExists(), "sqlite"),
		`DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropTable(sqlite) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.DropIndex("uq_users_email", users), "sqlite"),
		`DROP INDEX IF EXISTS "public"."uq_users_email";`; got != want {
		t.Errorf("DropIndex(sqlite) = %q, want %q", got, want)
	}
	// SQLite cannot drop a single FK in place: the statement renders empty.
	if got := mustDDL(t, sql.DropForeignKey(posts, "fk_posts_user_id"), "sqlite"); got != "" {
		t.Errorf("DropForeignKey(sqlite) = %q, want empty (unsupported)", got)
	}
	// SQLite has no COMMENT syntax, inline or standalone.
	if got := mustDDL(t, sql.CommentOnTable(users, "hi"), "sqlite"); got != "" {
		t.Errorf("CommentOnTable(sqlite) = %q, want empty", got)
	}
	if got := mustDDL(t, sql.CommentOnColumn(users, "email", "addr"), "sqlite"); got != "" {
		t.Errorf("CommentOnColumn(sqlite) = %q, want empty", got)
	}
}

// TestStandardSQLDDL covers the ANSI/standard dialect's DDL rendering: it drops
// a foreign key with the standard DROP CONSTRAINT and an index without the
// vendor-specific IF EXISTS / ON table clauses, and has no COMMENT syntax.
func TestStandardSQLDDL(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	posts := sql.Table{Schema: "public", Name: "posts"}

	if got, want := mustDDL(t, sql.DropTable(users).IfExists(), "standard"),
		`DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropTable(standard) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.DropIndex("uq_users_email", users), "standard"),
		`DROP INDEX "public"."uq_users_email";`; got != want {
		t.Errorf("DropIndex(standard) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.DropForeignKey(posts, "fk_posts_user_id"), "standard"),
		`ALTER TABLE "public"."posts" DROP CONSTRAINT "fk_posts_user_id";`; got != want {
		t.Errorf("DropForeignKey(standard) = %q, want %q", got, want)
	}
	if got := mustDDL(t, sql.CommentOnTable(users, "hi"), "standard"); got != "" {
		t.Errorf("CommentOnTable(standard) = %q, want empty", got)
	}
	if got := mustDDL(t, sql.CommentOnColumn(users, "email", "addr"), "standard"); got != "" {
		t.Errorf("CommentOnColumn(standard) = %q, want empty", got)
	}
}

// TestMySQLCommentStatementsAreInline covers MySQL's standalone comment
// statements rendering empty: MySQL carries comments inline in the column/table
// definition, so CommentOnTable/CommentOnColumn produce no separate statement.
func TestMySQLCommentStatementsAreInline(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	if got := mustDDL(t, sql.CommentOnTable(users, "hi"), "mysql"); got != "" {
		t.Errorf("CommentOnTable(mysql) = %q, want empty (comments are inline)", got)
	}
	if got := mustDDL(t, sql.CommentOnColumn(users, "email", "addr"), "mysql"); got != "" {
		t.Errorf("CommentOnColumn(mysql) = %q, want empty (comments are inline)", got)
	}
}
