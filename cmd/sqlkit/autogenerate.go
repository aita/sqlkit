package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/aita/sqlkit/introspect"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/migrate/autogen"
	sqlast "github.com/aita/sqlkit/sql"
)

// autogenerateCmd generates a migration by diffing a source schema against the
// target decl catalog (the desired state). The source is selected with
// --from-db (a live database) or --from-git (the catalog as it stood at a git
// revision). The Up body migrates the source up to the catalog; the Down body is
// a best-effort reverse. Because a catalog is Go code, the command compiles it
// into a throwaway program to read its schema (see shim.go).
type autogenerateCmd struct {
	Name    string `arg:"" help:"Human-readable migration name (e.g. \"add tags\")."`
	Catalog string `help:"Import path or directory of the package that declares the target decl catalog." default:"./schema"`
	Var     string `help:"Name of the *decl.Catalog variable." default:"Catalog" name:"var"`
	Dialect string `help:"SQL dialect (postgres or mysql); inferred from --driver for --from-db, else postgres."`

	FromDB  bool   `help:"Use a live database as the source schema (needs --dsn)." name:"from-db"`
	FromGit string `help:"Use the catalog at this git revision as the source schema (e.g. HEAD~1, main)." name:"from-git"`

	DSN    string `help:"Database connection string for --from-db." env:"DATABASE_URL"`
	Driver string `help:"database/sql driver name for --from-db (pgx or mysql)." default:"pgx"`
	Schema string `help:"Database schema to inspect for --from-db." default:"public"`

	Migrations string `help:"Directory holding the migration files (also read for the current head)." default:"./migrations"`
	Package    string `help:"Package name for the generated migration file." default:"migrations"`
	Registry   string `help:"Name of the exported *migrate.Registry in the migrations package." default:"Registry"`
	Parent     string `help:"Down revision to build on (defaults to the current head)."`
	Merge      bool   `help:"Link the migration onto all current heads (a merge)."`

	Rename      []string `help:"Treat a drop+add as a column rename: table.old:new (repeatable)."`
	RenameTable []string `help:"Treat a drop+add as a table rename: old:new (repeatable)." name:"rename-table"`

	DryRun bool `help:"Print the generated migration to stdout instead of writing a file." name:"dry-run"`
}

func (c *autogenerateCmd) Run() error {
	if c.FromDB == (c.FromGit != "") {
		return fmt.Errorf("choose exactly one source: --from-db or --from-git <ref>")
	}
	dialect := c.Dialect
	if dialect == "" {
		if c.FromDB {
			dialect = dialectFor(c.Driver)
		} else {
			dialect = "postgres"
		}
	}
	d, err := sqlast.DialectByName(dialect)
	if err != nil {
		return err
	}

	catalog, err := resolvePackage(c.Catalog)
	if err != nil {
		return err
	}

	// Target: the working-tree catalog, read through a shim that resolves it to a
	// dialect snapshot.
	target, err := c.targetSnapshot(catalog.ImportPath, dialect)
	if err != nil {
		return err
	}

	// Source: a live database or the catalog at a git revision.
	source, err := c.sourceMetadata(context.Background(), catalog.ImportPath, dialect, d)
	if err != nil {
		return err
	}

	renames, err := parseRenames(c.Rename, c.RenameTable)
	if err != nil {
		return err
	}

	result, err := autogen.Render(source, target.Metadata(), autogen.Options{Dialect: d, Renames: renames})
	if err != nil {
		return err
	}
	if result.Empty() {
		fmt.Fprintln(os.Stdout, "no schema changes: the source already matches the catalog") //nolint:errcheck // best-effort write to stdout
		return nil
	}

	heads, err := c.heads()
	if err != nil {
		return err
	}
	downField, err := downRevisionField(heads, c.Parent, c.Merge)
	if err != nil {
		return err
	}

	file, err := renderMigrationFile(migrationData{
		Package:   c.Package,
		Registry:  c.Registry,
		Revision:  migrate.NewRevision(),
		DownField: downField,
		Name:      c.Name,
		Up:        result.Up,
		Down:      result.Down,
		UsesSQL:   result.UsesSQL,
		UsesDecl:  result.UsesDecl,
	})
	if err != nil {
		return err
	}

	if c.DryRun {
		_, err := os.Stdout.Write(file.content)
		return err
	}
	if err := os.MkdirAll(c.Migrations, 0o755); err != nil {
		return err
	}
	path := filepath.Join(c.Migrations, file.name)
	if err := os.WriteFile(path, file.content, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "created %s\nReview it before applying; auto-generated migrations can need adjustment.\n", path) //nolint:errcheck // best-effort write to stdout
	return nil
}

