package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aita/sqlkit/migrate/autogen"
)

func TestParseRenames(t *testing.T) {
	r, err := parseRenames([]string{"users.name:full_name"}, []string{"old_t:new_t"})
	if err != nil {
		t.Fatalf("parseRenames error = %v", err)
	}
	want := autogen.Renames{
		Columns: []autogen.ColumnRename{{Table: "users", From: "name", To: "full_name"}},
		Tables:  []autogen.TableRename{{From: "old_t", To: "new_t"}},
	}
	if len(r.Columns) != 1 || r.Columns[0] != want.Columns[0] {
		t.Errorf("columns = %+v, want %+v", r.Columns, want.Columns)
	}
	if len(r.Tables) != 1 || r.Tables[0] != want.Tables[0] {
		t.Errorf("tables = %+v, want %+v", r.Tables, want.Tables)
	}
}

func TestParseRenamesInvalid(t *testing.T) {
	for _, spec := range []string{"nocolon", "users.:x", "users.name:", ".name:x"} {
		if _, err := parseRenames([]string{spec}, nil); err == nil {
			t.Errorf("parseRenames(%q) expected error", spec)
		}
	}
	if _, err := parseRenames(nil, []string{"noseparator"}); err == nil {
		t.Errorf("parseRenames table %q expected error", "noseparator")
	}
}

func TestDownRevisionField(t *testing.T) {
	tests := []struct {
		name   string
		heads  []string
		parent string
		merge  bool
		want   string
		errs   bool
	}{
		{name: "single head", heads: []string{"0003"}, want: `DownRevision: "0003"`},
		{name: "no heads", heads: nil, want: `DownRevision: ""`},
		{name: "explicit parent", heads: []string{"a", "b"}, parent: "a", want: `DownRevision: "a"`},
		{name: "merge", heads: []string{"a", "b"}, merge: true, want: "DownRevisions:"},
		{name: "ambiguous", heads: []string{"a", "b"}, errs: true},
		{name: "merge no heads", merge: true, errs: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := downRevisionField(tt.heads, tt.parent, tt.merge)
			if tt.errs {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("downRevisionField = %q, want substring %q", got, tt.want)
			}
		})
	}
}

// TestHeadsMissingMigrationsDirIsNoHeads covers the genuine "first migration"
// case: a --migrations directory that doesn't exist yet on disk. heads() must
// treat it as no heads rather than erroring.
func TestHeadsMissingMigrationsDirIsNoHeads(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module headstest\n\ngo 1.21\n")
	t.Chdir(dir)

	cmd := &autogenerateCmd{Migrations: "./migrations", Registry: "Registry"}
	heads, err := cmd.heads()
	if err != nil {
		t.Fatalf("heads() with no migrations directory: %v", err)
	}
	if len(heads) != 0 {
		t.Fatalf("heads = %v, want none", heads)
	}
}

// TestHeadsPropagatesGoListError covers an existing --migrations directory
// that fails to compile as a package (here: two conflicting `package`
// clauses): heads() must report the error, not silently treat it as "no
// migrations yet" — swallowing it would let autogenerate write a second,
// unlinked root migration on top of an already-broken package.
func TestHeadsPropagatesGoListError(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module headstest\n\ngo 1.21\n")
	writeTestFile(t, filepath.Join(dir, "migrations", "a.go"), "package migrations\n")
	writeTestFile(t, filepath.Join(dir, "migrations", "b.go"), "package othername\n")
	t.Chdir(dir)

	cmd := &autogenerateCmd{Migrations: "./migrations", Registry: "Registry"}
	if _, err := cmd.heads(); err == nil {
		t.Fatal("heads() with a broken migrations package: expected an error, got nil")
	}
}

func TestAutogenerateRequiresOneSource(t *testing.T) {
	// Neither source selected.
	if err := (&autogenerateCmd{}).Run(); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Errorf("no source: expected 'exactly one source' error, got %v", err)
	}
	// Both sources selected.
	if err := (&autogenerateCmd{FromDB: true, FromGit: "HEAD"}).Run(); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Errorf("both sources: expected 'exactly one source' error, got %v", err)
	}
}

// TestAutogenerateFromGit exercises the --from-git source path end to end in a
// throwaway git repository: it commits a catalog, edits the working tree, then
// runs autogenerate so the command checks the committed revision out into a
// worktree, runs its snapshot shims with `go run`, diffs the two snapshots, and
// writes a migration file. This covers gitSnapshot/targetSnapshot, the shim
// execution, and the gob snapshot boundary that the unit tests cannot reach.
//
// The temp module replaces github.com/aita/sqlkit with this checkout, and the
// catalog/shim packages have no external dependencies, so the shims build
// offline against the module cache already populated by the test run.
func TestAutogenerateFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// The sqlkit module root, captured before chdir so the temp module's replace
	// directive can point at it with an absolute path.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), fmt.Sprintf(`module sqlkitgittest.example/proj

go 1.25.0

require github.com/aita/sqlkit v0.0.0

replace github.com/aita/sqlkit => %s
`, repoRoot))
	writeTestFile(t, filepath.Join(dir, "go.sum"), "")

	// Initial committed catalog: widgets(id, name).
	writeTestFile(t, filepath.Join(dir, "schema", "schema.go"), catalogSource(
		`decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),`))

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "sqlkit test")
	// Disable commit signing: the test repo must commit without any globally
	// configured signing key or server.
	runGit(t, dir, "config", "commit.gpgsign", "false")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial catalog")

	// Working-tree catalog adds a column; --from-git HEAD diffs the committed
	// revision (source) against this working tree (target).
	writeTestFile(t, filepath.Join(dir, "schema", "schema.go"), catalogSource(
		`decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
		decl.Column("sku", decl.VarChar(64), decl.Nullable()),`))

	t.Chdir(dir)
	// Let the shims resolve the local replace without a pre-populated go.sum.
	t.Setenv("GOFLAGS", "-mod=mod")

	cmd := &autogenerateCmd{
		Name:       "add sku",
		Catalog:    "./schema",
		Var:        "Catalog",
		FromGit:    "HEAD",
		Migrations: "./migrations",
		Package:    "migrations",
		Registry:   "Registry",
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("autogenerate --from-git: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "migrations", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one generated migration, got %v", matches)
	}
	src, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, `op.AddColumn("widgets", decl.Column("sku"`) {
		t.Errorf("generated Up missing AddColumn(sku):\n%s", body)
	}
	if !strings.Contains(body, `op.DropColumn("widgets", "sku")`) {
		t.Errorf("generated Down missing DropColumn(sku):\n%s", body)
	}
}

// catalogSource renders a schema package declaring a widgets table with the
// given column elements. It is compiled by the autogenerate shims (via go run),
// not gofmt'd, so its indentation is unimportant.
func catalogSource(columns string) string {
	return `package schema

import "github.com/aita/sqlkit/decl"

var Catalog *decl.Catalog = func() *decl.Catalog {
	c := decl.NewCatalog(decl.DefaultSchema("public"))
	c.Table("widgets",
` + columns + `
	)
	return c
}()
`
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
