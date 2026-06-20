package migrate_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/migrate"
)

// appliedHistory returns a QueryFunc that reports the given revisions as applied
// (the SELECT history against schema_migrations, all "up" events), and serves
// empty for anything else.
func appliedHistory(revisions ...string) func(string, []any) (testutil.Result, error) {
	rows := make([][]driver.Value, len(revisions))
	for i, rev := range revisions {
		rows[i] = []driver.Value{rev, "m", "up", time.Unix(0, 0).UTC()}
	}
	return func(q string, _ []any) (testutil.Result, error) {
		if strings.Contains(q, "schema_migrations") && strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
			return testutil.Result{Columns: []string{"revision", "name", "direction", "applied_at"}, Rows: rows}, nil
		}
		return testutil.Result{}, nil
	}
}

func sampleMigrations() []migrate.Migration {
	return []migrate.Migration{
		{
			Revision:     "0002",
			DownRevision: "0001",
			Name:         "create_posts",
			Up: func(op *migrate.Operator) {
				op.CreateTable("posts", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
			},
			Down: func(op *migrate.Operator) { op.DropTable("posts") },
		},
		{
			Revision:     "0001",
			DownRevision: "",
			Name:         "create_tags",
			Up: func(op *migrate.Operator) {
				op.CreateTable("tags", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
			},
			Down: func(op *migrate.Operator) { op.DropTable("tags") },
		},
	}
}

func TestMigratorUpAppliesInTopologicalOrder(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
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
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002")} // both applied
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
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002")}
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
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002")}
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
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001")}
	db := sqlkit.WrapDB(fake.DB())
	m := migrate.New(db, sampleMigrations())

	status, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(status) != 2 {
		t.Fatalf("status len = %d, want 2", len(status))
	}
	if status[0].Revision != "0001" || !status[0].Applied {
		t.Errorf("status[0] = %+v, want revision 0001 applied", status[0])
	}
	if status[1].Revision != "0002" || status[1].Applied {
		t.Errorf("status[1] = %+v, want revision 0002 pending", status[1])
	}
}

func TestMigratorDuplicateRevisionRejected(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
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
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
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
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
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
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs)

	if err := m.Downgrade(context.Background(), "base"); err == nil {
		t.Error("online: expected error downgrading an irreversible migration")
	}
	if _, err := m.DowngradeSQL("head", "base"); err == nil {
		t.Error("offline: expected error rendering downgrade SQL for an irreversible migration")
	}
}

// mk builds a simple create/drop migration for one table.
func mk(rev, down, name, table string) migrate.Migration {
	return migrate.Migration{
		Revision:     rev,
		DownRevision: down,
		Name:         name,
		Up: func(op *migrate.Operator) {
			op.CreateTable(table, decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
		},
		Down: func(op *migrate.Operator) { op.DropTable(table) },
	}
}

// mergeMigrations is a DAG: 0001 branches into 0002a/0002b, merged by 0003.
func mergeMigrations() []migrate.Migration {
	merge := migrate.Migration{
		Revision:      "0003",
		DownRevisions: []string{"0002a", "0002b"},
		Name:          "merge",
		Up: func(op *migrate.Operator) {
			op.CreateTable("t3", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
		},
		Down: func(op *migrate.Operator) { op.DropTable("t3") },
	}
	// Declared out of order on purpose.
	return []migrate.Migration{
		mk("0002a", "0001", "branch_a", "t2a"),
		merge,
		mk("0001", "", "base", "t1"),
		mk("0002b", "0001", "branch_b", "t2b"),
	}
}

func creates(execs []testutil.Call) []string {
	var out []string
	for _, e := range execs {
		line := firstLine(e.SQL)
		if strings.HasPrefix(line, "CREATE TABLE ") && !strings.Contains(line, "schema_migrations") {
			out = append(out, line)
		}
	}
	return out
}

func TestDAGUpAppliesMergeInTopoOrder(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), mergeMigrations())

	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	want := []string{
		`CREATE TABLE "t1" (`,
		`CREATE TABLE "t2a" (`,
		`CREATE TABLE "t2b" (`,
		`CREATE TABLE "t3" (`,
	}
	if got := creates(fake.Execs()); !equalStrings(got, want) {
		t.Errorf("create order:\ngot  %v\nwant %v", got, want)
	}
}

func TestHeads(t *testing.T) {
	// Without the merge there are two heads; with it there is one.
	branched := []migrate.Migration{
		mk("0001", "", "base", "t1"),
		mk("0002a", "0001", "a", "t2a"),
		mk("0002b", "0001", "b", "t2b"),
	}
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), branched)
	if got, want := m.Heads(), []string{"0002a", "0002b"}; !equalStrings(got, want) {
		t.Errorf("heads = %v, want %v", got, want)
	}

	m = migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), mergeMigrations())
	if got, want := m.Heads(), []string{"0003"}; !equalStrings(got, want) {
		t.Errorf("heads after merge = %v, want %v", got, want)
	}
}

