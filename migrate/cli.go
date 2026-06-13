package migrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"
)

// WithOutput sets where the CLI writes its output (default os.Stdout).
func WithOutput(w io.Writer) Option {
	return func(m *Migrator) { m.out = w }
}

func (m *Migrator) output() io.Writer {
	if m.out != nil {
		return m.out
	}
	return os.Stdout
}

// RunCLI parses and dispatches a migration command against the migrator. It is
// the engine behind "sqlkit migrate <command>" (the sqlkit binary compiles the
// project's migrations into a small program that calls this), and a program can
// call it directly to ship its own binary:
//
//	m := migrations.Registry.NewMigrator(db)
//	err := m.RunCLI(context.Background(), os.Args[1:])
//
// Commands: up, down, stamp, status, pending, current, heads, history,
// sql up|down, and new.
func (m *Migrator) RunCLI(ctx context.Context, args []string) error {
	var cli migrateCLI
	parser, err := kong.New(&cli,
		kong.Name("migrate"),
		kong.Description("Run and scaffold schema migrations."),
		kong.Writers(m.output(), m.output()),
	)
	if err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run(&cliRunner{m: m, ctx: ctx})
}

// cliRunner carries the migrator and context to each subcommand's Run method.
type cliRunner struct {
	m   *Migrator
	ctx context.Context
}

// migrateCLI is the kong grammar for RunCLI's commands.
type migrateCLI struct {
	Up      upCmd      `cmd:"" help:"Apply migrations up to a target (default head)."`
	Down    downCmd    `cmd:"" help:"Roll back migrations to a target (default the most recent)."`
	Stamp   stampCmd   `cmd:"" help:"Mark a revision applied without running it."`
	Status  statusCmd  `cmd:"" help:"Show each migration's applied state."`
	Pending pendingCmd `cmd:"" help:"List the unapplied migrations."`
	Current currentCmd `cmd:"" help:"Show the revision(s) the database is at."`
	Heads   headsCmd   `cmd:"" help:"Show the head revision(s)."`
	History historyCmd `cmd:"" help:"Show the apply/rollback timeline."`
	SQL     sqlCmd     `cmd:"" name:"sql" help:"Render migration SQL without running it."`
	New     newCmd     `cmd:"" help:"Scaffold a new migration file."`
}

type upCmd struct {
	Target string `arg:"" optional:"" default:"head" help:"head, +N, or a revision id/prefix."`
}

func (c *upCmd) Run(r *cliRunner) error { return r.m.Upgrade(r.ctx, c.Target) }

type downCmd struct {
	Target string `arg:"" optional:"" default:"-1" help:"base, -N, or a revision id/prefix."`
}

func (c *downCmd) Run(r *cliRunner) error { return r.m.Downgrade(r.ctx, c.Target) }

type stampCmd struct {
	Revision string `arg:"" help:"Revision to mark applied (or head/base)."`
}

func (c *stampCmd) Run(r *cliRunner) error { return r.m.Stamp(r.ctx, c.Revision) }

type statusCmd struct{}

