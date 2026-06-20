package migrate

import (
	"context"
	stdsql "database/sql"
	"errors"
	"testing"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/sql"
)

// captureExec is a fake Execer that compiles each statement and records its SQL.
type captureExec struct {
	dialect sql.Dialect
	sqls    []string
	err     error
}

func (c *captureExec) Exec(_ context.Context, q sql.Query) (stdsql.Result, error) {
	if c.err != nil {
		return nil, c.err
	}
	text, _, err := q.ToSQL(c.dialect)
	if err != nil {
		return nil, err
	}
	if text == "" { // mirror Database/Session.Exec: empty SQL is a no-op
		return nil, nil
	}
	c.sqls = append(c.sqls, text)
	return nil, nil
}

func newCapture() *captureExec { return &captureExec{dialect: sql.PostgreSQLDialect{}} }

func TestOperatorCreateTable(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")
	op.CreateTable("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255), decl.Unique()),
	)
	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		"CREATE TABLE \"public\".\"users\" (\n" +
			"    \"id\" bigserial,\n" +
			"    \"email\" varchar(255) NOT NULL,\n" +
			"    PRIMARY KEY (\"id\")\n" +
			");",
		`CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");`,
	}
	assertSQLs(t, cap.sqls, want)
}

func TestOperatorAlterations(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")
	op.AddColumn("users", decl.Column("name", decl.Text, decl.Nullable()))
	op.DropColumn("users", "age")
	op.RenameColumn("users", "name", "full_name")
	op.AlterColumn("users", "score", TypeTo(decl.BigInt), SetNullable(true))
	op.CreateForeignKey("fk_posts_user_id", "posts", []string{"user_id"}, "users", []string{"id"})
	op.DropForeignKey("fk_posts_user_id", "posts")
	op.CreateIndex("idx_posts_user_id", "posts", "user_id")
	op.DropIndex("idx_posts_user_id", "posts")
	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		`ALTER TABLE "public"."users" ADD COLUMN "name" text;`,
		`ALTER TABLE "public"."users" DROP COLUMN "age";`,
		`ALTER TABLE "public"."users" RENAME COLUMN "name" TO "full_name";`,
		`ALTER TABLE "public"."users" ALTER COLUMN "score" TYPE bigint, ALTER COLUMN "score" DROP NOT NULL;`,
		`ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");`,
		`ALTER TABLE "public"."posts" DROP CONSTRAINT IF EXISTS "fk_posts_user_id";`,
		`CREATE INDEX "idx_posts_user_id" ON "public"."posts" ("user_id");`,
		`DROP INDEX IF EXISTS "public"."idx_posts_user_id";`,
	}
	assertSQLs(t, cap.sqls, want)
}

func TestOperatorCreateTemporaryTable(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")
	op.CreateTemporaryTable("scratch",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		"CREATE TEMPORARY TABLE \"public\".\"scratch\" (\n" +
			"    \"id\" bigserial,\n" +
			"    PRIMARY KEY (\"id\")\n" +
			");",
	}
	assertSQLs(t, cap.sqls, want)
}

func TestOperatorCreateAndDropView(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")
	body := sql.Select(sql.Col("id"), sql.Col("email")).
		From(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}).
		Where(sql.Raw("active"))
	op.CreateView("active_users", body, OrReplace())
	op.DropView("active_users")
	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		"CREATE OR REPLACE VIEW \"public\".\"active_users\" AS\n" +
			`SELECT "id", "email" FROM "public"."users" WHERE active;`,
		`DROP VIEW IF EXISTS "public"."active_users";`,
	}
	assertSQLs(t, cap.sqls, want)
}

func TestOperatorUnqualified(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "") // no schema
	op.DropTable("widgets")
	assertSQLs(t, cap.sqls, []string{`DROP TABLE IF EXISTS "widgets";`})
}

func TestOperatorStopsAfterError(t *testing.T) {
	cap := &captureExec{dialect: sql.PostgreSQLDialect{}, err: errors.New("boom")}
	op := newOperator(context.Background(), cap, "public")
	op.DropTable("a")
	op.DropTable("b") // must be a no-op after the first failure
	if op.Err() == nil {
		t.Fatal("expected error")
	}
	if len(cap.sqls) != 0 {
		t.Errorf("recorded %d statements, want 0", len(cap.sqls))
	}
}

func assertSQLs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d statements, want %d:\ngot  %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d:\ngot  %s\nwant %s", i, got[i], want[i])
		}
	}
}
