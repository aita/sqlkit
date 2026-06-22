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

// TestOperatorColumnAndConstraintOps exercises the alter-table operations that
// the autogenerator and hand-written migrations emit: full column restatement,
// primary-key add/drop, unique index, FK with referential actions, named
// constraint drops, and CHECK constraints. Each must render valid PostgreSQL.
func TestOperatorColumnAndConstraintOps(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")

	op.ModifyColumn("users", decl.Column("score", decl.BigInt, decl.Nullable()))
	op.RenameTable("users", "members")
	op.AddPrimaryKey("members", "id")
	op.DropPrimaryKey("members")
	op.CreateUniqueIndex("uq_members_email", "members", "email")
	op.CreateForeignKey("fk_posts_user", "posts", []string{"user_id"}, "members", []string{"id"},
		OnDelete("CASCADE"), OnUpdate("RESTRICT"))
	op.DropConstraint("ck_members_age", "members")
	op.AddCheck("ck_members_age", "members", "age >= 0")

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		`ALTER TABLE "public"."users" ALTER COLUMN "score" TYPE bigint, ALTER COLUMN "score" DROP NOT NULL, ALTER COLUMN "score" DROP DEFAULT;`,
		`ALTER TABLE "public"."users" RENAME TO "members";`,
		`ALTER TABLE "public"."members" ADD PRIMARY KEY ("id");`,
		`ALTER TABLE "public"."members" DROP CONSTRAINT "members_pkey";`,
		`CREATE UNIQUE INDEX "uq_members_email" ON "public"."members" ("email");`,
		`ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user" FOREIGN KEY ("user_id") REFERENCES "public"."members" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;`,
		`ALTER TABLE "public"."members" DROP CONSTRAINT "ck_members_age";`,
		`ALTER TABLE "public"."members" ADD CONSTRAINT "ck_members_age" CHECK (age >= 0);`,
	}
	assertSQLs(t, cap.sqls, want)
}

// TestOperatorAlterColumnDefaults covers the SetDefault/DropDefault changes and
// the AlterColumn no-op guard (zero changes emits nothing).
func TestOperatorAlterColumnDefaults(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")

	op.AlterColumn("users", "status", SetDefault("'active'"))
	op.AlterColumn("users", "status", DropDefault())
	op.AlterColumn("users", "status", SetNullable(false))
	op.AlterColumn("users", "status") // no changes: must emit nothing

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		`ALTER TABLE "public"."users" ALTER COLUMN "status" SET DEFAULT 'active';`,
		`ALTER TABLE "public"."users" ALTER COLUMN "status" DROP DEFAULT;`,
		`ALTER TABLE "public"."users" ALTER COLUMN "status" SET NOT NULL;`,
	}
	assertSQLs(t, cap.sqls, want)
}

// TestOperatorExecuteAndExec covers the two escape hatches: Execute for a raw
// DDL statement and Exec for a data-migration query.
func TestOperatorExecuteAndExec(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")

	op.Execute(sql.DropTable(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("legacy")}).IfExists())
	op.Exec(sql.InsertInto(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}).
		Columns("name").Values("alice"))

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		`DROP TABLE IF EXISTS "public"."legacy";`,
		`INSERT INTO "public"."users" ("name") VALUES ($1)`,
	}
	assertSQLs(t, cap.sqls, want)
}

// TestOperatorExecError checks the data-migration Exec path surfaces a driver
// error and short-circuits the remaining operations.
func TestOperatorExecError(t *testing.T) {
	cap := &captureExec{dialect: sql.PostgreSQLDialect{}, err: errors.New("boom")}
	op := newOperator(context.Background(), cap, "public")
	op.Exec(sql.InsertInto(sql.Table{Name: sql.NewIdent("t")}).Columns("c").Values(1))
	op.DropTable("after") // must be skipped
	if op.Err() == nil {
		t.Fatal("expected error from Exec")
	}
	if len(cap.sqls) != 0 {
		t.Errorf("recorded %d statements, want 0", len(cap.sqls))
	}
}

// TestOperatorCreateViewWithColumns covers the ViewColumns option.
func TestOperatorCreateViewWithColumns(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")
	body := sql.Select(sql.Col("id"), sql.Col("email")).
		From(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")})
	op.CreateView("user_emails", body, ViewColumns("uid", "addr"))
	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	if len(cap.sqls) != 1 || !contains(cap.sqls[0], `("uid", "addr")`) {
		t.Errorf("CreateView with columns rendered %v", cap.sqls)
	}
}

// TestOperatorDeclErrors checks the operations that build a column/table from a
// decl element surface a decl validation error (rather than emit bad DDL) and
// short-circuit. A decl.Index element declares no column, so AddColumn and
// ModifyColumn reject it; a primary-key-nullable column makes CreateTable's
// BuildTable fail.
func TestOperatorDeclErrors(t *testing.T) {
	t.Run("AddColumn", func(t *testing.T) {
		cap := newCapture()
		op := newOperator(context.Background(), cap, "public")
		op.AddColumn("t", decl.Index("a")) // not a column
		if op.Err() == nil {
			t.Fatal("expected a decl error from AddColumn")
		}
		if len(cap.sqls) != 0 {
			t.Errorf("AddColumn emitted DDL despite the decl error: %v", cap.sqls)
		}
	})
	t.Run("ModifyColumn", func(t *testing.T) {
		cap := newCapture()
		op := newOperator(context.Background(), cap, "public")
		op.ModifyColumn("t", decl.Index("a"))
		if op.Err() == nil {
			t.Fatal("expected a decl error from ModifyColumn")
		}
		if len(cap.sqls) != 0 {
			t.Errorf("ModifyColumn emitted DDL despite the decl error: %v", cap.sqls)
		}
	})
	t.Run("CreateTable", func(t *testing.T) {
		cap := newCapture()
		op := newOperator(context.Background(), cap, "public")
		// A primary-key column declared Nullable is invalid.
		op.CreateTable("t", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Nullable()))
		if op.Err() == nil {
			t.Fatal("expected a decl error from CreateTable")
		}
		if len(cap.sqls) != 0 {
			t.Errorf("CreateTable emitted DDL despite the decl error: %v", cap.sqls)
		}
	})
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
