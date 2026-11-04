package migrate

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// sqlRecorder is an Execer that compiles statements to SQL text and collects
// them instead of running them — the backend of the offline UpgradeSQL/
// DowngradeSQL rendering.
type sqlRecorder struct {
	dialect sql.Dialect
	stmts   []string
}

func (r *sqlRecorder) Exec(_ context.Context, q sql.Query) (stdsql.Result, error) {
	text, _, err := q.ToSQL(r.dialect)
	if err != nil {
		return nil, err
	}
	if text != "" {
		r.stmts = append(r.stmts, text)
	}
	return nil, nil
}

func (r *sqlRecorder) String() string { return strings.Join(r.stmts, "\n") }

// defaultTableName is the tracking table the Migrator records history in.
const defaultTableName = "schema_migrations"

// Migrator applies a set of migrations to a database and records their history.
// Migrations form a DAG linked by Revision/DownRevision(s); the Migrator
// resolves the apply order topologically. Create one with New.
type Migrator struct {
	db      *sqlkit.Database
	table   sql.Table
	schema  string // default schema for migration operations
	order   []Migration
	byRev   map[string]Migration
	planErr error
	out     io.Writer // CLI output (nil means os.Stdout)
}

// Option configures a Migrator.
type Option func(*Migrator)

// WithTableName overrides the tracking table name (default "schema_migrations").
func WithTableName(name string) Option {
	return func(m *Migrator) { m.table.Name = sql.NewIdent(name) }
}

// WithSchema sets the schema for both the tracking table and the tables a
// migration's operations create or alter.
func WithSchema(schema string) Option {
	return func(m *Migrator) {
		m.table.Schema = sql.NewIdent(schema)
		m.schema = schema
	}
}

// New creates a Migrator for the database and migrations. The migrations'
// Revision/DownRevision links are resolved into a topological apply order, so
// the caller's slice order does not matter. Configuration problems (duplicate or
// dangling revisions, cycles) are reported by the first operation.
func New(db *sqlkit.Database, migrations []Migration, opts ...Option) *Migrator {
	order, byRev, err := plan(migrations)
	m := &Migrator{
		db:      db,
		table:   sql.Table{Name: sql.NewIdent(defaultTableName)},
		order:   order,
		byRev:   byRev,
		planErr: err,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// NewMigrator creates a Migrator for the database from the registry's
// migrations. It is shorthand for New(db, registry.Migrations(), opts...).
func (r *Registry) NewMigrator(db *sqlkit.Database, opts ...Option) *Migrator {
	return New(db, r.Migrations(), opts...)
}

// plan validates the migration DAG and returns the migrations in topological
// (parents-before-child) order, plus a revision lookup.
func plan(migrations []Migration) ([]Migration, map[string]Migration, error) {
	byRev := make(map[string]Migration, len(migrations))
	for _, mig := range migrations {
		if mig.Revision == "" {
			return nil, nil, fmt.Errorf("migrate: migration %q has an empty revision", mig.Name)
		}
		if _, dup := byRev[mig.Revision]; dup {
			return nil, nil, fmt.Errorf("migrate: duplicate revision %q", mig.Revision)
		}
		byRev[mig.Revision] = mig
	}

	children := map[string][]string{}
	indegree := map[string]int{}
	for _, mig := range migrations {
		ps := mig.parents()
		indegree[mig.Revision] = len(ps)
		for _, p := range ps {
			if _, ok := byRev[p]; !ok {
				return nil, nil, fmt.Errorf("migrate: revision %q has unknown down revision %q", mig.Revision, p)
			}
			children[p] = append(children[p], mig.Revision)
		}
	}
	for k := range children {
		sort.Strings(children[k])
	}

	// Kahn's algorithm with a sorted ready set for deterministic order.
	var ready []string
	for _, mig := range migrations {
		if indegree[mig.Revision] == 0 {
			ready = append(ready, mig.Revision)
		}
	}
	sort.Strings(ready)

	var order []Migration
	for len(ready) > 0 {
		rev := ready[0]
		ready = ready[1:]
		order = append(order, byRev[rev])
		for _, child := range children[rev] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = insertSorted(ready, child)
			}
		}
	}
	if len(order) != len(migrations) {
		return nil, nil, fmt.Errorf("migrate: migrations contain a cycle")
	}
	return order, byRev, nil
}

