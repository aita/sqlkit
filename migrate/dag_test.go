package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/migrate"
)

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

func creates(execs []sqltest.Call) []string {
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
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
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
	m := migrate.New(sqlkit.WrapDB((&sqltest.Fake{}).DB()), branched)
	if got, want := m.Heads(), []string{"0002a", "0002b"}; !equalStrings(got, want) {
		t.Errorf("heads = %v, want %v", got, want)
	}

	m = migrate.New(sqlkit.WrapDB((&sqltest.Fake{}).DB()), mergeMigrations())
	if got, want := m.Heads(), []string{"0003"}; !equalStrings(got, want) {
		t.Errorf("heads after merge = %v, want %v", got, want)
	}
}

func TestCurrent(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory("0001", "0002a")}
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
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Upgrade(context.Background(), "+1"); err != nil {
		t.Fatalf("Upgrade +1: %v", err)
	}
	if got, want := creates(fake.Execs()), []string{`CREATE TABLE "tags" (`}; !equalStrings(got, want) {
		t.Errorf("+1 created %v, want %v", got, want)
	}

	// Upgrading to a specific revision applies it and its ancestors only.
	fake = &sqltest.Fake{QueryFunc: appliedHistory()}
	m = migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())
	if err := m.Upgrade(context.Background(), "0001"); err != nil {
		t.Fatalf("Upgrade 0001: %v", err)
	}
	if got, want := creates(fake.Execs()), []string{`CREATE TABLE "tags" (`}; !equalStrings(got, want) {
		t.Errorf("upgrade 0001 created %v, want %v", got, want)
	}
}

func TestUpgradeAmbiguousPrefix(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations()) // 0001, 0002
	if err := m.Upgrade(context.Background(), "00"); err == nil {
		t.Error("expected ambiguous-prefix error")
	}
}

func TestStampMarksWithoutRunning(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: appliedHistory()}
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
	m := migrate.New(sqlkit.WrapDB((&sqltest.Fake{}).DB()), sampleMigrations())
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
	m := migrate.New(sqlkit.WrapDB((&sqltest.Fake{}).DB()), sampleMigrations())
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
	fake := &sqltest.Fake{QueryFunc: appliedHistory("z", "a")}
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
