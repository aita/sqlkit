package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A catalog is a runtime Go value (foreign keys reference the meta.Table values
// returned by Table), so it can't be read statically. The generate and ddl
// subcommands therefore work the same way: write a throwaway main package that
// imports the catalog package and calls into the codegen/ddl libraries, then
// run it with `go run` inside the main module (writable, and able to import the
// catalog) so its imports resolve. This file holds the machinery both share.

// goPackage is the subset of `go list -json` output we need to locate a catalog
// package and the module it lives in.
type goPackage struct {
	ImportPath string
	Dir        string
	Module     *struct {
		Path string
		Dir  string
	}
}

// resolvePackage runs `go list` to canonicalize a package pattern (import path
// or ./dir) and find the module that contains it, so the shim can be placed
// in-module and import the package by its canonical path.
func resolvePackage(pattern string) (goPackage, error) {
	cmd := exec.Command("go", "list", "-json", pattern)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return goPackage{}, fmt.Errorf("go list %s: %w", pattern, err)
	}
	var pkg goPackage
	// A single pattern yields one JSON object; decode just the first.
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&pkg); err != nil {
		return goPackage{}, fmt.Errorf("parse go list output for %s: %w", pattern, err)
	}
	if pkg.Module == nil || pkg.Module.Dir == "" {
		return goPackage{}, fmt.Errorf("package %s is not in a Go module", pattern)
	}
	return pkg, nil
}

// formatShim gofmts rendered shim source, surfacing the unformatted text on
// error so template bugs are easy to spot.
func formatShim(buf *bytes.Buffer) ([]byte, error) {
	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generator shim: %w\n%s", err, buf.String())
	}
	return src, nil
}

// mainModuleDir returns the directory of the main module for the current
// working directory. The shim lives here rather than in the catalog package's
// own module: that module may be a downloaded dependency, whose cache directory
// Go makes read-only, while the main module (the user's project, which already
// requires the catalog package) is writable and can import it.
func mainModuleDir() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("sqlkit must be run inside a Go module")
	}
	return filepath.Dir(gomod), nil
}

// runShim writes src as a throwaway main package inside the main module, runs it
// with `go run`, and removes it afterward. desc names the work for error
// messages (e.g. "generate bindings from path.Var").
func runShim(src []byte, desc string) error {
	moduleDir, err := mainModuleDir()
	if err != nil {
		return err
	}
	return runShimInDir(moduleDir, src, desc, os.Stdout)
}

// runShimCapture runs a shim in the main module and returns its stdout, for
// shims that emit data (a schema snapshot, the migration heads) rather than
// write files themselves.
func runShimCapture(src []byte, desc string) ([]byte, error) {
	moduleDir, err := mainModuleDir()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := runShimInDir(moduleDir, src, desc, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// runShimInDir writes src as a throwaway main package inside moduleDir (a Go
// module directory), runs it with `go run`, directs its stdout to out, and
// removes it afterward. moduleDir lets a shim run inside a git worktree to build
// an older revision of a catalog.
func runShimInDir(moduleDir string, src []byte, desc string, out io.Writer) error {
	shimDir, err := os.MkdirTemp(moduleDir, "sqlkit-shim-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(shimDir) //nolint:errcheck // best-effort temp cleanup

	if err := os.WriteFile(filepath.Join(shimDir, "main.go"), src, 0o644); err != nil {
		return err
	}

	run := exec.Command("go", "run", ".")
	run.Dir = shimDir
	run.Stdout = out
	run.Stderr = os.Stderr
	if err := run.Run(); err != nil {
		return fmt.Errorf("%s: %w", desc, err)
	}
	return nil
}
