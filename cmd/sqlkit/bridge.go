package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// bridgeFlags are shared by the migrate execution subcommands. Because
// migrations are Go code, the sqlkit binary cannot run them directly; instead it
// compiles the migrations package into a throwaway program that opens the
// database and dispatches to migrate.NewCLI().Dispatch, then runs it with `go run`.
type bridgeFlags struct {
	Migrations string `help:"Directory (or import path) of the migrations package." default:"./migrations"`
	DSN        string `help:"Database connection string." env:"DATABASE_URL"`
	Driver     string `help:"database/sql driver name." default:"pgx"`
	DriverPkg  string `name:"driver-pkg" help:"Import path of the driver to register." default:"github.com/jackc/pgx/v5/stdlib"`
	Schema     string `help:"Default schema for migrations." default:"public"`
	Registry   string `help:"Name of the exported *migrate.Registry in the package." default:"Registry"`
}

var (
	identRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	driverRe  = regexp.MustCompile(`^[A-Za-z0-9+._-]+$`)
	pkgPathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// run compiles the migrations package and dispatches the command (args[0]) and
// its arguments to migrate.NewCLI().Dispatch inside the compiled program.
func (f *bridgeFlags) run(args ...string) error {
	// "new" only scaffolds a file from the migration set; everything else opens
	// the database.
	scaffoldOnly := len(args) > 0 && args[0] == "new"
	if !scaffoldOnly && strings.TrimSpace(f.DSN) == "" {
		return fmt.Errorf("no database connection: pass --dsn or set DATABASE_URL")
	}
	if !identRe.MatchString(f.Registry) {
		return fmt.Errorf("invalid --registry %q: must be a Go identifier", f.Registry)
	}
	if !driverRe.MatchString(f.Driver) {
		return fmt.Errorf("invalid --driver %q", f.Driver)
	}
	if !pkgPathRe.MatchString(f.DriverPkg) {
		return fmt.Errorf("invalid --driver-pkg %q", f.DriverPkg)
	}

	importPath, err := resolveImportPath(f.Migrations)
	if err != nil {
		return err
	}
	moduleRoot, err := moduleRoot()
	if err != nil {
		return err
	}

	src, err := renderBridge(bridgeData{
		ImportPath: importPath,
		Registry:   f.Registry,
		Driver:     f.Driver,
		DriverPkg:  f.DriverPkg,
	})
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp(moduleRoot, ".sqlkit-migrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck // best-effort temp cleanup
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), src, 0o644); err != nil {
		return err
	}

	rel, err := filepath.Rel(moduleRoot, tmpDir)
	if err != nil {
		return err
	}

	// Build first so a compile error in the migrations package is reported as
	// such, then run the binary directly — running via `go build` rather than
	// `go run` keeps `go run`'s own "exit status" line out of the output.
	bin := filepath.Join(tmpDir, "runner")
	build := exec.Command("go", "build", "-o", bin, "./"+filepath.ToSlash(rel))
	build.Dir = moduleRoot
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("compiling the migrations package: %w", err)
	}

	// The build needs the module, but the compiled binary runs in the caller's
	// working directory so a relative --dir on "new" resolves where expected.
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"SQLKIT_MIGRATE_DSN="+f.DSN,
		"SQLKIT_MIGRATE_SCHEMA="+f.Schema,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// The compiled program already reported the real error on stderr; mirror
		// its exit code rather than wrapping it in a second "exit status" line.
		os.RemoveAll(tmpDir) //nolint:errcheck // os.Exit skips the deferred cleanup; best-effort
		os.Exit(exitErr.ExitCode())
	}
	return err
}

// resolveImportPath turns the migrations directory (or import path) into a
// canonical import path the generated program can import.
func resolveImportPath(dir string) (string, error) {
	// An import path (not a filesystem path) is used as-is.
	if !strings.HasPrefix(dir, ".") && !strings.HasPrefix(dir, "/") && !filepathExists(dir) {
		return dir, nil
	}
	out, err := goList("{{.ImportPath}}", dir)
	if err != nil {
		return "", fmt.Errorf("resolving migrations package %q: %w", dir, err)
	}
	return out, nil
}

// moduleRoot returns the directory of the current module's go.mod.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("locating module: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("not in a Go module: run from your project so its migrations resolve")
	}
	return filepath.Dir(gomod), nil
}

func goList(format, dir string) (string, error) {
	cmd := exec.Command("go", "list", "-f", format, dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func filepathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

type bridgeData struct {
	ImportPath string
	Registry   string
	Driver     string
	DriverPkg  string
}

func renderBridge(d bridgeData) ([]byte, error) {
	var buf bytes.Buffer
	if err := bridgeTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var bridgeTmpl = template.Must(template.New("bridge").Parse(`// Code generated by sqlkit migrate; DO NOT EDIT.
package main

import (
	"context"
	"log"
	"os"

	_ {{printf "%q" .DriverPkg}}

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/migrate"
	usermigrations {{printf "%q" .ImportPath}}
)

func main() {
	db, err := sqlkit.Open({{printf "%q" .Driver}}, os.Getenv("SQLKIT_MIGRATE_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.SQL().Close()

	m := usermigrations.{{.Registry}}.NewMigrator(db, migrate.WithSchema(os.Getenv("SQLKIT_MIGRATE_SCHEMA")))
	if err := migrate.NewCLI().Dispatch(m.Runner(context.Background()), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
`))