func (c *statusCmd) Run(r *cliRunner) error {
	status, err := r.m.Status(r.ctx)
	if err != nil {
		return err
	}
	for _, s := range status {
		mark := "pending"
		if s.Applied {
			mark = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(r.m.output(), "%-12s %-20s %s\n", s.Revision, s.Name, mark)
	}
	return nil
}

type pendingCmd struct{}

func (c *pendingCmd) Run(r *cliRunner) error {
	pending, err := r.m.Pending(r.ctx)
	if err != nil {
		return err
	}
	for _, mig := range pending {
		fmt.Fprintf(r.m.output(), "%-12s %s\n", mig.Revision, mig.Name)
	}
	return nil
}

type currentCmd struct{}

func (c *currentCmd) Run(r *cliRunner) error {
	cur, err := r.m.Current(r.ctx)
	if err != nil {
		return err
	}
	for _, rev := range cur {
		fmt.Fprintln(r.m.output(), rev)
	}
	return nil
}

type headsCmd struct{}

func (c *headsCmd) Run(r *cliRunner) error {
	for _, h := range r.m.Heads() {
		fmt.Fprintln(r.m.output(), h)
	}
	return r.m.planErr
}

type historyCmd struct{}

func (c *historyCmd) Run(r *cliRunner) error {
	events, err := r.m.History(r.ctx)
	if err != nil {
		return err
	}
	for _, e := range events {
		fmt.Fprintf(r.m.output(), "%-4s %-12s %-20s %s\n",
			e.Direction, e.Revision, e.Name, e.AppliedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

type sqlCmd struct {
	Direction string `arg:"" enum:"up,down" help:"up or down."`
	From      string `arg:"" optional:"" help:"Start revision (defaults: up=base, down=head)."`
	To        string `arg:"" optional:"" help:"End revision (defaults: up=head, down=base)."`
}

func (c *sqlCmd) Run(r *cliRunner) error {
	var (
		script string
		err    error
	)
	switch c.Direction {
	case "up":
		script, err = r.m.UpgradeSQL(c.From, orDefault(c.To, "head"))
	case "down":
		script, err = r.m.DowngradeSQL(orDefault(c.From, "head"), orDefault(c.To, "base"))
	default:
		return fmt.Errorf("migrate: sql direction must be up or down, got %q", c.Direction)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(r.m.output(), script)
	return nil
}

type newCmd struct {
	Name    string `arg:"" help:"Human-readable migration name (e.g. \"create users\")."`
	Dir     string `help:"Directory holding the migration files." default:"migrations"`
	Package string `help:"Package name for the generated file." default:"migrations"`
	Parent  string `help:"Down revision to build on (defaults to the current head)."`
	Merge   bool   `help:"Create a merge migration that joins all current heads."`
}

func (c *newCmd) Run(r *cliRunner) error {
	if r.m.planErr != nil {
		return r.m.planErr
	}
	downField, err := downRevisionField(r.m.Heads(), c.Parent, c.Merge)
	if err != nil {
		return err
	}
	path, revision, err := writeScaffold(c.Dir, c.Package, c.Name, downField)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.m.output(), "created %s (revision %s)\n", path, revision)
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// downRevisionField renders the DownRevision/DownRevisions struct field for a
// scaffold, choosing the parent from the heads (or parent/merge).
func downRevisionField(heads []string, parent string, merge bool) (string, error) {
	switch {
	case merge:
		if len(heads) == 0 {
			return "", fmt.Errorf("migrate: --merge needs existing migrations")
		}
		return fmt.Sprintf("\tDownRevisions: %#v,", heads), nil
	case parent != "":
		return fmt.Sprintf("\tDownRevision: %q,", parent), nil
	case len(heads) == 0:
		return "\tDownRevision: \"\",", nil
	case len(heads) == 1:
		return fmt.Sprintf("\tDownRevision: %q,", heads[0]), nil
	default:
		return "", fmt.Errorf("migrate: multiple heads %v; pass --parent <rev> or --merge", heads)
	}
}

// writeScaffold writes a migration file with a fresh revision and the given
// rendered DownRevision/DownRevisions field, returning the path and revision.
func writeScaffold(dir, pkg, name, downField string) (path, revision string, err error) {
	revision = NewRevision()
	content := fmt.Sprintf(scaffoldTemplate, pkg, revision, downField, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	filename := time.Now().UTC().Format("20060102150405") + "_" + slug(name) + ".go"
	path = filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", "", err
	}
	return path, revision, nil
}

const scaffoldTemplate = `package %s

import "github.com/aita/sqlkit/migrate"

var _ = Registry.Add(migrate.Migration{
	Revision:     %q,
%s
	Name:         %q,
	Up: func(op *migrate.Operator) {
		// TODO: implement
	},
	Down: func(op *migrate.Operator) {
		// TODO: implement
	},
})
`

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
