package migrate

import (
	"context"
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestOperatorSchemaObjects(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")

	op.CreateSchema("audit")
	op.CreateSequence("order_no", func(s sql.CreateSequenceBuilder) sql.CreateSequenceBuilder {
		return s.Start(1000).Increment(1)
	})
	op.CreateEnum("mood", "happy", "sad")
	op.AddEnumValue("mood", "neutral")
	op.RenameEnumValue("mood", "sad", "blue")
	op.CreateExtension("pg_trgm")
	op.DropSequence("order_no")
	op.DropEnum("mood")
	op.DropExtension("pg_trgm")
	op.DropSchema("audit")

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		`CREATE SCHEMA IF NOT EXISTS "audit";`,
		`CREATE SEQUENCE "public"."order_no" INCREMENT BY 1 START WITH 1000;`,
		`CREATE TYPE "public"."mood" AS ENUM ('happy', 'sad');`,
		`ALTER TYPE "public"."mood" ADD VALUE 'neutral';`,
		`ALTER TYPE "public"."mood" RENAME VALUE 'sad' TO 'blue';`,
		`CREATE EXTENSION IF NOT EXISTS "pg_trgm";`,
		`DROP SEQUENCE IF EXISTS "public"."order_no";`,
		`DROP TYPE IF EXISTS "public"."mood";`,
		`DROP EXTENSION IF EXISTS "pg_trgm";`,
		`DROP SCHEMA IF EXISTS "audit";`,
	}
	assertSQLs(t, cap.sqls, want)
}

func TestOperatorMaterializedViewAndGrant(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")

	body := sql.Select(sql.Raw("1 AS n"))
	op.CreateMaterializedView("nums", body, func(s sql.CreateMaterializedViewBuilder) sql.CreateMaterializedViewBuilder {
		return s.WithNoData()
	})
	op.RefreshMaterializedView("nums")
	op.Grant(sql.Grant("SELECT").On("public.nums").To("readonly"))
	op.Revoke(sql.Revoke("SELECT").On("public.nums").From("readonly"))
	op.DropMaterializedView("nums")

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	want := []string{
		"CREATE MATERIALIZED VIEW \"public\".\"nums\" AS\nSELECT 1 AS n WITH NO DATA;",
		`REFRESH MATERIALIZED VIEW "public"."nums";`,
		`GRANT SELECT ON TABLE "public"."nums" TO "readonly";`,
		`REVOKE SELECT ON TABLE "public"."nums" FROM "readonly";`,
		`DROP MATERIALIZED VIEW IF EXISTS "public"."nums";`,
	}
	assertSQLs(t, cap.sqls, want)
}

// TestOperatorFunctions covers CreateFunction (with a configure callback) and
// DropFunction (with argument types), the trigger-function escape hatches.
func TestOperatorFunctions(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")

	op.CreateFunction("bump", func(s sql.CreateFunctionBuilder) sql.CreateFunctionBuilder {
		return s.OrReplace().Returns("trigger").Language("plpgsql").Body("BEGIN RETURN NEW; END;")
	})
	op.DropFunction("bump")
	op.DropFunction("overloaded", func(s sql.DropFunctionBuilder) sql.DropFunctionBuilder {
		return s.ArgTypes("int", "text")
	})

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	if len(cap.sqls) != 3 {
		t.Fatalf("got %d statements, want 3:\n%v", len(cap.sqls), cap.sqls)
	}
	if !contains(cap.sqls[0], "CREATE OR REPLACE FUNCTION") || !contains(cap.sqls[0], `"public"."bump"`) {
		t.Errorf("CreateFunction rendered %q", cap.sqls[0])
	}
	if !contains(cap.sqls[1], "DROP FUNCTION IF EXISTS") {
		t.Errorf("DropFunction rendered %q", cap.sqls[1])
	}
	if !contains(cap.sqls[2], "int") || !contains(cap.sqls[2], "text") {
		t.Errorf("DropFunction with arg types rendered %q", cap.sqls[2])
	}
}

// TestOperatorExtensionWithSchema covers the CreateExtension configure callback
// (WITH SCHEMA) and the unqualified-name path of qualified() via an empty schema.
func TestOperatorExtensionWithSchema(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "") // empty schema: names unqualified

	op.CreateExtension("pg_trgm", func(s sql.CreateExtensionBuilder) sql.CreateExtensionBuilder {
		return s.Schema("public")
	})
	op.CreateEnum("mood", "happy") // qualified() with empty schema -> bare name

	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	if !contains(cap.sqls[0], "SCHEMA") {
		t.Errorf("CreateExtension WITH SCHEMA rendered %q", cap.sqls[0])
	}
	if contains(cap.sqls[1], ".") {
		t.Errorf("CreateEnum with empty schema should be unqualified, got %q", cap.sqls[1])
	}
}

// TestOperatorRefreshConcurrently covers the RefreshMaterializedView configure
// callback.
func TestOperatorRefreshConcurrently(t *testing.T) {
	cap := newCapture()
	op := newOperator(context.Background(), cap, "public")
	op.RefreshMaterializedView("nums", func(s sql.RefreshMaterializedViewBuilder) sql.RefreshMaterializedViewBuilder {
		return s.Concurrently()
	})
	if err := op.Err(); err != nil {
		t.Fatalf("Err = %v", err)
	}
	if len(cap.sqls) != 1 || !contains(cap.sqls[0], "CONCURRENTLY") {
		t.Errorf("RefreshMaterializedView Concurrently rendered %v", cap.sqls)
	}
}