func insertSorted(s []string, v string) []string {
	i := sort.SearchStrings(s, v)
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// ancestors returns the inclusive ancestor set of rev (rev plus everything it
// transitively builds on).
func (m *Migrator) ancestors(rev string) map[string]bool {
	set := map[string]bool{}
	var walk func(string)
	walk = func(r string) {
		if set[r] {
			return
		}
		mig, ok := m.byRev[r]
		if !ok {
			return
		}
		set[r] = true
		for _, p := range mig.parents() {
			walk(p)
		}
	}
	walk(rev)
	return set
}

// descendants returns every revision that has rev as an ancestor (excluding rev).
func (m *Migrator) descendants(rev string) map[string]bool {
	set := map[string]bool{}
	for _, mig := range m.order {
		if mig.Revision == rev {
			continue
		}
		if m.ancestors(mig.Revision)[rev] {
			set[mig.Revision] = true
		}
	}
	return set
}

// Heads returns the revisions no other migration builds on (the tips of the DAG).
func (m *Migrator) Heads() []string {
	isParent := map[string]bool{}
	for _, mig := range m.order {
		for _, p := range mig.parents() {
			isParent[p] = true
		}
	}
	var heads []string
	for _, mig := range m.order {
		if !isParent[mig.Revision] {
			heads = append(heads, mig.Revision)
		}
	}
	sort.Strings(heads)
	return heads
}

// Current returns the applied revisions that no applied migration builds on —
// the revision(s) the database is currently at.
func (m *Migrator) Current(ctx context.Context) ([]string, error) {
	if m.planErr != nil {
		return nil, m.planErr
	}
	if err := m.ensureTable(ctx); err != nil {
		return nil, err
	}
	applied, err := m.appliedRevisions(ctx)
	if err != nil {
		return nil, err
	}
	hasAppliedChild := map[string]bool{}
	for _, mig := range m.order {
		if !applied[mig.Revision] {
			continue
		}
		for _, p := range mig.parents() {
			hasAppliedChild[p] = true
		}
	}
	var cur []string
	for _, mig := range m.order {
		if applied[mig.Revision] && !hasAppliedChild[mig.Revision] {
			cur = append(cur, mig.Revision)
		}
	}
	sort.Strings(cur)
	return cur, nil
}

// MigrationStatus is the applied state of one migration, returned by Status.
type MigrationStatus struct {
	Revision     string
	DownRevision string
	Name         string
	Applied      bool
	AppliedAt    time.Time // zero when not applied
}

// Up applies every pending migration in topological order (to all heads).
func (m *Migrator) Up(ctx context.Context) error { return m.Upgrade(ctx, "head") }

// UpTo applies the target revision and any of its unapplied ancestors.
func (m *Migrator) UpTo(ctx context.Context, revision string) error {
	return m.Upgrade(ctx, revision)
}

// Down rolls back the most recently applied migration.
func (m *Migrator) Down(ctx context.Context) error { return m.Downgrade(ctx, "-1") }

// DownTo rolls back every applied migration that descends from revision (not
// revision itself). An empty revision rolls back everything.
func (m *Migrator) DownTo(ctx context.Context, revision string) error {
	if revision == "" {
		return m.Downgrade(ctx, "base")
	}
	return m.Downgrade(ctx, revision)
}

// Upgrade applies migrations toward a target, which may be:
//   - "head" / "heads" / "" — every pending migration;
//   - "+N" — the next N pending migrations in topological order;
//   - a revision id (or unique prefix) — that revision and its unapplied ancestors.
//
// On PostgreSQL and MySQL it holds the migration lock for the run, so
// concurrent Migrators on the same tracking table apply each migration once.
func (m *Migrator) Upgrade(ctx context.Context, target string) error {
	if m.planErr != nil {
		return m.planErr
	}
	return m.withLock(ctx, func(m *Migrator) error { return m.upgrade(ctx, target) })
}

func (m *Migrator) upgrade(ctx context.Context, target string) error {
	if err := m.ensureTable(ctx); err != nil {
		return err
	}
	applied, err := m.appliedRevisions(ctx)
	if err != nil {
		return err
	}

	switch {
	case target == "" || target == "head" || target == "heads":
		return m.applyPending(ctx, applied, nil, -1)
	case strings.HasPrefix(target, "+"):
		n, err := strconv.Atoi(target[1:])
		if err != nil || n < 0 {
			return fmt.Errorf("migrate: invalid relative target %q", target)
		}
		return m.applyPending(ctx, applied, nil, n)
	default:
		rev, err := m.resolve(target)
		if err != nil {
			return err
		}
		return m.applyPending(ctx, applied, m.ancestors(rev), -1)
	}
}

// applyPending applies pending migrations in topological order. limit, when
// non-nil, restricts to its revisions; max, when >= 0, caps how many are applied.
func (m *Migrator) applyPending(ctx context.Context, applied map[string]bool, limit map[string]bool, max int) error {
	count := 0
	for _, mig := range m.order {
		if max >= 0 && count >= max {
			break
		}
		if limit != nil && !limit[mig.Revision] {
			continue
		}
		if applied[mig.Revision] {
			continue
		}
		if err := m.applyUp(ctx, mig); err != nil {
			return fmt.Errorf("migrate: applying %s (%s): %w", mig.Revision, mig.Name, err)
		}
		count++
	}
	return nil
}

// Downgrade rolls migrations back toward a target, which may be:
//   - "base" / "" — every applied migration;
//   - "-N" — the N most recently applied migrations;
//   - a revision id (or unique prefix) — every applied descendant of it (the
//     database ends up at that revision).
//
// Like Upgrade, it holds the migration lock for the run.
func (m *Migrator) Downgrade(ctx context.Context, target string) error {
	if m.planErr != nil {
		return m.planErr
	}
	return m.withLock(ctx, func(m *Migrator) error { return m.downgrade(ctx, target) })
}

func (m *Migrator) downgrade(ctx context.Context, target string) error {
	if err := m.ensureTable(ctx); err != nil {
		return err
	}
	events, err := m.historyRows(ctx)
	if err != nil {
		return err
	}
	applied := appliedFromEvents(events)
	// Refuse to downgrade when the database is at a revision the migrator no
	// longer knows about (its migration was removed from the code). Rolling such
	// a revision back is impossible — its Down is gone — and silently skipping it
	// would report success while leaving the schema untouched. Erroring here
	// matches established tools (Alembic's "Can't locate revision").
	if unknown := m.unknownApplied(applied); len(unknown) > 0 {
		return fmt.Errorf("migrate: cannot downgrade: applied revision(s) %v are not in the migration set", unknown)
	}

	switch {
	case target == "" || target == "base":
		return m.rollback(ctx, applied, nil)
	case strings.HasPrefix(target, "-"):
		n, err := strconv.Atoi(target[1:])
		if err != nil || n < 0 {
			return fmt.Errorf("migrate: invalid relative target %q", target)
		}
		return m.rollbackRecent(ctx, events, n)
	default:
		rev, err := m.resolve(target)
		if err != nil {
			return err
		}
		return m.rollback(ctx, applied, m.descendants(rev))
	}
}

// rollback rolls back applied migrations in reverse topological order. limit,
// when non-nil, restricts to its revisions.
func (m *Migrator) rollback(ctx context.Context, applied map[string]bool, limit map[string]bool) error {
	for i := len(m.order) - 1; i >= 0; i-- {
		mig := m.order[i]
		if limit != nil && !limit[mig.Revision] {
			continue
		}
		if !applied[mig.Revision] {
			continue
		}
		if err := m.applyDown(ctx, mig); err != nil {
			return fmt.Errorf("migrate: rolling back %s (%s): %w", mig.Revision, mig.Name, err)
		}
	}
	return nil
}

// rollbackRecent rolls back the n most recently applied migrations, ordered by
// the history (most recent first) rather than by topological position — so
// Down/"-N" undoes what was actually applied last, even across independent
// branches. Newest-first is dependency-safe: a child is always applied after its
// parents, so it is rolled back before them.
func (m *Migrator) rollbackRecent(ctx context.Context, events []MigrationEvent, n int) error {
	recent := recentlyApplied(events)
	if n > len(recent) {
		n = len(recent)
	}
	for _, rev := range recent[:n] {
		mig, ok := m.byRev[rev]
		if !ok {
			// Guarded against by Downgrade's unknownApplied check; kept defensive.
			return fmt.Errorf("migrate: cannot roll back revision %q: not in the migration set", rev)
		}
		if err := m.applyDown(ctx, mig); err != nil {
			return fmt.Errorf("migrate: rolling back %s (%s): %w", mig.Revision, mig.Name, err)
		}
	}
	return nil
}

// recentlyApplied returns the currently-applied revisions ordered by when they
// were last applied, most recent first.
func recentlyApplied(events []MigrationEvent) []string {
	lastUp := map[string]int{}
	latest := map[string]Direction{}
	for i, e := range events {
		latest[e.Revision] = e.Direction
		if e.Direction == DirectionUp {
			lastUp[e.Revision] = i
		}
	}
	var revs []string
	for rev, dir := range latest {
		if dir == DirectionUp {
			revs = append(revs, rev)
		}
	}
	sort.Slice(revs, func(a, b int) bool { return lastUp[revs[a]] > lastUp[revs[b]] })
	return revs
}

// resolve turns a target into a concrete revision: an exact id, or a unique
// prefix of one.
func (m *Migrator) resolve(target string) (string, error) {
	if _, ok := m.byRev[target]; ok {
		return target, nil
	}
	var matches []string
	for rev := range m.byRev {
		if strings.HasPrefix(rev, target) {
			matches = append(matches, rev)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("migrate: unknown revision %q", target)
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("migrate: ambiguous revision %q matches %v", target, matches)
	}
}

// Stamp records the database as being at revision without running any migration —
// for baselining an existing schema. It writes history rows so the applied set
// becomes exactly the inclusive ancestors of revision: "head" stamps everything,
// "base"/"" stamps nothing applied. Migrations newly considered applied get an
// "up" row; ones no longer in range get a "down" row. No DDL runs.
//
// Like Upgrade, it holds the migration lock while it writes.
func (m *Migrator) Stamp(ctx context.Context, revision string) error {
	if m.planErr != nil {
		return m.planErr
	}
	return m.withLock(ctx, func(m *Migrator) error { return m.stamp(ctx, revision) })
}

func (m *Migrator) stamp(ctx context.Context, revision string) error {
	if err := m.ensureTable(ctx); err != nil {
		return err
	}
	applied, err := m.appliedRevisions(ctx)
	if err != nil {
		return err
	}

	var want map[string]bool
	switch revision {
	case "base", "":
		want = map[string]bool{}
	case "head", "heads":
		want = map[string]bool{}
		for _, mig := range m.order {
			want[mig.Revision] = true
		}
	default:
		rev, err := m.resolve(revision)
		if err != nil {
			return err
		}
		want = m.ancestors(rev)
	}

	return m.db.WithSession(ctx, func(s *sqlkit.Session) error {
		for _, mig := range m.order { // forward: stamp newly-applied
			if want[mig.Revision] && !applied[mig.Revision] {
				if _, err := s.Exec(ctx, m.recordEvent(mig, DirectionUp)); err != nil {
					return err
				}
			}
		}
		for i := len(m.order) - 1; i >= 0; i-- { // reverse: unstamp out-of-range
			mig := m.order[i]
			if !want[mig.Revision] && applied[mig.Revision] {
				if _, err := s.Exec(ctx, m.recordEvent(mig, DirectionDown)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// UpgradeSQL renders, without touching the database, the SQL that upgrading from
// revision to revision would run (offline mode). from defaults to "base" (""),
// to defaults to "head". Each migration's operations and its history insert are
// included, in topological order.
func (m *Migrator) UpgradeSQL(from, to string) (string, error) {
	if m.planErr != nil {
		return "", m.planErr
	}
	fromSet, err := m.rangeSet(from, true)
	if err != nil {
		return "", err
	}
	toSet, err := m.rangeSet(to, false)
	if err != nil {
		return "", err
	}
	rec := &sqlRecorder{dialect: m.db.Dialect()}
	for _, mig := range m.order {
		if !toSet[mig.Revision] || fromSet[mig.Revision] {
			continue
		}
		if err := m.record(rec, mig, mig.Up, DirectionUp); err != nil {
			return "", err
		}
	}
	return rec.String(), nil
}

// DowngradeSQL renders, without touching the database, the SQL that downgrading
// from revision to revision would run (offline mode). from defaults to "head",
// to defaults to "base" (""). Statements are emitted in reverse topological order.
func (m *Migrator) DowngradeSQL(from, to string) (string, error) {
	if m.planErr != nil {
		return "", m.planErr
	}
	fromDefault := from
	if fromDefault == "" {
		fromDefault = "head"
	}
	fromSet, err := m.rangeSet(fromDefault, false)
	if err != nil {
		return "", err
	}
	toSet, err := m.rangeSet(to, true)
	if err != nil {
		return "", err
	}
	rec := &sqlRecorder{dialect: m.db.Dialect()}
	for i := len(m.order) - 1; i >= 0; i-- {
		mig := m.order[i]
		if !fromSet[mig.Revision] || toSet[mig.Revision] {
			continue
		}
		if mig.Down == nil {
			return "", errIrreversible(mig)
		}
		if err := m.record(rec, mig, mig.Down, DirectionDown); err != nil {
			return "", err
		}
	}
	return rec.String(), nil
}

// rangeSet resolves an offline endpoint to a revision set: "head"/"" -> all (or
// empty when emptyDefault), "base" -> empty, else the inclusive ancestors.
func (m *Migrator) rangeSet(target string, emptyDefault bool) (map[string]bool, error) {
	switch target {
	case "":
		if emptyDefault {
			return map[string]bool{}, nil
		}
		fallthrough
	case "head", "heads":
		all := map[string]bool{}
		for _, mig := range m.order {
			all[mig.Revision] = true
		}
		return all, nil
	case "base":
		return map[string]bool{}, nil
	default:
		rev, err := m.resolve(target)
		if err != nil {
			return nil, err
		}
		return m.ancestors(rev), nil
	}
}

// record runs one direction of a migration against a recorder and appends its
// history event SQL.
func (m *Migrator) record(rec *sqlRecorder, mig Migration, fn func(*Operator), dir Direction) error {
	if fn != nil {
		op := newOperator(context.Background(), rec, m.schema)
		fn(op)
		if err := op.Err(); err != nil {
			return err
		}
	}
	_, err := rec.Exec(context.Background(), m.recordEvent(mig, dir))
	return err
}

// Pending returns the migrations that have not been applied, in topological order.
func (m *Migrator) Pending(ctx context.Context) ([]Migration, error) {
	if m.planErr != nil {
		return nil, m.planErr
	}
	if err := m.ensureTable(ctx); err != nil {
		return nil, err
	}
	applied, err := m.appliedRevisions(ctx)
	if err != nil {
		return nil, err
	}
	var pending []Migration
	for _, mig := range m.order {
		if !applied[mig.Revision] {
			pending = append(pending, mig)
		}
	}
	return pending, nil
}

// Status returns the applied state of every known migration, in topological order.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	if m.planErr != nil {
		return nil, m.planErr
	}
	if err := m.ensureTable(ctx); err != nil {
		return nil, err
	}
	latest, err := m.latestByRevision(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationStatus, 0, len(m.order))
	for _, mig := range m.order {
		e, ok := latest[mig.Revision]
		applied := ok && e.Direction == DirectionUp
		status := MigrationStatus{
			Revision:     mig.Revision,
			DownRevision: mig.DownRevision,
			Name:         mig.Name,
			Applied:      applied,
		}
		if applied {
			status.AppliedAt = e.AppliedAt
		}
		out = append(out, status)
	}
	return out, nil
}

// applyUp applies one migration (its Up callback and an "up" history row), in a
// transaction unless DisableTx is set.
func (m *Migrator) applyUp(ctx context.Context, mig Migration) error {
	return m.apply(ctx, mig, mig.Up, DirectionUp)
}

// applyDown rolls one migration back (its Down callback and a "down" history
// row). The history table is append-only: a rollback records a new event rather
// than deleting the apply, so the full timeline is preserved.
func (m *Migrator) applyDown(ctx context.Context, mig Migration) error {
	if mig.Down == nil {
		return errIrreversible(mig)
	}
	return m.apply(ctx, mig, mig.Down, DirectionDown)
}

// errIrreversible reports a downgrade of a migration that declares no Down.
func errIrreversible(mig Migration) error {
	return fmt.Errorf("migrate: revision %s (%s) is irreversible: it has no Down", mig.Revision, mig.Name)
}

// apply runs one direction of a migration and records its history event,
// wrapping it in a transaction unless DisableTx is set.
func (m *Migrator) apply(ctx context.Context, mig Migration, fn func(*Operator), dir Direction) error {
	run := func(ex Execer) error {
		if fn != nil {
			op := newOperator(ctx, ex, m.schema)
			fn(op)
			if err := op.Err(); err != nil {
				return err
			}
		}
		_, err := ex.Exec(ctx, m.recordEvent(mig, dir))
		return err
	}
	if mig.DisableTx {
		return run(m.db)
	}
	return m.db.WithSession(ctx, func(s *sqlkit.Session) error { return run(s) })
}

// ensureTable creates the history table if it does not exist.
func (m *Migrator) ensureTable(ctx context.Context) error {
	_, err := m.db.Exec(ctx, m.createTableStmt())
	return err
}

// createTableStmt builds the CREATE TABLE IF NOT EXISTS for the history table.
// It is append-only: each apply or rollback inserts a row, so an
// auto-incrementing id orders the timeline and the current state of a revision
// is its most recent event. The timestamp type is resolved for the dialect.
func (m *Migrator) createTableStmt() sql.CreateTableStmt {
	return sql.CreateTable(m.table).
		Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: autoIncrIDType(m.db.Dialect()), NotNull: true, AutoIncrement: true}).
		Column(sql.ColumnDef{Name: sql.NewIdent("revision"), Type: sql.EngineType{Name: "varchar", Length: 255}, NotNull: true}).
		Column(sql.ColumnDef{Name: sql.NewIdent("name"), Type: sql.EngineType{Name: "varchar", Length: 255}, NotNull: true}).
		Column(sql.ColumnDef{Name: sql.NewIdent("direction"), Type: sql.EngineType{Name: "varchar", Length: 4}, NotNull: true}).
		Column(sql.ColumnDef{Name: sql.NewIdent("applied_at"), Type: timestampType(m.db.Dialect()), NotNull: true, Default: sql.Raw("CURRENT_TIMESTAMP")}).
		PrimaryKey("id").
		IfNotExists().
		Stmt()
}

// recordEvent inserts a history row for applying or rolling back a migration.
func (m *Migrator) recordEvent(mig Migration, dir Direction) sql.Query {
	return sql.InsertInto(m.table).
		Columns("revision", "name", "direction").
		Values(mig.Revision, mig.Name, string(dir))
}

// appliedRevisions returns the set of revisions whose latest history event is an
// apply.
func (m *Migrator) appliedRevisions(ctx context.Context) (map[string]bool, error) {
	events, err := m.historyRows(ctx)
	if err != nil {
		return nil, err
	}
	return appliedFromEvents(events), nil
}

// unknownApplied returns the currently-applied revisions the migrator has no
// migration for — history rows whose code has since been removed. They are
// sorted for a deterministic error message.
func (m *Migrator) unknownApplied(applied map[string]bool) []string {
	var unknown []string
	for rev := range applied {
		if _, ok := m.byRev[rev]; !ok {
			unknown = append(unknown, rev)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// appliedFromEvents folds the ordered history into the set of currently-applied
// revisions (a revision is applied when its most recent event is an apply).
func appliedFromEvents(events []MigrationEvent) map[string]bool {
	latest := map[string]Direction{}
	for _, e := range events {
		latest[e.Revision] = e.Direction
	}
	set := make(map[string]bool, len(latest))
	for rev, dir := range latest {
		if dir == DirectionUp {
			set[rev] = true
		}
	}
	return set
}

// latestByRevision returns the most recent history event per revision. Callers
// must have ensured the table exists.
func (m *Migrator) latestByRevision(ctx context.Context) (map[string]MigrationEvent, error) {
	events, err := m.historyRows(ctx)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]MigrationEvent, len(events))
	for _, e := range events {
		latest[e.Revision] = e // ordered oldest first, so last wins
	}
	return latest, nil
}

type eventRow struct {
	Revision  string
	Name      string
	Direction string
	AppliedAt time.Time
}

// History returns every recorded apply/rollback event, oldest first. It is the
// full migration timeline persisted in the database.
func (m *Migrator) History(ctx context.Context) ([]MigrationEvent, error) {
	if err := m.ensureTable(ctx); err != nil {
		return nil, err
	}
	return m.historyRows(ctx)
}

// historyRows reads the history table; callers must have ensured it exists.
func (m *Migrator) historyRows(ctx context.Context) ([]MigrationEvent, error) {
	q := sql.Select(sql.Col("revision"), sql.Col("name"), sql.Col("direction"), sql.Col("applied_at")).
		From(m.table).
		OrderBy(sql.Asc(sql.Col("id")))
	var rows []eventRow
	if err := m.db.RawSQL(ctx, q).Scan(&rows); err != nil {
		return nil, err
	}
	events := make([]MigrationEvent, len(rows))
	for i, r := range rows {
		events[i] = MigrationEvent{
			Revision:  r.Revision,
			Name:      r.Name,
			Direction: Direction(r.Direction),
			AppliedAt: r.AppliedAt,
		}
	}
	return events, nil
}

// timestampType resolves the timestamp engine type for the dialect.
func timestampType(d sql.Dialect) sql.EngineType {
	if name := d.Name(); name == "mysql" || name == "sqlite" || name == "libsql" {
		return sql.EngineType{Name: "datetime"}
	}
	return sql.EngineType{Name: "timestamptz"}
}

// autoIncrIDType resolves the auto-incrementing primary-key type for the
// dialect. PostgreSQL renders int8 as bigserial; MySQL needs bigint to be
// recognized as an AUTO_INCREMENT integer (it does not know "int8").
func autoIncrIDType(d sql.Dialect) sql.EngineType {
	switch d.Name() {
	case "mysql":
		return sql.EngineType{Name: "bigint"}
	case "sqlite", "libsql":
		// The rowid alias must be declared INTEGER PRIMARY KEY.
		return sql.EngineType{Name: "integer"}
	}
	return sql.EngineType{Name: "int8"}
}