// targetSnapshot builds the working-tree catalog and resolves it to a snapshot
// via a shim that imports the catalog package and the autogen library.
func (c *autogenerateCmd) targetSnapshot(importPath, dialect string) (autogen.Snapshot, error) {
	src, err := renderTemplate(snapshotShimTmpl, snapshotData{ImportPath: importPath, Var: c.Var, Dialect: dialect})
	if err != nil {
		return autogen.Snapshot{}, err
	}
	out, err := runShimCapture(src, fmt.Sprintf("read catalog %s.%s", importPath, c.Var))
	if err != nil {
		return autogen.Snapshot{}, err
	}
	return decodeSnapshot(out)
}

// sourceMetadata resolves the source schema: a live database (introspection) or the
// catalog at a git revision (a worktree + a self-contained snapshot shim).
func (c *autogenerateCmd) sourceMetadata(ctx context.Context, importPath, dialect string, d sqlast.Dialect) (meta.Metadata, error) {
	if c.FromDB {
		if strings.TrimSpace(c.DSN) == "" {
			return meta.Metadata{}, fmt.Errorf("--from-db needs a database: pass --dsn or set DATABASE_URL")
		}
		db, err := sql.Open(c.Driver, c.DSN)
		if err != nil {
			return meta.Metadata{}, err
		}
		defer db.Close() //nolint:errcheck // best-effort close
		return introspect.Schema(ctx, db, dialect, c.Schema)
	}

	// --from-git: build the catalog at the given revision in a temporary worktree.
	snap, err := c.gitSnapshot(importPath, c.FromGit, dialect)
	if err != nil {
		return meta.Metadata{}, err
	}
	return snap.Metadata(), nil
}

// gitSnapshot checks the repository out at ref in a temporary worktree and runs
// the snapshot shim there, so the catalog at that revision builds against its
// own sqlkit version. It reads the full schema (including foreign-key actions),
// the same as the working-tree and database sources.
func (c *autogenerateCmd) gitSnapshot(importPath, ref, dialect string) (autogen.Snapshot, error) {
	worktree, err := os.MkdirTemp("", "sqlkit-worktree-")
	if err != nil {
		return autogen.Snapshot{}, err
	}
	defer os.RemoveAll(worktree) //nolint:errcheck // best-effort temp cleanup
	if out, err := exec.Command("git", "worktree", "add", "--detach", worktree, ref).CombinedOutput(); err != nil {
		return autogen.Snapshot{}, fmt.Errorf("git worktree add %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	defer exec.Command("git", "worktree", "remove", "--force", worktree).Run() //nolint:errcheck // best-effort cleanup

	src, err := renderTemplate(snapshotShimTmpl, snapshotData{ImportPath: importPath, Var: c.Var, Dialect: dialect})
	if err != nil {
		return autogen.Snapshot{}, err
	}
	var buf bytes.Buffer
	if err := runShimInDir(worktree, src, fmt.Sprintf("read catalog %s.%s at %s", importPath, c.Var, ref), &buf); err != nil {
		return autogen.Snapshot{}, err
	}
	return decodeSnapshot(buf.Bytes())
}

// heads compiles the migrations package and prints its current head revisions,
// without touching a database.
func (c *autogenerateCmd) heads() ([]string, error) {
	pkg, err := resolveImportPath(c.Migrations)
	if err != nil {
		// No migrations package yet: treat as no heads (first migration).
		return nil, nil
	}
	if !identRe.MatchString(c.Registry) {
		return nil, fmt.Errorf("invalid --registry %q: must be a Go identifier", c.Registry)
	}
	src, err := renderTemplate(headsTmpl, headsData{ImportPath: pkg, Registry: c.Registry})
	if err != nil {
		return nil, err
	}
	out, err := runShimCapture(src, fmt.Sprintf("read heads from %s.%s", pkg, c.Registry))
	if err != nil {
		return nil, err
	}
	var heads []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			heads = append(heads, line)
		}
	}
	return heads, nil
}

func decodeSnapshot(b []byte) (autogen.Snapshot, error) {
	return autogen.DecodeSnapshot(bytes.NewReader(b))
}

// parseRenames turns --rename / --rename-table flags into autogen.Renames.
func parseRenames(columns, tables []string) (autogen.Renames, error) {
	var r autogen.Renames
	for _, spec := range columns {
		left, to, ok := strings.Cut(spec, ":")
		table, from, ok2 := strings.Cut(left, ".")
		if !ok || !ok2 || table == "" || from == "" || to == "" {
			return autogen.Renames{}, fmt.Errorf("invalid --rename %q: want table.old:new", spec)
		}
		r.Columns = append(r.Columns, autogen.ColumnRename{Table: table, From: from, To: to})
	}
	for _, spec := range tables {
		from, to, ok := strings.Cut(spec, ":")
		if !ok || from == "" || to == "" {
			return autogen.Renames{}, fmt.Errorf("invalid --rename-table %q: want old:new", spec)
		}
		r.Tables = append(r.Tables, autogen.TableRename{From: from, To: to})
	}
	return r, nil
}

