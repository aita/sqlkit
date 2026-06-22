package migrate_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/migrate"
)

func TestCLIUpAndDown(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: appliedHistory()}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations())

	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"up"}); err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := creates(fake.Execs()); len(got) != 2 {
		t.Errorf("up created %v, want 2 tables", got)
	}
}

func TestCLIHeadsAndStatusOutput(t *testing.T) {
	var buf bytes.Buffer
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), sampleMigrations(), migrate.WithOutput(&buf))

	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"heads"}); err != nil {
		t.Fatalf("heads: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "0002" {
		t.Errorf("heads output = %q, want 0002", got)
	}

	buf.Reset()
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"status"}); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "0001") || !strings.Contains(out, "applied") {
		t.Errorf("status missing applied 0001:\n%s", out)
	}
	if !strings.Contains(out, "0002") || !strings.Contains(out, "pending") {
		t.Errorf("status missing pending 0002:\n%s", out)
	}
}

func TestCLISQLOffline(t *testing.T) {
	var buf bytes.Buffer
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), sampleMigrations(), migrate.WithOutput(&buf))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"sql", "up", "base", "head"}); err != nil {
		t.Fatalf("sql up: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `CREATE TABLE "tags"`) || !strings.Contains(out, `CREATE TABLE "posts"`) {
		t.Errorf("sql up output missing creates:\n%s", out)
	}
}

func TestCLINewScaffold(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	// One existing migration, so the new file's DownRevision is its head.
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()),
		[]migrate.Migration{mk("0001", "", "base", "t1")},
		migrate.WithOutput(&buf))

	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"new", "add widgets", "--dir", dir, "--package", "migrations"}); err != nil {
		t.Fatalf("new: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("created %d files, want 1", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, "_add_widgets.go") {
		t.Errorf("filename = %q, want ..._add_widgets.go", name)
	}
	src, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package migrations",
		`DownRevision: "0001",`,
		`Name:         "add widgets",`,
		"Registry.Add(migrate.Migration{",
		"func(op *migrate.Operator)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("scaffold missing %q:\n%s", want, src)
		}
	}
}

func TestCLINewScaffoldEqualsFlags(t *testing.T) {
	dir := t.TempDir()
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()),
		[]migrate.Migration{mk("0001", "", "base", "t1")},
		migrate.WithOutput(&bytes.Buffer{}))

	// --flag=value form must work the same as --flag value.
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()),
		[]string{"new", "add widgets", "--dir=" + dir, "--package=migrations"}); err != nil {
		t.Fatalf("new with =flags: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "_add_widgets.go") {
		t.Fatalf("entries = %v, want one ..._add_widgets.go", entries)
	}
}

func TestCLINewMerge(t *testing.T) {
	dir := t.TempDir()
	// 0001 branches into two heads, so a plain new is ambiguous and --merge joins them.
	migs := []migrate.Migration{
		mk("0001", "", "base", "t1"),
		mk("0002a", "0001", "branch_a", "t2a"),
		mk("0002b", "0001", "branch_b", "t2b"),
	}
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), migs, migrate.WithOutput(&bytes.Buffer{}))

	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"new", "x", "--dir", dir, "--package", "migrations"}); err == nil {
		t.Error("expected error scaffolding onto multiple heads without --merge")
	}

	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"new", "merge", "--dir", dir, "--package", "migrations", "--merge"}); err != nil {
		t.Fatalf("new --merge: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("created %d files, want 1", len(entries))
	}
	src, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(src), `DownRevisions: []string{"0002a", "0002b"}`) {
		t.Errorf("merge scaffold missing both heads:\n%s", src)
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), sampleMigrations(), migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"bogus"}); err == nil {
		t.Error("expected error for unknown command")
	}
}

// dispatch runs one CLI command and returns its captured output, failing the
// test on dispatch error.
func dispatch(t *testing.T, fake *testutil.Fake, migs []migrate.Migration, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs, migrate.WithOutput(&buf))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), args); err != nil {
		t.Fatalf("dispatch %v: %v", args, err)
	}
	return buf.String()
}

