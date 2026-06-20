package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateMaterializedView(t *testing.T) {
	assertRoundTrip(t,
		"CREATE MATERIALIZED VIEW totals AS SELECT id FROM orders WITH NO DATA",
		sql.CreateMaterializedView(sql.Table{Name: sql.NewIdent("totals")},
			sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("orders")})).WithNoData(),
		"CREATE MATERIALIZED VIEW \"totals\" AS\nSELECT \"id\" FROM \"orders\" WITH NO DATA;")
}

func TestDropMaterializedView(t *testing.T) {
	assertRoundTrip(t,
		"DROP MATERIALIZED VIEW IF EXISTS reporting.totals CASCADE",
		sql.DropMaterializedView(sql.Table{Schema: sql.NewIdent("reporting"), Name: sql.NewIdent("totals")}).IfExists().Cascade(),
		`DROP MATERIALIZED VIEW IF EXISTS "reporting"."totals" CASCADE;`)
}

func TestRefreshMaterializedView(t *testing.T) {
	assertRoundTrip(t,
		"REFRESH MATERIALIZED VIEW CONCURRENTLY totals",
		sql.RefreshMaterializedView(sql.Table{Name: sql.NewIdent("totals")}).Concurrently(),
		`REFRESH MATERIALIZED VIEW CONCURRENTLY "totals";`)
}

func TestAlterTypeAddValue(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TYPE mood ADD VALUE IF NOT EXISTS 'excited' AFTER 'happy'",
		sql.AlterTypeAddValue("mood", "excited").IfNotExists().After("happy"),
		`ALTER TYPE "mood" ADD VALUE IF NOT EXISTS 'excited' AFTER 'happy';`)
}

func TestAlterTypeRenameValue(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TYPE mood RENAME VALUE 'ok' TO 'fine'",
		sql.AlterTypeRenameValue("mood", "ok", "fine"),
		`ALTER TYPE "mood" RENAME VALUE 'ok' TO 'fine';`)
}