// downRevisionField renders the DownRevision/DownRevisions struct field, linking
// the migration onto the head (or the explicit parent/merge).
func downRevisionField(heads []string, parent string, merge bool) (string, error) {
	switch {
	case merge:
		if len(heads) == 0 {
			return "", fmt.Errorf("--merge needs existing migrations")
		}
		return fmt.Sprintf("\tDownRevisions: %#v,", heads), nil
	case parent != "":
		return fmt.Sprintf("\tDownRevision: %q,", parent), nil
	case len(heads) == 0:
		return "\tDownRevision: \"\",", nil
	case len(heads) == 1:
		return fmt.Sprintf("\tDownRevision: %q,", heads[0]), nil
	default:
		return "", fmt.Errorf("multiple heads %v; pass --parent <rev> or --merge", heads)
	}
}

type migrationData struct {
	Package   string
	Registry  string
	Revision  string
	DownField string
	Name      string
	Up        string
	Down      string
	UsesSQL   bool
	UsesDecl  bool
}

type migrationFile struct {
	name    string
	content []byte
}

// renderMigrationFile builds and gofmts the migration source.
func renderMigrationFile(d migrationData) (migrationFile, error) {
	src, err := renderTemplate(migrationTmpl, d)
	if err != nil {
		return migrationFile{}, err
	}
	formatted, err := format.Source(src)
	if err != nil {
		return migrationFile{}, fmt.Errorf("formatting generated migration: %w\n%s", err, src)
	}
	name := time.Now().UTC().Format("20060102150405") + "_" + slug(d.Name) + ".go"
	return migrationFile{name: name, content: formatted}, nil
}

// slug turns a migration name into a filename-safe lowercase token.
func slug(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		case !prevUnderscore:
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "migration"
	}
	return out
}

func renderTemplate(t *template.Template, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type snapshotData struct {
	ImportPath string
	Var        string
	Dialect    string
}

type headsData struct {
	ImportPath string
	Registry   string
}

// snapshotShimTmpl reads a catalog (working tree or git worktree) and resolves
// it to a dialect snapshot with the autogen library.
var snapshotShimTmpl = template.Must(template.New("snapshot-shim").Parse(`// Code generated by sqlkit migrate autogenerate; temporary. DO NOT EDIT.
package main

import (
	"log"
	"os"

	schema {{printf "%q" .ImportPath}}
	"github.com/aita/sqlkit/migrate/autogen"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/decl"
)

var _ *decl.Catalog = schema.{{.Var}}

func main() {
	md, err := schema.{{.Var}}.Build()
	if err != nil {
		log.Fatal(err)
	}
	d, err := sql.DialectByName({{printf "%q" .Dialect}})
	if err != nil {
		log.Fatal(err)
	}
	snap, err := autogen.Resolve(md, d)
	if err != nil {
		log.Fatal(err)
	}
	if err := snap.Encode(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
`))

// headsTmpl prints the migration set's current head revisions (no database).
var headsTmpl = template.Must(template.New("heads").Parse(`// Code generated by sqlkit migrate autogenerate; temporary. DO NOT EDIT.
package main

import (
	"fmt"

	migrations {{printf "%q" .ImportPath}}
	"github.com/aita/sqlkit/migrate"
)

var _ *migrate.Registry = migrations.{{.Registry}}

func main() {
	m := migrations.{{.Registry}}.NewMigrator(nil)
	for _, h := range m.Heads() {
		fmt.Println(h)
	}
}
`))

// migrationTmpl is the generated migration file: a registered Migration whose
// Up/Down bodies are the rendered Operator-DSL statements.
var migrationTmpl = template.Must(template.New("migration").Parse(`// Code generated by sqlkit migrate autogenerate; review before applying.
package {{.Package}}

import (
	"github.com/aita/sqlkit/migrate"
{{- if .UsesSQL}}
	"github.com/aita/sqlkit/sql"
{{- end}}
{{- if .UsesDecl}}
	"github.com/aita/sqlkit/decl"
{{- end}}
)

var _ = {{.Registry}}.Add(migrate.Migration{
	Revision:     {{printf "%q" .Revision}},
{{.DownField}}
	Name:         {{printf "%q" .Name}},
	Up: func(op *migrate.Operator) {
{{.Up}}
	},
	Down: func(op *migrate.Operator) {
{{.Down}}
	},
})
`))
