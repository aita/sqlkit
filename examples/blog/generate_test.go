package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGenerateMatchesCommittedBindings runs the generate command end-to-end
// against the blog catalog the same way the //go:generate directive does, and
// checks it reproduces the committed bindings byte for byte. It lives in the
// examples module because the sqlkit command is a separate module (its database
// drivers are kept out of the root module that library consumers import) and is
// wired here as a tool dependency; running it as a subprocess is exactly what
// `go generate ./...` does.
func TestGenerateMatchesCommittedBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go run-based end-to-end test in -short mode")
	}
	out := t.TempDir()
	cmd := exec.Command("go", "run", "github.com/aita/sqlkit/cmd/sqlkit",
		"generate", "./schema",
		"--name", "blogdb",
		"--out", out,
		"--relations", // matches the example's //go:generate directive
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(out, "tables.go"))
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("blogdb", "tables.go"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("generated bindings differ from blogdb/tables.go\n--- got ---\n%s", got)
	}
}
