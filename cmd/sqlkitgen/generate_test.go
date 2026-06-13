package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderShimIsValidGo checks the throwaway generator program is buildable
// Go and wires the options through to codegen.Generate.
func TestRenderShimIsValidGo(t *testing.T) {
	c := &generateCmd{
		Var:          "Catalog",
		Name:         "appdb",
		Dialect:      "postgres",
		Tables:       []string{"users", "posts"},
		FilePerTable: true,
		EmitDTO:      true,
	}
	src, err := renderShim("github.com/me/app/schema", "/tmp/appdb", c)
	if err != nil {
		t.Fatalf("renderShim: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", src, parser.AllErrors); err != nil {
		t.Fatalf("shim is not valid Go: %v\n%s", err, src)
	}
	s := string(src)
	for _, want := range []string{
		`schema "github.com/me/app/schema"`,
		`"github.com/aita/sqlkit/sqldef"`,
		"var _ *sqldef.Catalog = schema.Catalog",
		"schema.Catalog.Build()",
		"codegen.Generate(md,",
		`PackageName:     "appdb"`,
		`Tables:          []string{"users", "posts"}`,
		"FilePerTable:    true",
		"EmitDTO:         true",
		`codegen.Write("/tmp/appdb", files)`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("shim missing %q\n%s", want, s)
		}
	}
}

// TestRenderDDLShim checks the ddl subcommand's shim, both to a file and to
// stdout.
func TestRenderDDLShim(t *testing.T) {
	toFile := &ddlCmd{Var: "Catalog", Dialect: "postgres"}
	src, err := renderDDLShim("github.com/me/app/schema", "/tmp/schema.sql", toFile)
	if err != nil {
		t.Fatalf("renderDDLShim: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", src, parser.AllErrors); err != nil {
		t.Fatalf("ddl shim is not valid Go: %v\n%s", err, src)
	}
	s := string(src)
	for _, want := range []string{
		`schema "github.com/me/app/schema"`,
		"var _ *sqldef.Catalog = schema.Catalog",
		`sql.DialectByName("postgres")`,
		`md.CreateSQL(dialect)`,
		`os.WriteFile("/tmp/schema.sql"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ddl shim missing %q\n%s", want, s)
		}
	}

	// With no --out the shim prints to stdout instead of writing a file.
	stdoutSrc, err := renderDDLShim("github.com/me/app/schema", "", &ddlCmd{Var: "Catalog", Dialect: "mysql"})
	if err != nil {
		t.Fatalf("renderDDLShim stdout: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", stdoutSrc, parser.AllErrors); err != nil {
		t.Fatalf("ddl stdout shim is not valid Go: %v\n%s", err, stdoutSrc)
	}
	ss := string(stdoutSrc)
	if !strings.Contains(ss, "fmt.Print(sqlText)") {
		t.Errorf("ddl stdout shim should print to stdout\n%s", ss)
	}
	if strings.Contains(ss, "os.WriteFile") {
		t.Errorf("ddl stdout shim should not write a file\n%s", ss)
	}

	// With --drop the shim prepends the teardown DDL.
	dropSrc, err := renderDDLShim("github.com/me/app/schema", "", &ddlCmd{Var: "Catalog", Dialect: "postgres", Drop: true})
	if err != nil {
		t.Fatalf("renderDDLShim drop: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", dropSrc, parser.AllErrors); err != nil {
		t.Fatalf("ddl drop shim is not valid Go: %v\n%s", err, dropSrc)
	}
	if ds := string(dropSrc); !strings.Contains(ds, "md.DropSQL(dialect)") {
		t.Errorf("ddl drop shim should prepend DropSQL\n%s", ds)
	}
}

func TestTablesLiteral(t *testing.T) {
	if got := tablesLiteral(nil); got != "nil" {
		t.Errorf("tablesLiteral(nil) = %q, want nil", got)
	}
	if got := tablesLiteral([]string{"a", "b"}); got != `[]string{"a", "b"}` {
		t.Errorf("tablesLiteral = %q", got)
	}
}

// TestGenerateMatchesCommittedBindings runs the generate command end-to-end
// against the in-repo blog catalog (the same path the example's gen.go drives)
// and checks it reproduces the committed bindings byte for byte.
func TestGenerateMatchesCommittedBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go run-based end-to-end test in -short mode")
	}
	out := t.TempDir()
	cmd := &generateCmd{
		Package: "github.com/aita/sqlkit/examples/blog/schema",
		Var:     "Catalog",
		Name:    "blogdb",
		Out:     out,
		Dialect: "postgres",
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(out, "tables.go"))
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "examples", "blog", "blogdb", "tables.go"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("generated bindings differ from examples/blog/blogdb/tables.go\n--- got ---\n%s", got)
	}
}
