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
