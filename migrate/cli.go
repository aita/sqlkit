package migrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// Runner carries the migrator and context to each subcommand's Run method. It
// is the binding a CLI driver hands to the parsed command — pass it to kong's
// kctx.Run, or to CLI.Dispatch.
type Runner struct {
	m   *Migrator
	ctx context.Context
}

// Runner returns the binding that CLI commands run against. Hand it to a parsed
// command, e.g. kong's kctx.Run(m.Runner(ctx)) or CLI.Dispatch(m.Runner(ctx), …).
func (m *Migrator) Runner(ctx context.Context) *Runner {
	return &Runner{m: m, ctx: ctx}
}

// CLI is the command grammar for running and scaffolding migrations. Its struct
// tags describe the commands to a CLI framework without this package depending
// on one: build it with NewCLI, hand it to kong (kong.New(migrate.NewCLI())),
// and run the parsed command against m.Runner(ctx). To ship a migration binary
// without pulling in kong, use CLI.Dispatch instead.
//
// Commands: up, down, stamp, status, pending, current, heads, history,
// sql up|down, and new.
type CLI struct {
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

// NewCLI returns a fresh command grammar to hand to a CLI parser. The caller
// owns the parser (and its dependency): e.g.
//
//	cli := migrate.NewCLI()
//	parser, _ := kong.New(cli, kong.Name("migrate"))
//	kctx, _ := parser.Parse(os.Args[1:])
//	err := kctx.Run(m.Runner(ctx))
func NewCLI() *CLI { return &CLI{} }

// Dispatch parses args and runs the matching command against r, without any
// third-party CLI dependency. It accepts the same command set as the kong
// grammar (up, down, stamp, status, pending, current, heads, history,
// sql up|down, new) and is what a shipped binary or the sqlkit migrate bridge
// calls so neither needs kong:
//
//	err := migrate.NewCLI().Dispatch(m.Runner(ctx), os.Args[1:])
func (c *CLI) Dispatch(r *Runner, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("migrate: no command; expected one of %s", strings.Join(commandNames, ", "))
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "up":
		c.Up.Target = argOr(rest, 0, "head")
		return c.Up.Run(r)
	case "down":
		c.Down.Target = argOr(rest, 0, "-1")
		return c.Down.Run(r)
	case "stamp":
		if len(rest) == 0 {
			return fmt.Errorf("migrate: stamp needs a revision")
		}
		c.Stamp.Revision = rest[0]
		return c.Stamp.Run(r)
	case "status":
		return c.Status.Run(r)
	case "pending":
		return c.Pending.Run(r)
	case "current":
		return c.Current.Run(r)
	case "heads":
		return c.Heads.Run(r)
	case "history":
		return c.History.Run(r)
	case "sql":
		if len(rest) == 0 || (rest[0] != "up" && rest[0] != "down") {
			return fmt.Errorf("migrate: sql direction must be up or down")
		}
		c.SQL.Direction = rest[0]
		c.SQL.From = argOr(rest, 1, "")
		c.SQL.To = argOr(rest, 2, "")
		return c.SQL.Run(r)
	case "new":
		if err := parseNew(&c.New, rest); err != nil {
			return err
		}
		return c.New.Run(r)
	default:
		return fmt.Errorf("migrate: unknown command %q; expected one of %s", cmd, strings.Join(commandNames, ", "))
	}
}

var commandNames = []string{"up", "down", "stamp", "status", "pending", "current", "heads", "history", "sql", "new"}

// argOr returns args[i] if present, else def.
func argOr(args []string, i int, def string) string {
	if i < len(args) {
		return args[i]
	}
	return def
}

// parseNew fills the new-migration command from "<name> [--dir D] [--package P]
// [--parent X] [--merge]" — the flags the sqlkit bridge passes through. Both
// "--flag value" and "--flag=value" forms are accepted.
func parseNew(c *newCmd, args []string) error {
	c.Dir, c.Package = "migrations", "migrations"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			name, val, hasEq := strings.Cut(a, "=")
			if name == "--merge" {
				c.Merge = true
				if hasEq {
					b, err := strconv.ParseBool(val)
					if err != nil {
						return fmt.Errorf("migrate: invalid --merge value %q", val)
					}
					c.Merge = b
				}
				continue
			}
			if !hasEq {
				if i+1 >= len(args) {
					return fmt.Errorf("migrate: %s needs a value", name)
				}
				i++
				val = args[i]
			}
			switch name {
			case "--dir":
				c.Dir = val
			case "--package":
				c.Package = val
			case "--parent":
				c.Parent = val
			default:
				return fmt.Errorf("migrate: unknown flag %q for new", name)
			}
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("migrate: unknown flag %q for new", a)
		case c.Name == "":
			c.Name = a
		default:
			return fmt.Errorf("migrate: unexpected argument %q for new", a)
		}
	}
	if c.Name == "" {
		return fmt.Errorf("migrate: new needs a migration name")
	}
	return nil
}

type upCmd struct {
	Target string `arg:"" optional:"" default:"head" help:"head, +N, or a revision id/prefix."`
}

func (c *upCmd) Run(r *Runner) error { return r.m.Upgrade(r.ctx, c.Target) }

type downCmd struct {
	Target string `arg:"" optional:"" default:"-1" help:"base, -N, or a revision id/prefix."`
}

func (c *downCmd) Run(r *Runner) error { return r.m.Downgrade(r.ctx, c.Target) }

type stampCmd struct {
	Revision string `arg:"" help:"Revision to mark applied (or head/base)."`
}

func (c *stampCmd) Run(r *Runner) error { return r.m.Stamp(r.ctx, c.Revision) }

type statusCmd struct{}

func (c *statusCmd) Run(r *Runner) error {
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

func (c *pendingCmd) Run(r *Runner) error {
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

func (c *currentCmd) Run(r *Runner) error {
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

func (c *headsCmd) Run(r *Runner) error {
	for _, h := range r.m.Heads() {
		fmt.Fprintln(r.m.output(), h)
	}
	return r.m.planErr
}

type historyCmd struct{}

func (c *historyCmd) Run(r *Runner) error {
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

func (c *sqlCmd) Run(r *Runner) error {
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

func (c *newCmd) Run(r *Runner) error {
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
