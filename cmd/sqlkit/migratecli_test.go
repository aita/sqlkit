package main

import (
	"testing"

	"github.com/alecthomas/kong"

	"github.com/aita/sqlkit/migrate"
)

// TestMigrateCLIGrammarParses ensures migrate.NewCLI()'s struct tags form a
// valid kong grammar and every subcommand parses. The kong path is what
// ship-your-own-binary programs (and examples/migrate) use; the sqlkit bridge
// goes through CLI.Dispatch instead, so without this test the grammar tags are
// only exercised at runtime.
func TestMigrateCLIGrammarParses(t *testing.T) {
	parser, err := kong.New(migrate.NewCLI(),
		kong.Name("migrate"),
		kong.Exit(func(int) {}),
	)
	if err != nil {
		t.Fatalf("kong.New(migrate.NewCLI()): %v", err)
	}

	// Targets that begin with "-" (e.g. "down -1") are a kong limitation, not a
	// grammar bug — its default handles the common case — so they are omitted.
	for _, args := range [][]string{
		{"up"}, {"up", "+1"},
		{"down"},
		{"stamp", "head"},
		{"status"}, {"pending"}, {"current"}, {"heads"}, {"history"},
		{"sql", "up"}, {"sql", "down", "head", "base"},
		{"new", "add widgets"},
		{"new", "merge it", "--merge"},
	} {
		if _, err := parser.Parse(args); err != nil {
			t.Errorf("parse %v: %v", args, err)
		}
	}
}