// TestCLIPendingCurrentHistory exercises the pending, current and history
// subcommands' Run methods and their output formatting.
func TestCLIPendingCurrentHistory(t *testing.T) {
	migs := sampleMigrations() // 0001 -> 0002

	pending := dispatch(t, &testutil.Fake{QueryFunc: appliedHistory("0001")}, migs, "pending")
	if !strings.Contains(pending, "0002") || strings.Contains(pending, "0001") {
		t.Errorf("pending output = %q, want only 0002 listed", pending)
	}

	current := dispatch(t, &testutil.Fake{QueryFunc: appliedHistory("0001")}, migs, "current")
	if strings.TrimSpace(current) != "0001" {
		t.Errorf("current output = %q, want 0001", current)
	}

	history := dispatch(t, &testutil.Fake{QueryFunc: appliedHistory("0001", "0002")}, migs, "history")
	if !strings.Contains(history, "up") || !strings.Contains(history, "0001") || !strings.Contains(history, "0002") {
		t.Errorf("history output = %q, want up events for 0001 and 0002", history)
	}
}

// TestCLIDownAndStamp exercises the down and stamp subcommands' Run methods.
func TestCLIDownAndStamp(t *testing.T) {
	migs := sampleMigrations()

	// down (default -1) rolls back the most recently applied migration.
	fake := &testutil.Fake{QueryFunc: appliedHistory("0001", "0002")}
	m := migrate.New(sqlkit.WrapDB(fake.DB()), migs, migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{"down"}); err != nil {
		t.Fatalf("down: %v", err)
	}
	var droppedPosts bool
	for _, e := range fake.Execs() {
		if strings.Contains(e.SQL, "DROP TABLE") && strings.Contains(e.SQL, `"posts"`) {
			droppedPosts = true
		}
	}
	if !droppedPosts {
		t.Error("down did not roll back the most recent migration (posts)")
	}

	// stamp head marks everything applied without running DDL.
	fake2 := &testutil.Fake{QueryFunc: appliedHistory()}
	m2 := migrate.New(sqlkit.WrapDB(fake2.DB()), migs, migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m2.Runner(context.Background()), []string{"stamp", "head"}); err != nil {
		t.Fatalf("stamp head: %v", err)
	}
	if got := creates(fake2.Execs()); len(got) != 0 {
		t.Errorf("stamp ran DDL: %v", got)
	}
}

// TestCLISQLDown renders an offline downgrade via the sql down subcommand.
func TestCLISQLDown(t *testing.T) {
	out := dispatch(t, &testutil.Fake{}, sampleMigrations(), "sql", "down")
	if !strings.Contains(out, `DROP TABLE IF EXISTS "posts";`) || !strings.Contains(out, `DROP TABLE IF EXISTS "tags";`) {
		t.Errorf("sql down output missing drops:\n%s", out)
	}
}

