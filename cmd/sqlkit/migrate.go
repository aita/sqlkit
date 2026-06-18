package main

// migrateCmd groups the migration subcommands. Every command needs the project's
// migrations, which are Go code, so each compiles the migrations package on the
// fly and dispatches to migrate.NewCLI().Dispatch inside it — see bridge.go. "new" only
// reads the migration set to scaffold a file, so it does not touch the database.
type migrateCmd struct {
	New          migrateNewCmd   `cmd:"" help:"Scaffold a new migration file whose DownRevision is the current head."`
	Autogenerate autogenerateCmd `cmd:"" help:"Generate a migration by diffing a source schema (a live DB or a git revision) against the target catalog."`

	Up      migrateUpCmd      `cmd:"" help:"Apply migrations up to a target (default head)."`
	Down    migrateDownCmd    `cmd:"" help:"Roll back migrations to a target (default the most recent)."`
	Status  migrateStatusCmd  `cmd:"" help:"Show each migration's applied state."`
	Pending migratePendingCmd `cmd:"" help:"List the unapplied migrations."`
	Current migrateCurrentCmd `cmd:"" help:"Show the revision(s) the database is at."`
	Heads   migrateHeadsCmd   `cmd:"" help:"Show the head revision(s)."`
	History migrateHistoryCmd `cmd:"" help:"Show the apply/rollback timeline."`
	Stamp   migrateStampCmd   `cmd:"" help:"Mark a revision applied without running it."`
	SQL     migrateSQLCmd     `cmd:"" name:"sql" help:"Render migration SQL without running it."`
}

// migrateNewCmd scaffolds a new migration .go file whose DownRevision links onto
// the current head, read from the compiled migrations package. It needs no
// database.
type migrateNewCmd struct {
	bridgeFlags `embed:""`
	Name        string `arg:"" help:"Human-readable migration name (e.g. \"create users\")."`
	Dir         string `help:"Directory to write the generated file." default:"migrations"`
	Package     string `help:"Package name for the generated file." default:"migrations"`
	Parent      string `help:"Down revision to build on (defaults to the current head)."`
	Merge       bool   `help:"Create a merge migration that joins all current heads."`
}

func (c *migrateNewCmd) Run() error {
	args := []string{"new", c.Name, "--dir", c.Dir, "--package", c.Package}
	if c.Parent != "" {
		args = append(args, "--parent", c.Parent)
	}
	if c.Merge {
		args = append(args, "--merge")
	}
	return c.run(args...)
}

type migrateUpCmd struct {
	bridgeFlags `embed:""`
	Target      string `arg:"" optional:"" default:"head" help:"head, +N, or a revision id/prefix."`
}

func (c *migrateUpCmd) Run() error { return c.run("up", c.Target) }

type migrateDownCmd struct {
	bridgeFlags `embed:""`
	Target      string `arg:"" optional:"" default:"-1" help:"base, -N, or a revision id/prefix."`
}

func (c *migrateDownCmd) Run() error { return c.run("down", c.Target) }

type migrateStatusCmd struct {
	bridgeFlags `embed:""`
}

func (c *migrateStatusCmd) Run() error { return c.run("status") }

type migratePendingCmd struct {
	bridgeFlags `embed:""`
}

func (c *migratePendingCmd) Run() error { return c.run("pending") }

type migrateCurrentCmd struct {
	bridgeFlags `embed:""`
}

func (c *migrateCurrentCmd) Run() error { return c.run("current") }

type migrateHeadsCmd struct {
	bridgeFlags `embed:""`
}

func (c *migrateHeadsCmd) Run() error { return c.run("heads") }

type migrateHistoryCmd struct {
	bridgeFlags `embed:""`
}

func (c *migrateHistoryCmd) Run() error { return c.run("history") }

type migrateStampCmd struct {
	bridgeFlags `embed:""`
	Revision    string `arg:"" help:"Revision to mark applied (or head/base)."`
}

func (c *migrateStampCmd) Run() error { return c.run("stamp", c.Revision) }

type migrateSQLCmd struct {
	bridgeFlags `embed:""`
	Direction   string `arg:"" enum:"up,down" help:"up or down."`
	From        string `arg:"" optional:"" help:"Start revision."`
	To          string `arg:"" optional:"" help:"End revision."`
}

func (c *migrateSQLCmd) Run() error {
	args := []string{"sql", c.Direction}
	if c.From != "" {
		args = append(args, c.From)
	}
	if c.To != "" {
		args = append(args, c.To)
	}
	return c.run(args...)
}
