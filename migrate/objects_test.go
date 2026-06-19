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