// TestCLIDispatchArgErrors covers Dispatch's argument-validation branches.
func TestCLIDispatchArgErrors(t *testing.T) {
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), sampleMigrations(), migrate.WithOutput(&bytes.Buffer{}))
	run := func(args ...string) error {
		return migrate.NewCLI().Dispatch(m.Runner(context.Background()), args)
	}
	cases := map[string][]string{
		"no command":        {},
		"stamp no revision": {"stamp"},
		"sql no direction":  {"sql"},
		"sql bad direction": {"sql", "sideways"},
		"new no name":       {"new"},
	}
	for name, args := range cases {
		if err := run(args...); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestCLINewFlagErrors covers parseNew's error branches: unknown flags, a flag
// missing its value, an invalid --merge value, and an unexpected positional.
func TestCLINewFlagErrors(t *testing.T) {
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()),
		[]migrate.Migration{mk("0001", "", "base", "t1")}, migrate.WithOutput(&bytes.Buffer{}))
	run := func(args ...string) error {
		return migrate.NewCLI().Dispatch(m.Runner(context.Background()), append([]string{"new"}, args...))
	}
	cases := map[string][]string{
		"unknown long flag":  {"name", "--bogus", "x"},
		"unknown short flag": {"name", "-x"},
		"flag missing value": {"name", "--dir"},
		"bad merge value":    {"name", "--merge=maybe"},
		"extra positional":   {"a", "b"},
	}
	for name, args := range cases {
		if err := run(args...); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestCLINewParentFlag covers the --parent path of downRevisionField (an
// explicit parent rather than the inferred head).
func TestCLINewParentFlag(t *testing.T) {
	dir := t.TempDir()
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()),
		[]migrate.Migration{mk("0001", "", "base", "t1"), mk("0002", "0001", "next", "t2")},
		migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()),
		[]string{"new", "branch off root", "--dir", dir, "--parent", "0001"}); err != nil {
		t.Fatalf("new --parent: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("created %d files, want 1", len(entries))
	}
	src, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(src), `DownRevision: "0001",`) {
		t.Errorf("scaffold did not use --parent 0001:\n%s", src)
	}
}

// TestCLINewNoMigrations scaffolds the first migration (no heads): its
// DownRevision must be empty.
func TestCLINewNoMigrations(t *testing.T) {
	dir := t.TempDir()
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), nil, migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()),
		[]string{"new", "first", "--dir", dir}); err != nil {
		t.Fatalf("new: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	src, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(src), `DownRevision: "",`) {
		t.Errorf("first migration scaffold should have empty DownRevision:\n%s", src)
	}
}

// TestCLINewMergeNeedsMigrations checks --merge with no existing migrations
// errors rather than producing a merge of nothing.
func TestCLINewMergeNeedsMigrations(t *testing.T) {
	dir := t.TempDir()
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), nil, migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()),
		[]string{"new", "merge", "--dir", dir, "--merge"}); err == nil {
		t.Error("--merge with no migrations should error")
	}
}

// TestCLINewPlanErrorSurfaces checks the new command refuses when the migration
// DAG is misconfigured (Heads can't be trusted), surfacing the plan error.
func TestCLINewPlanErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	dup := []migrate.Migration{mk("x", "", "a", "ta"), mk("x", "", "b", "tb")}
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), dup, migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()),
		[]string{"new", "next", "--dir", dir}); err == nil {
		t.Error("new should surface the plan error for a duplicate-revision set")
	}
}

// TestCLISlugSanitizes checks the scaffold filename slug collapses punctuation
// and whitespace into single underscores and trims them.
func TestCLISlugSanitizes(t *testing.T) {
	dir := t.TempDir()
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{}).DB()), nil, migrate.WithOutput(&bytes.Buffer{}))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()),
		[]string{"new", "  Add Users & Posts!! ", "--dir", dir}); err != nil {
		t.Fatalf("new: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("created %d files, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), "_add_users_posts.go") {
		t.Errorf("filename = %q, want suffix _add_users_posts.go", entries[0].Name())
	}
}

// TestCLIErrorFromRunPropagates checks a subcommand whose Run hits a database
// error returns it (here pending against a failing history SELECT).
func TestCLIErrorFromRunPropagates(t *testing.T) {
	boom := func(q string, _ []any) (testutil.Result, error) {
		if strings.Contains(q, "schema_migrations") && strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
			return testutil.Result{}, context.DeadlineExceeded
		}
		return testutil.Result{}, nil
	}
	m := migrate.New(sqlkit.WrapDB((&testutil.Fake{QueryFunc: boom}).DB()), sampleMigrations(),
		migrate.WithOutput(&bytes.Buffer{}))
	for _, cmd := range []string{"pending", "current", "status", "history"} {
		if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), []string{cmd}); err == nil {
			t.Errorf("%s should propagate the history read error", cmd)
		}
	}
}
