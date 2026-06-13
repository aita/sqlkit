// Package uow provides a Unit of Work over sqlkit table bindings. A uow.Scope
// tracks entities loaded through it and the rows staged on it, then flushes them
// as one ordered batch of statements through a sqlkit.Executor — typically a
// *sqlkit.Session so load and flush share one transaction.
//
// It is deliberately named Scope, not Session, so it reads clearly alongside
// sqlkit.Session (the transaction handle it flushes through):
//
//	db.WithSession(func(s *sqlkit.Session) error {
//		scope := uow.NewScope(s)
//
//		var u User
//		scope.Get(ctx, app.Users, &u, 1) // loaded => managed
//		u.Name = "renamed"               // mutate in place
//
//		carol := &User{Email: "c@example.com"}
//		scope.Add(app.Users, carol)      // INSERT; carol.ID written back on flush
//
//		return scope.Flush(ctx)          // UPDATE only u's changed columns, INSERT carol
//	})
//
// Tracking is snapshot-based (the Hibernate model): a managed entity's column
// values are snapshotted when it is loaded, and at flush the current values are
// diffed against the snapshot so only changed columns are written. Go cannot
// intercept field assignments, so dirty state is computed at flush rather than
// on mutation; mutate the loaded instance (not a copy) for changes to be seen.
//
// Nested scopes are savepoint-backed: child := scope.NewScope() flushes within a
// SAVEPOINT, and child.Rollback rolls back to it without disturbing the parent
// transaction.
package uow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// ErrBindingConflict reports two bindings used for the same table that differ in
// structure (checksum) or in the generator that produced them (sqlkit version /
// generated-code version). It catches a Unit of Work assembled from packages
// whose bindings drifted out of sync.
var ErrBindingConflict = errors.New("uow: conflicting bindings for the same table")

// ErrNestingRequiresTx reports a nested scope flushed over an executor that
// cannot open savepoints — nesting requires a transaction (a *sqlkit.Session).
var ErrNestingRequiresTx = errors.New("uow: nested scope requires a transaction-backed executor")

// ErrPrimaryKeyChanged reports a managed entity whose primary-key fields were
// mutated after it was loaded; identity is fixed at load time.
var ErrPrimaryKeyChanged = errors.New("uow: primary key of a managed row changed")

// savepointer is the subset of *sqlkit.Session a nested scope needs.
type savepointer interface {
	Savepoint(context.Context) (string, error)
	RollbackTo(context.Context, string) error
	Release(context.Context, string) error
}

// dialecter exposes the SQL dialect; *sqlkit.Database and *sqlkit.Session both
// implement it.
type dialecter interface{ Dialect() sql.Dialect }

// Scope is a Unit of Work over table bindings. Construct one with NewScope (or a
// child with Scope.NewScope). A Scope is not safe for concurrent use.
type Scope struct {
	ex         sqlkit.Executor
	identities *meta.IdentityMap

	order  []string
	tables map[string]*tableState

	// batchSize, when > 0, splits the staged value inserts of each table into
	// Insert statements of at most this many rows (see WithBatchSize).
	batchSize int

	// nesting
	parent    *Scope
	savepoint string
}

// Option configures a Scope.
type Option func(*Scope)

// WithIdentities validates every table touched by the scope against an identity
// map built from the catalog (the source of truth): a binding absent from the
// map, or whose checksum differs (schema drift), fails fast.
func WithIdentities(identities *meta.IdentityMap) Option {
	return func(s *Scope) { s.identities = identities }
}

// WithBatchSize splits the value rows staged with Add (those passed by value,
// without primary-key write-back) into INSERT statements of at most n rows each
// per table at flush, keeping large bulk inserts within the database's
// bind-parameter and statement-size limits. n <= 0 (the default) inserts each
// table's value rows in a single statement. Pointer rows still insert one at a
// time so their generated keys can be written back. The setting is inherited by
// nested scopes.
func WithBatchSize(n int) Option {
	return func(s *Scope) { s.batchSize = n }
}