func TestCurrent(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002a")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), mergeMigrations())
	got, err := m.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if want := []string{"0002a"}; !equalStrings(got, want) {
		t.Errorf("current = %v, want %v", got, want)
	}
}

func TestUpgradeRelativeAndRevision(t *testing.T) {
	// +1 applies only the first pending migration.
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Upgrade(context.Background(), "+1"); err != nil {
		t.Fatalf("Upgrade +1: %v", err)
	}
	if got, want := creates(fake.Execs()), []string{`CREATE TABLE "tags" (`}; !equalStrings(got, want) {
		t.Errorf("+1 created %v, want %v", got, want)
	}

	// Upgrading to a specific revision applies it and its ancestors only.
	fake = &testutil.Fake{QueryFunc: appliedHistory()}
	m = migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Upgrade(context.Background(), "0001"); err != nil {
		t.Fatalf("Upgrade 0001: %v", err)
	}
	if got, want := creates(fake.Execs()), []string{`CREATE TABLE "tags" (`}; !equalStrings(got, want) {
		t.Errorf("upgrade 0001 created %v, want %v", got, want)
	}
}

func TestUpgradeAmbiguousPrefix(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations()) // 0001, 0002
	if err := m.Upgrade(context.Background(), "00"); err == nil {
		t.Error("expected ambiguous-prefix error")
	}
}

func TestStampMarksWithoutRunning(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Stamp(context.Background(), "head"); err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	// No DDL ran; only history inserts (and the ensureTable CREATE).
	if got := creates(fake.Execs()); len(got) != 0 {
		t.Errorf("stamp ran DDL: %v", got)
	}
	var inserts int
	for _, e := range fake.Execs() {
		if strings.HasPrefix(e.SQL, "INSERT") {
			inserts++
			if e.Args[2] != "up" {
				t.Errorf("stamp recorded direction %v, want up", e.Args[2])
			}
		}
	}
	if inserts != 2 {
		t.Errorf("stamp wrote %d history rows, want 2", inserts)
	}
}

func TestUpgradeSQLOffline(t *testing.T) {
	// Offline rendering does not query the database.
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), sampleMigrations())
	script, err := m.UpgradeSQL("", "head")
	if err != nil {
		t.Fatalf("UpgradeSQL: %v", err)
	}
	mustOrder(t, script,
		`CREATE TABLE "tags" (`,
		`INSERT INTO "schema_migrations"`,
		`CREATE TABLE "posts" (`,
		`INSERT INTO "schema_migrations"`,
	)
}

func TestDowngradeSQLOffline(t *testing.T) {
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), sampleMigrations())
	script, err := m.DowngradeSQL("head", "base")
	if err != nil {
		t.Fatalf("DowngradeSQL: %v", err)
	}
	mustOrder(t, script,
		`DROP TABLE IF EXISTS "posts";`,
		`DROP TABLE IF EXISTS "tags";`,
	)
}

func TestDownRollsBackMostRecentlyApplied(t *testing.T) {
	// Two independent branches. Topological order is [a, z] (sorted), so reverse
	// topo would pick "z" first — but "a" was applied last, and Down must undo
	// the most recently applied migration, not the one that sorts later.
	migs := []migrate.Migration{
		mk("a", "", "branch_a", "ta"),
		mk("z", "", "branch_z", "tz"),
	}
	// History: z applied first, then a (a is most recent).
	fake := &testutil.Fake{QueryFunc: appliedHistory("z", "a")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs)

	if err := m.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var dropped, rev string
	for _, e := range fake.Execs() {
		if strings.HasPrefix(e.SQL, "DROP TABLE") {
			dropped = firstLine(e.SQL)
		}
		if strings.HasPrefix(e.SQL, "INSERT") {
			rev, _ = e.Args[0].(string)
		}
	}
	if !strings.Contains(dropped, `"ta"`) {
		t.Errorf("dropped %q, want table ta (revision a)", dropped)
	}
	if rev != "a" {
		t.Errorf("rolled back revision %q, want a (most recently applied)", rev)
	}
}

// mustOrder asserts each substring appears in script in the given order.
func mustOrder(t *testing.T, script string, subs ...string) {
	t.Helper()
	prev := 0
	for _, sub := range subs {
		i := strings.Index(script[prev:], sub)
		if i < 0 {
			t.Fatalf("missing %q (in order) in:\n%s", sub, script)
		}
		prev += i + len(sub)
	}
}
