package migrate_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sqldef"
)

// appliedHistory returns a QueryFunc that reports the given revisions as applied
// (the SELECT history against schema_migrations, all "up" events), and serves
// empty for anything else.
func appliedHistory(revisions ...string) func(string, []any) (sqltest.Result, error) {
	rows := make([][]driver.Value, len(revisions))
	for i, rev := range revisions {
		rows[i] = []driver.Value{rev, "m", "up", time.Unix(0, 0).UTC()}
	}
	return func(q string, _ []any) (sqltest.Result, error) {
		if strings.Contains(q, "schema_migrations") && strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
			return sqltest.Result{Columns: []string{"revision", "name", "direction", "applied_at"}, Rows: rows}, nil
		}
		return sqltest.Result{}, nil
	}
}

func sampleMigrations() []migrate.Migration {
	return []migrate.Migration{
		{
			Revision:     "0002",
			DownRevision: "0001",
			Name:         "create_posts",
			Up: func(op *migrate.Operator) {
				op.CreateTable("posts", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
			},
			Down: func(op *migrate.Operator) { op.DropTable("posts") },
		},
		{
			Revision:     "0001",
			DownRevision: "",
			Name:         "create_tags",
			Up: func(op *migrate.Operator) {
				op.CreateTable("tags", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
			},
			Down: func(op *migrate.Operator) { op.DropTable("tags") },
		},
	}
}

func TestMigratorUpAppliesInTopologicalOrder(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, sampleMigrations())

	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	execs := fake.Execs()
	var got []string
	for _, e := range execs {
		got = append(got, firstLine(e.SQL))
	}
	want := []string{
		`CREATE TABLE IF NOT EXISTS "schema_migrations" (`,
		`CREATE TABLE "tags" (`, // 0001 (root) first
		`INSERT INTO "schema_migrations" ("revision", "name", "direction") VALUES ($1, $2, $3)`,
		`CREATE TABLE "posts" (`, // 0002 (child) second
		`INSERT INTO "schema_migrations" ("revision", "name", "direction") VALUES ($1, $2, $3)`,
	}
	if !equalStrings(got, want) {
		t.Errorf("exec statements:\ngot  %v\nwant %v", got, want)
	}

	if fake.Begins() != 2 || fake.Commits() != 2 {
		t.Errorf("begins=%d commits=%d, want 2 and 2", fake.Begins(), fake.Commits())
	}

	if rev := execs[2].Args[0]; rev != "0001" {
		t.Errorf("first recorded revision = %v, want 0001", rev)
	}
	if rev := execs[4].Args[0]; rev != "0002" {
		t.Errorf("second recorded revision = %v, want 0002", rev)
	}
}

func TestMigratorUpSkipsApplied(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory("0001", "0002")} // both applied
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, sampleMigrations())

	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	for _, e := range fake.Execs() {
		if strings.Contains(e.SQL, `"tags"`) || strings.Contains(e.SQL, `"posts"`) || strings.HasPrefix(e.SQL, "INSERT") {
			t.Errorf("unexpected exec for applied migration: %q", e.SQL)
		}
	}
	if fake.Begins() != 0 {
		t.Errorf("begins=%d, want 0 (nothing to apply)", fake.Begins())
	}
}

func TestMigratorDownRollsBackLast(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory("0001", "0002")}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, sampleMigrations())

	if err := m.Down(context.Background()); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	var got []string
	for _, e := range fake.Execs() {
		got = append(got, firstLine(e.SQL))
	}
	want := []string{
		`CREATE TABLE IF NOT EXISTS "schema_migrations" (`,
		`DROP TABLE IF EXISTS "posts";`, // most recently applied (0002) rolled back
		`INSERT INTO "schema_migrations" ("revision", "name", "direction") VALUES ($1, $2, $3)`,
	}
	if !equalStrings(got, want) {
		t.Errorf("exec statements:\ngot  %v\nwant %v", got, want)
	}
	if dir := fake.Execs()[2].Args[2]; dir != "down" {
		t.Errorf("recorded direction = %v, want down", dir)
	}
	if rev := fake.Execs()[2].Args[0]; rev != "0002" {
		t.Errorf("rolled back revision = %v, want 0002", rev)
	}
}

func TestMigratorHistory(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory("0001", "0002")}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, sampleMigrations())

	events, err := m.History(context.Background())
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("history len = %d, want 2", len(events))
	}
	if events[0].Revision != "0001" || events[0].Direction != migrate.DirectionUp {
		t.Errorf("events[0] = %+v, want revision 0001 up", events[0])
	}
}

func TestMigratorStatus(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory("0001")}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, sampleMigrations())

	status, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(status) != 2 {
		t.Fatalf("status len = %d, want 2", len(status))
	}
	if !(status[0].Revision == "0001" && status[0].Applied) {
		t.Errorf("status[0] = %+v, want revision 0001 applied", status[0])
	}
	if !(status[1].Revision == "0002" && !status[1].Applied) {
		t.Errorf("status[1] = %+v, want revision 0002 pending", status[1])
	}
}

func TestMigratorDuplicateRevisionRejected(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, []migrate.Migration{
		{Revision: "x", Name: "a"},
		{Revision: "x", Name: "b"},
	})
	if err := m.Up(context.Background()); err == nil {
		t.Error("expected duplicate-revision error")
	}
}

func TestMigratorDanglingDownRevisionRejected(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, []migrate.Migration{
		{Revision: "0002", DownRevision: "missing", Name: "b"},
	})
	if err := m.Up(context.Background()); err == nil {
		t.Error("expected dangling down-revision error")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestHistoryTableMySQL checks the tracking table renders types MySQL accepts:
// a bigint AUTO_INCREMENT key (not the PostgreSQL-only "int8") and a
// CURRENT_TIMESTAMP default.
func TestHistoryTableMySQL(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))
	m := migrate.New(db, sampleMigrations())
	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var create string
	for _, e := range fake.Execs() {
		if strings.Contains(e.SQL, "CREATE TABLE") && strings.Contains(e.SQL, "schema_migrations") {
			create = e.SQL
			break
		}
	}
	if create == "" {
		t.Fatal("no CREATE TABLE for schema_migrations recorded")
	}
	for _, want := range []string{"bigint", "AUTO_INCREMENT", "CURRENT_TIMESTAMP"} {
		if !strings.Contains(create, want) {
			t.Errorf("MySQL history DDL missing %q:\n%s", want, create)
		}
	}
	if strings.Contains(create, "int8") {
		t.Errorf("MySQL history DDL uses int8, which MySQL does not know:\n%s", create)
	}
}

// TestDowngradeIrreversible checks that rolling back a migration with no Down
// fails, online and offline, instead of recording a phantom rollback.
func TestDowngradeIrreversible(t *testing.T) {
	migs := []migrate.Migration{
		{Revision: "0001", Name: "no_down", Up: func(*migrate.Operator) {}, Down: nil},
	}
	fake := &sqltest.Fake{QueryFunc: appliedHistory("0001")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs)

	if err := m.Downgrade(context.Background(), "base"); err == nil {
		t.Error("online: expected error downgrading an irreversible migration")
	}
	if _, err := m.DowngradeSQL("head", "base"); err == nil {
		t.Error("offline: expected error rendering downgrade SQL for an irreversible migration")
	}
}