// NewScope creates a Unit of Work that loads and flushes through ex. Pass a
// *sqlkit.Session so load and flush share one transaction (and so nested scopes
// can use savepoints); a *sqlkit.Database works too, but each flush statement
// then auto-commits independently.
func NewScope(ex sqlkit.Executor, opts ...Option) *Scope {
	s := &Scope{ex: ex, tables: map[string]*tableState{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewScope opens a nested scope sharing this scope's executor and identity map.
// The child accumulates its own entities and staged rows; child.Flush runs
// inside a SAVEPOINT so a failure there rolls back to the savepoint and leaves
// this (parent) scope's transaction intact.
func (s *Scope) NewScope() *Scope {
	return &Scope{
		ex:         s.ex,
		identities: s.identities,
		batchSize:  s.batchSize,
		tables:     map[string]*tableState{},
		parent:     s,
	}
}

// tableState holds the entities and staged rows for one table.
type tableState struct {
	table     sqlkit.Table
	key       meta.TableKey
	generator string

	inserts []any // rows staged via Add (pointer => PK write-back)
	updates []any // rows staged via explicit Update (detached)
	removed []removal
	managed []*tracked

	managedIdx map[string]int
	removedIdx map[string]bool
}

type removal struct {
	pk []any
}

// tracked is a managed entity: a pointer to the caller's struct plus the column
// snapshot taken when it was loaded.
type tracked struct {
	ptr       any
	pk        []any
	snapNames []string
	snapVals  []any // normalized (dereferenced) copies, for change detection
}

// Get loads the row of table with the given primary key into dest (a pointer to
// a struct) and, when found, tracks it as a managed entity. Returns whether a
// row was found. Mutating dest afterward and calling Flush emits an UPDATE of
// only the changed columns.
func (s *Scope) Get(ctx context.Context, table sqlkit.Table, dest any, pk ...any) (bool, error) {
	ts, err := s.stateFor(table)
	if err != nil {
		return false, err
	}
	key := table.PrimaryKey()
	if len(key) == 0 {
		return false, fmt.Errorf("%w: %s", sqlkit.ErrNoPrimaryKey, table.Info().QualifiedName())
	}
	if len(pk) != len(key) {
		return false, fmt.Errorf("uow: Get on %s expects %d primary-key value(s), got %d",
			table.Info().QualifiedName(), len(key), len(pk))
	}
	err = s.ex.Select().From(table).Where(pkPredicates(table, pk)...).First(ctx).Scan(dest)
	if errors.Is(err, sqlkit.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := s.track(ts, dest); err != nil {
		return false, err
	}
	return true, nil
}

// Find loads every row of table matching predicates into dest (a pointer to a
// slice) and tracks each as a managed entity. With no predicates it loads the
// whole table.
func (s *Scope) Find(ctx context.Context, table sqlkit.Table, dest any, predicates ...sqlkit.Predicate) error {
	ts, err := s.stateFor(table)
	if err != nil {
		return err
	}
	q := s.ex.Select().From(table)
	if len(predicates) > 0 {
		q = q.Where(predicates...)
	}
	if err := q.All(ctx).Scan(dest); err != nil {
		return err
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("uow: Find destination must be a pointer to a slice, got %T", dest)
	}
	slice := rv.Elem()
	for i := 0; i < slice.Len(); i++ {
		elem := slice.Index(i)
		if elem.Kind() == reflect.Pointer {
			if err := s.track(ts, elem.Interface()); err != nil {
				return err
			}
			continue
		}
		if !elem.CanAddr() {
			return fmt.Errorf("uow: Find slice elements must be addressable")
		}
		if err := s.track(ts, elem.Addr().Interface()); err != nil {
			return err
		}
	}
	return nil
}

// Add stages rows for INSERT. A row passed by pointer has its database-generated
// columns (primary key, defaults) written back after flush and becomes a managed
// entity; a row passed by value is inserted without write-back. Each row is a
// struct (or pointer to one) mapped to columns exactly as sqlkit's Insert does.
func (s *Scope) Add(table sqlkit.Table, rows ...any) error {
	ts, err := s.stateFor(table)
	if err != nil {
		return err
	}
	ts.inserts = append(ts.inserts, rows...)
	return nil
}

// Update stages detached rows for an UPDATE by primary key that writes every
// mapped non-primary-key column. Use it for rows that were not loaded through
// the scope; entities loaded with Get/Find are updated automatically from their
// snapshot diff and need no Update call.
func (s *Scope) Update(table sqlkit.Table, rows ...any) error {
	ts, err := s.stateFor(table)
	if err != nil {
		return err
	}
	ts.updates = append(ts.updates, rows...)
	return nil
}

// Remove stages rows for DELETE by primary key. Re-staging the same primary key
// is a no-op beyond the first.
func (s *Scope) Remove(table sqlkit.Table, rows ...any) error {
	ts, err := s.stateFor(table)
	if err != nil {
		return err
	}
	for _, row := range rows {
		pk, err := sqlkit.PrimaryKeyValues(table, row)
		if err != nil {
			return err
		}
		k := pkString(ts.key, pk)
		if ts.removedIdx[k] {
			continue
		}
		ts.removedIdx[k] = true
		ts.removed = append(ts.removed, removal{pk: pk})
	}
	return nil
}

// Flush emits the staged work. On a nested scope it runs inside a savepoint, so
// an error rolls back to the savepoint and leaves the parent transaction intact;
// on a root scope it runs directly on the executor. On success the staged
// inserts/updates/deletes are cleared and managed entities' snapshots are
// refreshed, so the scope can be reused for another round.
func (s *Scope) Flush(ctx context.Context) error {
	if s.parent == nil {
		return s.flush(ctx)
	}
	sp, ok := s.ex.(savepointer)
	if !ok {
		return ErrNestingRequiresTx
	}
	if s.savepoint == "" {
		name, err := sp.Savepoint(ctx)
		if err != nil {
			return err
		}
		s.savepoint = name
	}
	if err := s.flush(ctx); err != nil {
		_ = sp.RollbackTo(ctx, s.savepoint)
		return err
	}
	return nil
}

// Rollback undoes a nested scope's flushed work by rolling back to its savepoint
// and discards its staged state. It is only valid on a scope created with
// Scope.NewScope.
func (s *Scope) Rollback(ctx context.Context) error {
	if s.parent == nil {
		return errors.New("uow: Rollback is only valid on a nested scope")
	}
	if s.savepoint != "" {
		sp, ok := s.ex.(savepointer)
		if !ok {
			return ErrNestingRequiresTx
		}
		if err := sp.RollbackTo(ctx, s.savepoint); err != nil {
			return err
		}
		_ = sp.Release(ctx, s.savepoint)
		s.savepoint = ""
	}
	s.order = nil
	s.tables = map[string]*tableState{}
	return nil
}

// flush runs the staged statements directly on the executor.
func (s *Scope) flush(ctx context.Context) error {
	for _, name := range s.order {
		ts := s.tables[name]
		if err := s.flushInserts(ctx, ts); err != nil {
			return err
		}
		if err := s.flushManaged(ctx, ts); err != nil {
			return err
		}
		if err := s.flushUpdates(ctx, ts); err != nil {
			return err
		}
		if err := s.flushRemovals(ctx, ts); err != nil {
			return err
		}
	}
	// Success: clear staged work and refresh managed snapshots so the scope can
	// be reused for another mutate/flush cycle.
	for _, name := range s.order {
		ts := s.tables[name]
		ts.inserts = nil
		ts.updates = nil
		ts.removed = nil
		ts.removedIdx = map[string]bool{}
		for _, tr := range ts.managed {
			if names, vals, err := sqlkit.ColumnValues(ts.table, tr.ptr); err == nil {
				tr.snapNames, tr.snapVals = names, normalizeAll(vals)
			}
		}
	}
	return nil
}

func (s *Scope) flushInserts(ctx context.Context, ts *tableState) error {
	var batch []any
	for _, row := range ts.inserts {
		if isPointer(row) {
			if err := s.insertWithWriteback(ctx, ts, row); err != nil {
				return err
			}
			continue
		}
		batch = append(batch, row)
	}
	if len(batch) > 0 {
		if _, err := s.ex.Insert(ts.table).Values(batch...).BatchSize(s.batchSize).Exec(ctx); err != nil {
			return fmt.Errorf("uow: insert into %s: %w", ts.key, err)
		}
	}
	return nil
}

// insertWithWriteback inserts one pointer row and writes its generated key (and,
// via RETURNING, every default) back into the struct, then tracks it.
func (s *Scope) insertWithWriteback(ctx context.Context, ts *tableState, row any) error {
	if s.supportsReturning() {
		if err := s.ex.Insert(ts.table).Values(row).Returning().All(ctx).Scan(row); err != nil {
			return fmt.Errorf("uow: insert into %s: %w", ts.key, err)
		}
	} else {
		res, err := s.ex.Insert(ts.table).Values(row).Exec(ctx)
		if err != nil {
			return fmt.Errorf("uow: insert into %s: %w", ts.key, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("uow: insert into %s: recover key: %w", ts.key, err)
		}
		if pk := pkColumnNames(ts.table); len(pk) == 1 {
			if err := sqlkit.SetColumns(row, pk, []any{id}); err != nil {
				return fmt.Errorf("uow: insert into %s: %w", ts.key, err)
			}
		}
	}
	return s.track(ts, row)
}

func (s *Scope) flushManaged(ctx context.Context, ts *tableState) error {
	for _, tr := range ts.managed {
		pk, err := sqlkit.PrimaryKeyValues(ts.table, tr.ptr)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(normalizeAll(pk), normalizeAll(tr.pk)) {
			return fmt.Errorf("%w: %s", ErrPrimaryKeyChanged, ts.key)
		}
		names, vals, err := sqlkit.ColumnValues(ts.table, tr.ptr)
		if err != nil {
			return err
		}
		assignments := diffAssignments(ts.table, tr, names, vals)
		if len(assignments) == 0 {
			continue
		}
		if _, err := s.ex.Update(ts.table).Set(assignments...).Where(pkPredicates(ts.table, tr.pk)...).Exec(ctx); err != nil {
			return fmt.Errorf("uow: update %s: %w", ts.key, err)
		}
	}
	return nil
}

func (s *Scope) flushUpdates(ctx context.Context, ts *tableState) error {
	for _, row := range ts.updates {
		pk, err := sqlkit.PrimaryKeyValues(ts.table, row)
		if err != nil {
			return err
		}
		assignments, err := updateAssignments(ts.table, row)
		if err != nil {
			return err
		}
		if len(assignments) == 0 {
			continue
		}
		if _, err := s.ex.Update(ts.table).Set(assignments...).Where(pkPredicates(ts.table, pk)...).Exec(ctx); err != nil {
			return fmt.Errorf("uow: update %s: %w", ts.key, err)
		}
	}
	return nil
}

func (s *Scope) flushRemovals(ctx context.Context, ts *tableState) error {
	for _, r := range ts.removed {
		if _, err := s.ex.Delete(ts.table).Where(pkPredicates(ts.table, r.pk)...).Exec(ctx); err != nil {
			return fmt.Errorf("uow: delete from %s: %w", ts.key, err)
		}
	}
	return nil
}

// track registers a loaded/inserted struct pointer as a managed entity, taking a
// normalized snapshot of its columns. Re-tracking the same primary key replaces
// the previous instance.
func (s *Scope) track(ts *tableState, ptr any) error {
	pk, err := sqlkit.PrimaryKeyValues(ts.table, ptr)
	if err != nil {
		return err
	}
	names, vals, err := sqlkit.ColumnValues(ts.table, ptr)
	if err != nil {
		return err
	}
	tr := &tracked{ptr: ptr, pk: pk, snapNames: names, snapVals: normalizeAll(vals)}
	k := pkString(ts.key, pk)
	if i, ok := ts.managedIdx[k]; ok {
		ts.managed[i] = tr
		return nil
	}
	ts.managedIdx[k] = len(ts.managed)
	ts.managed = append(ts.managed, tr)
	return nil
}

func (s *Scope) supportsReturning() bool {
	if d, ok := s.ex.(dialecter); ok {
		return d.Dialect().SupportsReturning()
	}
	return false
}

// stateFor returns the per-table state, creating it on first use. It validates
// the binding against the identity map when one is configured, and reconciles
// repeated bindings of the same table: two bindings under one name must agree on
// structure (checksum) and generator, or ErrBindingConflict surfaces the drift.
func (s *Scope) stateFor(table sqlkit.Table) (*tableState, error) {
	info := table.Info()
	key := meta.TableKey{Schema: info.Schema, Name: info.Name, Checksum: info.Checksum}
	if s.identities != nil {
		if _, ok := s.identities.Lookup(key); !ok {
			if want, named := s.identities.KeyForName(info.Schema, info.Name); named {
				return nil, fmt.Errorf("uow: binding %s does not match catalog table %s: %w", key, want, meta.ErrTableConflict)
			}
			return nil, fmt.Errorf("uow: table %s is not in the identity map", key)
		}
	}
	name := key.QualifiedName()
	if ts, ok := s.tables[name]; ok {
		if ts.key != key || ts.generator != info.Generator {
			return nil, fmt.Errorf("%w %s: %s/%s vs %s/%s", ErrBindingConflict, name,
				ts.key, generatorLabel(ts.generator), key, generatorLabel(info.Generator))
		}
		return ts, nil
	}
	ts := &tableState{
		table:      table,
		key:        key,
		generator:  info.Generator,
		managedIdx: map[string]int{},
		removedIdx: map[string]bool{},
	}
	s.tables[name] = ts
	s.order = append(s.order, name)
	return ts, nil
}

// diffAssignments builds SET assignments for the managed entity's changed,
// non-primary-key columns by comparing current values to the snapshot.
func diffAssignments(table sqlkit.Table, tr *tracked, names []string, vals []any) []sql.Assignment {
	pk := pkNameSet(table)
	snap := make(map[string]any, len(tr.snapNames))
	for i, n := range tr.snapNames {
		snap[n] = tr.snapVals[i]
	}
	var out []sql.Assignment
	for i, name := range names {
		if pk[name] {
			continue
		}
		if old, ok := snap[name]; ok && valueEqual(old, normalize(vals[i])) {
			continue
		}
		out = append(out, sql.Set(name, sql.Val(vals[i])))
	}
	return out
}

// updateAssignments builds SET assignments for every mapped non-primary-key
// column of a detached row.
func updateAssignments(table sqlkit.Table, row any) ([]sql.Assignment, error) {
	names, values, err := sqlkit.ColumnValues(table, row)
	if err != nil {
		return nil, err
	}
	pk := pkNameSet(table)
	assignments := make([]sql.Assignment, 0, len(names))
	for i, name := range names {
		if pk[name] {
			continue
		}
		assignments = append(assignments, sql.Set(name, sql.Val(values[i])))
	}
	return assignments, nil
}

// pkPredicates builds the "pk_col = value" predicates that target a single row.
func pkPredicates(table sqlkit.Table, pk []any) []sqlkit.Predicate {
	cols := table.PrimaryKey()
	predicates := make([]sqlkit.Predicate, len(cols))
	for i, col := range cols {
		predicates[i] = sqlkit.Cond(sql.Eq(col, sql.Val(pk[i])))
	}
	return predicates
}

func pkColumnNames(table sqlkit.Table) []string {
	cols := table.PrimaryKey()
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.ColumnName()
	}
	return out
}

func pkNameSet(table sqlkit.Table) map[string]bool {
	out := map[string]bool{}
	for _, c := range table.PrimaryKey() {
		out[c.ColumnName()] = true
	}
	return out
}

func isPointer(row any) bool {
	return reflect.ValueOf(row).Kind() == reflect.Pointer
}

// normalize dereferences a pointer to a copy of its pointee (so a later in-place
// mutation through that pointer is still detected against the snapshot) and
// leaves non-pointers as-is. A nil pointer normalizes to nil.
func normalize(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return rv.Elem().Interface()
	}
	return v
}

func normalizeAll(vals []any) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = normalize(v)
	}
	return out
}

// valueEqual compares two normalized column values, using time.Time.Equal for
// instants (whose struct form can differ for equal times) and reflect.DeepEqual
// otherwise.
func valueEqual(a, b any) bool {
	if at, ok := a.(time.Time); ok {
		if bt, ok := b.(time.Time); ok {
			return at.Equal(bt)
		}
	}
	return reflect.DeepEqual(a, b)
}

// pkString renders a primary key to a map key scoped by table identity. The type
// is included so values that print alike but differ in type stay distinct.
func pkString(key meta.TableKey, pk []any) string {
	var sb strings.Builder
	sb.WriteString(key.Checksum)
	for _, v := range pk {
		fmt.Fprintf(&sb, "\x00%T=%v", v, v)
	}
	return sb.String()
}

// generatorLabel renders a generator fingerprint for diagnostics, naming an
// empty one (a hand-written binding) explicitly.
func generatorLabel(generator string) string {
	if generator == "" {
		return "(unidentified)"
	}
	return generator
}
