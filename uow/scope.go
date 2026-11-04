// Package uow provides a Unit of Work over sqlkit table bindings. A uow.Scope
// tracks entities loaded through it and the rows staged on it, then flushes them
// as one ordered batch of statements through a sqlkit.Querier — typically a
// *sqlkit.Session so load and flush share one transaction.
//
// It is deliberately named Scope, not Session, so it reads clearly alongside
// sqlkit.Session (the transaction handle it flushes through):
//
//	db.WithSession(ctx, func(s *sqlkit.Session) error {
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
// transaction. Because rolling back to a savepoint undoes everything executed
// after it, Rollback refuses (ErrRollbackDiscardsWork) once another scope has
// flushed work after the child's savepoint; finish or roll back a nested scope
// before its parent flushes again.
package uow

import (
	"context"
	stddriver "database/sql/driver"
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

// ErrAttachRequiresPointer reports a non-pointer row passed to Attach. The
// snapshot diff re-reads the row at flush time, so a value copy could never
// reflect later mutations.
var ErrAttachRequiresPointer = errors.New("uow: Attach requires a pointer to a struct")

// ErrRollbackDiscardsWork reports a nested scope's Rollback refused because it
// would also undo work that another scope — an enclosing scope or a sibling —
// flushed after this scope's savepoint was taken. Rolling back to the savepoint
// discards everything executed since, and that other scope has already cleared
// its staged work believing it written, so the rows would be lost silently.
// Roll back (or finish) the nested scope before the enclosing scope flushes
// again, or roll back the whole session.
var ErrRollbackDiscardsWork = errors.New("uow: Rollback would discard work another scope flushed after this scope's savepoint")

// savepointer is the subset of *sqlkit.Session a nested scope needs.
type savepointer interface {
	Savepoint(context.Context) (string, error)
	RollbackTo(context.Context, string) error
	Release(context.Context, string) error
}

// dialecter exposes the SQL dialect; *sqlkit.Database and *sqlkit.Session both
// implement it.
type dialecter interface{ Dialect() sql.Dialect }

// identityProvider is implemented by executors that carry a catalog identity
// map — a *sqlkit.Database or *sqlkit.Session bound with sqlkit.WithMetadata.
// NewScope reads it so a scope validates its bindings automatically, without an
// explicit WithIdentities.
type identityProvider interface {
	Identities() *meta.IdentityMap
}

// Scope is a Unit of Work over table bindings. Construct one with NewScope (or a
// child with Scope.NewScope). A Scope is not safe for concurrent use.
type Scope struct {
	ex         sqlkit.Querier
	identities *meta.IdentityMap

	order  []string
	tables map[string]*tableState

	// batchSize, when > 0, splits the staged value inserts of each table into
	// Insert statements of at most this many rows (see WithBatchSize).
	batchSize int

	// autoChunk overrides, when non-nil, whether the flush batches UPDATE/DELETE
	// work (merging deletes into chunked IN lists and updating via a VALUES join)
	// and sizes it to the bind-parameter limit. nil inherits the executor's
	// WithAutoChunk setting. See WithAutoChunk.
	autoChunk *bool

	// autoflush, when set, flushes pending work before a Get/Find read so the
	// read observes the scope's own not-yet-flushed changes. See WithAutoflush.
	autoflush bool

	// hooks observe the flush lifecycle; each may implement BeforeFlushHook
	// and/or AfterFlushHook. See WithFlushHook.
	hooks []any

	// nesting
	parent    *Scope
	savepoint string

	// Flush ordering across a scope tree, for Rollback's safety check. tree is
	// shared by the root scope and every scope nested under it; spSeq is the
	// tree's sequence number when this scope took its savepoint, and lastFlush
	// the sequence number of this scope's latest flush that wrote work.
	tree      *scopeTree
	spSeq     int
	lastFlush int
	// flushWrote reports whether the latest flush had work to write (checked
	// after the BeforeFlush hooks, which may stage more).
	flushWrote bool

	// versionWrites are the bumped version values of rows whose guarded UPDATE
	// succeeded during the current flush. They are written back into the rows
	// only once the whole flush succeeds (a failed flush leaves the staged
	// state, and the rows, as they were so a retry guards on the same version).
	versionWrites []versionWrite
}

// scopeTree is the state shared by a root scope and its nested scopes: a
// sequence counter ordering savepoints and flushes, and every scope in the tree.
type scopeTree struct {
	seq    int
	scopes []*Scope
}

func (t *scopeTree) next() int { t.seq++; return t.seq }

// treeOf returns s's scope tree, creating it for a root scope on first use.
func (s *Scope) treeOf() *scopeTree {
	if s.tree == nil {
		s.tree = &scopeTree{scopes: []*Scope{s}}
	}
	return s.tree
}

// descendsFrom reports whether s is anc or nested (at any depth) under it.
func (s *Scope) descendsFrom(anc *Scope) bool {
	for sc := s; sc != nil; sc = sc.parent {
		if sc == anc {
			return true
		}
	}
	return false
}

// versionWrite records a version field to set to next after a successful flush.
type versionWrite struct {
	row    any // pointer to the row struct
	column string
	next   any
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

// WithAutoChunk overrides, for this scope, whether the flush automatically
// batches its writes within the dialect's bind-parameter limit: removals of
// rows with a single-column primary key are merged into chunked DELETE ... WHERE
// pk IN (...) statements, and updates are applied with a chunked UPDATE ... FROM
// (VALUES ...) join instead of one statement per row. Without it a scope inherits
// the executor's WithAutoChunk setting; pass false to keep the per-row flush even
// when the executor enables splitting, or true to batch over an executor that has
// it off. Composite-key tables always flush per row. The setting is inherited by
// nested scopes.
func WithAutoChunk(enabled bool) Option {
	return func(s *Scope) { s.autoChunk = &enabled }
}

// NewScope creates a Unit of Work that loads and flushes through ex. Pass a
// *sqlkit.Session so load and flush share one transaction (and so nested scopes
// can use savepoints); a *sqlkit.Database works too, but each flush statement
// then auto-commits independently.
//
// When ex carries a catalog identity map (a database or session bound with
// sqlkit.WithMetadata), the scope adopts it automatically and validates its
// bindings against it; an explicit WithIdentities still overrides that.
func NewScope(ex sqlkit.Querier, opts ...Option) *Scope {
	s := &Scope{ex: ex, tables: map[string]*tableState{}}
	if ip, ok := ex.(identityProvider); ok {
		s.identities = ip.Identities()
	}
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
	tree := s.treeOf()
	child := &Scope{
		ex:         s.ex,
		identities: s.identities,
		batchSize:  s.batchSize,
		autoChunk:  s.autoChunk,
		autoflush:  s.autoflush,
		hooks:      s.hooks,
		tables:     map[string]*tableState{},
		parent:     s,
		tree:       tree,
	}
	tree.scopes = append(tree.scopes, child)
	return child
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
	if err := s.maybeAutoflush(ctx); err != nil {
		return false, err
	}
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
	if err := s.maybeAutoflush(ctx); err != nil {
		return err
	}
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

// Attach registers detached rows as managed entities, snapshotting their
// current column values. Mutate a row after attaching and Flush emits an UPDATE
// that sets only the changed non-primary-key columns — the same snapshot-diff
// path used for rows loaded with Get/Find, and emitting nothing when nothing
// changed. Use it for rows that originate outside the scope (for example a
// cache) when you want a minimal UPDATE instead of Update's full-column
// overwrite.
//
// Each row must be a pointer to its struct: the diff is computed at flush time
// by re-reading that pointer against the snapshot, so a value copy could never
// reflect later mutations. Re-attaching the same primary key replaces the
// previous snapshot.
func (s *Scope) Attach(table sqlkit.Table, rows ...any) error {
	ts, err := s.stateFor(table)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !isPointer(row) {
			return fmt.Errorf("%w: %T", ErrAttachRequiresPointer, row)
		}
		if err := s.track(ts, row); err != nil {
			return err
		}
	}
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

// Flush emits the staged work in three phases so foreign keys hold across tables:
// every INSERT first, then the UPDATEs, then every DELETE. When the scope has a
// catalog identity map (WithMetadata or WithIdentities), the tables are ordered
// by their foreign keys — parents are inserted before the tables that reference
// them, and deleted after — so staging a child before its parent still flushes in
// a safe order; without metadata the staging order is used as-is. A managed or
// updated row that is also staged for removal is deleted without a preceding
// UPDATE.
//
// On a nested scope it runs inside a savepoint, so an error rolls back to the
// savepoint and leaves the parent transaction intact; on a root scope it runs
// directly on the executor. On success the staged inserts/updates/deletes are
// cleared and managed entities' snapshots are refreshed, so the scope can be
// reused for another round.
//
// Registered BeforeFlush hooks run first, before anything is written; an error
// from one aborts the flush with the staged work intact. AfterFlush hooks run
// once the flush has succeeded, with a ChangeSet of what was written; an error
// from one is returned but does not undo the flush (see AfterFlushHook).
func (s *Scope) Flush(ctx context.Context) error {
	changes, err := s.flushGuarded(ctx)
	if err != nil {
		return err
	}
	return s.runAfterFlush(ctx, changes)
}

// flushGuarded runs flush — inside a guard savepoint on a nested scope — and
// returns the ChangeSet snapshot of the work it wrote, for the AfterFlush hooks.
func (s *Scope) flushGuarded(ctx context.Context) (*ChangeSet, error) {
	if s.parent == nil {
		changes, err := s.flush(ctx)
		if s.flushWrote {
			// Even a failed root flush may have written part of its work.
			s.lastFlush = s.treeOf().next()
		}
		return changes, err
	}
	sp, ok := s.ex.(savepointer)
	if !ok {
		return nil, ErrNestingRequiresTx
	}
	if s.savepoint == "" {
		name, err := sp.Savepoint(ctx)
		if err != nil {
			return nil, err
		}
		s.savepoint = name
		s.spSeq = s.treeOf().next()
	}
	guard, err := sp.Savepoint(ctx)
	if err != nil {
		return nil, err
	}
	changes, err := s.flush(ctx)
	if err != nil {
		_ = sp.RollbackTo(ctx, guard)
		return nil, err
	}
	if s.flushWrote {
		s.lastFlush = s.treeOf().next()
	}
	return changes, nil
}

// Rollback undoes a nested scope's flushed work by rolling back to its savepoint
// and discards its staged state. It is only valid on a scope created with
// Scope.NewScope.
//
// Rolling back to the savepoint undoes everything executed since it was taken,
// so Rollback refuses with ErrRollbackDiscardsWork when another scope (the
// parent, an ancestor, or a sibling) has flushed work after that point; that
// work would otherwise be lost without an error.
func (s *Scope) Rollback(ctx context.Context) error {
	if s.parent == nil {
		return errors.New("uow: Rollback is only valid on a nested scope")
	}
	if s.savepoint != "" {
		for _, other := range s.treeOf().scopes {
			if !other.descendsFrom(s) && other.lastFlush > s.spSeq {
				return ErrRollbackDiscardsWork
			}
		}
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

// flush runs the staged statements directly on the executor, in three phases so
// foreign keys are respected across tables: every INSERT first (parent tables
// before the tables that reference them), then the UPDATEs, then every DELETE in
// the reverse order (children before parents). flushOrder supplies the
// foreign-key-aware table order; within a phase each table's own work runs as a
// unit.
//
// The BeforeFlush hooks run first, against the live staged work (so they can
// still change it). It returns a snapshot of the work it then wrote, for the
// AfterFlush hooks, or nil when none is registered.
func (s *Scope) flush(ctx context.Context) (*ChangeSet, error) {
	s.versionWrites = nil
	s.flushWrote = false
	if err := s.runBeforeFlush(ctx); err != nil {
		return nil, err
	}
	s.flushWrote = s.hasPending()
	changes := s.captureChanges()
	order := s.flushOrder()
	for _, name := range order {
		if err := s.flushInserts(ctx, s.tables[name]); err != nil {
			return nil, err
		}
	}
	for _, name := range order {
		ts := s.tables[name]
		if err := s.flushManaged(ctx, ts); err != nil {
			return nil, err
		}
		if err := s.flushUpdates(ctx, ts); err != nil {
			return nil, err
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		if err := s.flushRemovals(ctx, s.tables[order[i]]); err != nil {
			return nil, err
		}
	}
	// Success: write the bumped version values back into their rows (so the
	// struct, and the refreshed snapshot below, match the database), clear
	// staged work, and refresh managed snapshots so the scope can be reused for
	// another mutate/flush cycle.
	for _, w := range s.versionWrites {
		if err := sqlkit.SetColumns(w.row, []string{w.column}, []any{w.next}); err != nil {
			return nil, fmt.Errorf("uow: write back version %s: %w", w.column, err)
		}
	}
	s.versionWrites = nil
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
	return changes, nil
}

// flushOrder returns the staged tables ordered so each table follows the tables
// it references by foreign key (parents before children) — the order the insert
// and update phases use, and whose reverse the delete phase uses. Ordering needs
// foreign-key structure, which only the catalog carries, so a scope orders only
// when it has an identity map (WithMetadata or WithIdentities); without one, or
// when the references form a cycle, it returns the staging order unchanged.
func (s *Scope) flushOrder() []string {
	if s.identities == nil || len(s.order) < 2 {
		return s.order
	}
	staged := make(map[string]bool, len(s.order))
	for _, n := range s.order {
		staged[n] = true
	}
	// parents[name] holds the staged tables that name references and so must be
	// inserted before it. A self-reference and references to non-staged tables are
	// ignored — they impose no ordering on this flush.
	parents := make(map[string]map[string]bool)
	for _, name := range s.order {
		t, ok := s.identities.Lookup(s.tables[name].key)
		if !ok {
			continue
		}
		for _, fk := range t.ForeignKeys {
			ref := fkRefName(fk)
			if ref == name || !staged[ref] {
				continue
			}
			if parents[name] == nil {
				parents[name] = map[string]bool{}
			}
			parents[name][ref] = true
		}
	}
	if len(parents) == 0 {
		return s.order
	}
	ordered, ok := topoStable(s.order, parents)
	if !ok {
		return s.order // a cycle: fall back to staging order
	}
	return ordered
}

// topoStable orders names so every name follows all its parents, preserving the
// original order among nodes that are otherwise unconstrained. It reports ok
// false when the parent relation has a cycle.
func topoStable(order []string, parents map[string]map[string]bool) (result []string, ok bool) {
	emitted := make(map[string]bool, len(order))
	out := make([]string, 0, len(order))
	for len(out) < len(order) {
		progressed := false
		for _, name := range order {
			if emitted[name] {
				continue
			}
			ready := true
			for p := range parents[name] {
				if !emitted[p] {
					ready = false
					break
				}
			}
			if ready {
				emitted[name] = true
				out = append(out, name)
				progressed = true
			}
		}
		if !progressed {
			return nil, false
		}
	}
	return out, true
}

// fkRefName is the schema-qualified name of a foreign key's referenced table, in
// the same form stateFor keys staged tables by (see TableKey.QualifiedName).
func fkRefName(fk meta.ForeignKey) string {
	if fk.RefSchema.Name == "" {
		return fk.RefTable
	}
	return fk.RefSchema.Name + "." + fk.RefTable
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
		if _, err := s.ex.Insert(ts.table).Values(batch...).AutoChunk(sqlkit.ChunkSize(s.batchSize)).Exec(ctx); err != nil {
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
	auto, limit := s.splitConfig()
	bulk := (auto || s.batchSize > 0) && len(ts.table.PrimaryKey()) == 1
	var pending []pendingUpdate
	for _, tr := range ts.managed {
		// A managed entity also staged for removal is about to be deleted; skip its
		// UPDATE rather than write a row the delete phase will drop.
		if ts.removedIdx[pkString(ts.key, tr.pk)] {
			continue
		}
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
		meta, err := sqlkit.ColumnMetadata(tr.ptr, names)
		if err != nil {
			return err
		}
		assignments, guard := diffAssignments(ts.table, tr, names, vals, meta)
		if len(assignments) == 0 {
			continue
		}
		if !bulk || hasNullAssignment(assignments) || hasValuerAssignment(assignments) || guard != nil {
			if err := s.execRowUpdate(ctx, ts, tr.pk, assignments, guard); err != nil {
				return err
			}
			s.recordVersionWrite(tr.ptr, guard)
			continue
		}
		pending = append(pending, pendingUpdate{pk: tr.pk, assignments: assignments})
	}
	return s.flushBulkUpdates(ctx, ts, pending, limit)
}

func (s *Scope) flushUpdates(ctx context.Context, ts *tableState) error {
	auto, limit := s.splitConfig()
	bulk := (auto || s.batchSize > 0) && len(ts.table.PrimaryKey()) == 1
	var pending []pendingUpdate
	for _, row := range ts.updates {
		pk, err := sqlkit.PrimaryKeyValues(ts.table, row)
		if err != nil {
			return err
		}
		// A row also staged for removal is about to be deleted; skip its UPDATE.
		if ts.removedIdx[pkString(ts.key, pk)] {
			continue
		}
		assignments, guard, err := updateAssignments(ts.table, row)
		if err != nil {
			return err
		}
		if len(assignments) == 0 {
			continue
		}
		if !bulk || hasNullAssignment(assignments) || hasValuerAssignment(assignments) || guard != nil {
			if err := s.execRowUpdate(ctx, ts, pk, assignments, guard); err != nil {
				return err
			}
			s.recordVersionWrite(row, guard)
			continue
		}
		pending = append(pending, pendingUpdate{pk: pk, assignments: assignments})
	}
	return s.flushBulkUpdates(ctx, ts, pending, limit)
}

func (s *Scope) flushRemovals(ctx context.Context, ts *tableState) error {
	if len(ts.removed) == 0 {
		return nil
	}
	auto, limit := s.splitConfig()
	cols := ts.table.PrimaryKey()
	// Merge single-column-PK removals into chunked DELETE ... WHERE pk IN (...)
	// statements when batching is on; composite keys flush one row at a time.
	if len(cols) == 1 && (auto || s.batchSize > 0) {
		vals := make([]any, len(ts.removed))
		for i, r := range ts.removed {
			vals[i] = r.pk[0]
		}
		for _, part := range chunkSlice(vals, s.chunkSize(1, auto, limit)) {
			if _, err := s.ex.Delete(ts.table).Where(sqlkit.InValues(cols[0], part...)).Exec(ctx); err != nil {
				return fmt.Errorf("uow: delete from %s: %w", ts.key, err)
			}
		}
		return nil
	}
	for _, r := range ts.removed {
		if _, err := s.ex.Delete(ts.table).Where(pkPredicates(ts.table, r.pk)...).Exec(ctx); err != nil {
			return fmt.Errorf("uow: delete from %s: %w", ts.key, err)
		}
	}
	return nil
}

// pendingUpdate is a managed/explicit update deferred so several rows can be
// applied together in one UPDATE ... FROM (VALUES ...) statement.
type pendingUpdate struct {
	pk          []any
	assignments []sql.Assignment
}

// execRowUpdate runs one single-row UPDATE by primary key — the unbatched path.
// A non-nil guard adds the optimistic-lock predicate (WHERE version = current,
// ANDed onto the primary-key predicate) and reports sqlkit.ErrVersionConflict
// when the statement affects no row: either the primary key is gone or another
// writer already advanced the version column past current.
func (s *Scope) execRowUpdate(ctx context.Context, ts *tableState, pk []any, assignments []sql.Assignment, guard *versionGuard) error {
	predicates := pkPredicates(ts.table, pk)
	if guard != nil {
		predicates = append(predicates, sqlkit.Cond(sql.Eq(sql.Col(guard.column), sql.Val(guard.current))))
	}
	res, err := s.ex.Update(ts.table).Set(assignments...).Where(predicates...).Exec(ctx)
	if err != nil {
		return fmt.Errorf("uow: update %s: %w", ts.key, err)
	}
	if guard == nil {
		return nil
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("uow: update %s: %w", ts.key, err)
	}
	if affected == 0 {
		return fmt.Errorf("uow: update %s: %w", ts.key, sqlkit.ErrVersionConflict)
	}
	return nil
}

// flushBulkUpdates applies the deferred updates with a VALUES-join UPDATE. Rows
// are grouped by their set of changed columns (each group updates a fixed column
// list) and each group is chunked to the bind-parameter limit; a group of one row
// degrades to a plain single-row UPDATE.
func (s *Scope) flushBulkUpdates(ctx context.Context, ts *tableState, pending []pendingUpdate, limit int) error {
	if len(pending) == 0 {
		return nil
	}
	pkCol := ts.table.PrimaryKey()[0]
	groups, order := groupBySignature(pending)
	for _, sig := range order {
		group := groups[sig]
		if len(group) == 1 {
			// pending never carries a version-guarded row (see flushManaged/
			// flushUpdates), so no guard applies here.
			if err := s.execRowUpdate(ctx, ts, group[0].pk, group[0].assignments, nil); err != nil {
				return err
			}
			continue
		}
		colNames := assignmentColumns(group[0].assignments)
		for _, part := range chunkSlice(group, s.chunkSize(1+len(colNames), true, limit)) {
			if err := s.execValuesUpdate(ctx, ts, pkCol, colNames, part); err != nil {
				return err
			}
		}
	}
	return nil
}

// execValuesUpdate runs one UPDATE ... SET col = v.cN FROM (VALUES (...)) AS
// v(k, c0, c1, …) WHERE pk = v.k for a chunk of rows sharing colNames. The VALUES
// columns use synthetic names (k for the key, cN for each value) so they never
// collide with the target table's column names — keeping the SET targets bare
// (required by PostgreSQL) and unambiguous (required by MySQL).
func (s *Scope) execValuesUpdate(ctx context.Context, ts *tableState, pkCol sqlkit.AnyColumn, colNames []string, group []pendingUpdate) error {
	valueCols := make([]string, 0, 1+len(colNames))
	valueCols = append(valueCols, "k")
	for i := range colNames {
		valueCols = append(valueCols, fmt.Sprintf("c%d", i))
	}
	rows := make([][]sql.Expression, len(group))
	for i, p := range group {
		byCol := make(map[string]sql.Expression, len(p.assignments))
		for _, a := range p.assignments {
			byCol[a.Column.String()] = a.Value
		}
		row := make([]sql.Expression, 0, len(valueCols))
		row = append(row, sql.Val(p.pk[0]))
		for _, cn := range colNames {
			row = append(row, byCol[cn])
		}
		rows[i] = row
	}
	setCols := make([]sql.Assignment, len(colNames))
	for i, cn := range colNames {
		setCols[i] = sql.Set(cn, sql.Col("v."+valueCols[i+1]))
	}
	source := sql.ValuesSource(rows, "v", valueCols...)
	join := sqlkit.Cond(sql.Eq(pkCol, sql.Col("v.k")))
	if _, err := s.ex.Update(ts.table).Set(setCols...).From(source).Where(join).Exec(ctx); err != nil {
		return fmt.Errorf("uow: update %s: %w", ts.key, err)
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

// recordVersionWrite queues the bumped version of a row whose guarded UPDATE
// succeeded, to be written back when the flush completes. A row passed by value
// (a detached Update with a non-pointer struct) can't be written back; its
// caller keeps the stale version and must reload before updating it again.
func (s *Scope) recordVersionWrite(row any, guard *versionGuard) {
	if guard == nil {
		return
	}
	rv := reflect.ValueOf(row)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return
	}
	next, ok := nextVersion(guard.current)
	if !ok {
		return
	}
	s.versionWrites = append(s.versionWrites, versionWrite{row: row, column: guard.column, next: next})
}

// nextVersion returns v+1 in v's own integer type, mirroring the database's
// "col = col + 1". It reports false for a non-integer version value.
func nextVersion(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(rv.Int() + 1).Convert(rv.Type()).Interface(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(rv.Uint() + 1).Convert(rv.Type()).Interface(), true
	}
	return nil, false
}

// versionGuard is an optimistic-lock predicate to AND onto an UPDATE's WHERE:
// the version column matches current, so a row another writer already
// advanced past current matches nothing and execRowUpdate reports
// sqlkit.ErrVersionConflict.
type versionGuard struct {
	column  string
	current any
}

// diffAssignments builds SET assignments for the managed entity's changed,
// non-primary-key columns by comparing current values to the snapshot. A
// version-tagged column is never diffed (app code does not mutate it itself):
// it is bumped (col = col + 1) and its snapshot value becomes the returned
// guard (and is bumped in the struct too once the flush succeeds), mirroring update.go's SetStruct so optimistic locking works
// the same through uow as through the direct builder path. A touch-tagged
// column is set to CURRENT_TIMESTAMP regardless of whether it changed, since
// app code does not maintain it either.
//
// Both ride along with a real change only: when no ordinary column differs
// from the snapshot the entity is clean, and per uow's dirty-tracking contract
// a clean entity flushes nothing — no UPDATE, no version bump, no touch.
func diffAssignments(table sqlkit.Table, tr *tracked, names []string, vals []any, meta map[string]sqlkit.ColumnMeta) ([]sql.Assignment, *versionGuard) {
	pk := pkNameSet(table)
	snap := make(map[string]any, len(tr.snapNames))
	for i, n := range tr.snapNames {
		snap[n] = tr.snapVals[i]
	}
	if !hasRealChange(pk, snap, names, vals, meta) {
		return nil, nil
	}
	var out []sql.Assignment
	var guard *versionGuard
	for i, name := range names {
		if pk[name] {
			continue
		}
		m := meta[name]
		switch {
		case m.Version:
			out = append(out, sql.Set(name, sql.Add(sql.Col(name), sql.Val(1))))
			// Guard on the version the entity was loaded (or last flushed)
			// with — the snapshot — not on whatever the struct holds now, so a
			// hand-edited version field can't defeat or spoil the lock.
			current, ok := snap[name]
			if !ok {
				current = normalize(vals[i])
			}
			guard = &versionGuard{column: name, current: current}
		case m.Touch:
			out = append(out, sql.Set(name, sql.CurrentTimestamp()))
		default:
			if old, ok := snap[name]; ok && valueEqual(old, normalize(vals[i])) {
				continue
			}
			out = append(out, sql.Set(name, sql.Val(vals[i])))
		}
	}
	return out, guard
}

// hasRealChange reports whether any ordinary (non-primary-key, non-version,
// non-touch) column differs from its snapshot value. Version and touch columns
// are excluded because the app never maintains them itself — counting them
// would turn every clean entity into a perpetual write.
func hasRealChange(pk map[string]bool, snap map[string]any, names []string, vals []any, meta map[string]sqlkit.ColumnMeta) bool {
	for i, name := range names {
		if pk[name] {
			continue
		}
		if m := meta[name]; m.Version || m.Touch {
			continue
		}
		if old, ok := snap[name]; ok && valueEqual(old, normalize(vals[i])) {
			continue
		}
		return true
	}
	return false
}

// updateAssignments builds SET assignments for every mapped non-primary-key
// column of a detached row. As in diffAssignments, a version-tagged column is
// bumped with its current value returned as the guard rather than copied (and,
// for a row passed by pointer, bumped in the struct once the flush succeeds),
// and a touch-tagged column is unconditionally set to CURRENT_TIMESTAMP.
func updateAssignments(table sqlkit.Table, row any) ([]sql.Assignment, *versionGuard, error) {
	names, values, err := sqlkit.ColumnValues(table, row)
	if err != nil {
		return nil, nil, err
	}
	meta, err := sqlkit.ColumnMetadata(row, names)
	if err != nil {
		return nil, nil, err
	}
	pk := pkNameSet(table)
	assignments := make([]sql.Assignment, 0, len(names))
	var guard *versionGuard
	for i, name := range names {
		if pk[name] {
			continue
		}
		m := meta[name]
		switch {
		case m.Version:
			assignments = append(assignments, sql.Set(name, sql.Add(sql.Col(name), sql.Val(1))))
			guard = &versionGuard{column: name, current: values[i]}
		case m.Touch:
			assignments = append(assignments, sql.Set(name, sql.CurrentTimestamp()))
		default:
			assignments = append(assignments, sql.Set(name, sql.Val(values[i])))
		}
	}
	return assignments, guard, nil
}

// splitConfig resolves whether the flush should batch its UPDATE/DELETE work and
// the dialect's bind-parameter limit to size it by, letting a scope-level
// WithAutoChunk override the executor's setting.
func (s *Scope) splitConfig() (auto bool, limit int) {
	if cfg, ok := s.ex.(interface{ AutoChunkEnabled() bool }); ok {
		auto = cfg.AutoChunkEnabled()
	}
	if d, ok := s.ex.(dialecter); ok {
		if bp, ok := d.Dialect().(sql.BindParamLimiter); ok {
			limit = bp.MaxBindParams()
		}
	}
	if s.autoChunk != nil {
		auto = *s.autoChunk
	}
	return auto, limit
}

// chunkSize returns the maximum rows per batched statement: the scope's batch
// size, lowered to floor(limit/paramsPerRow) when auto-splitting is on so the
// parameters never exceed the limit. 0 means a single chunk.
func (s *Scope) chunkSize(paramsPerRow int, auto bool, limit int) int {
	size := s.batchSize
	if auto && limit > 0 && paramsPerRow > 0 {
		perStmt := limit / paramsPerRow
		if perStmt < 1 {
			perStmt = 1
		}
		if size <= 0 || perStmt < size {
			size = perStmt
		}
	}
	return size
}

// chunkSlice splits items into consecutive chunks of at most size; size <= 0
// yields a single chunk.
func chunkSlice[T any](items []T, size int) [][]T {
	if size <= 0 || len(items) <= size {
		return [][]T{items}
	}
	out := make([][]T, 0, (len(items)+size-1)/size)
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

// groupBySignature groups pending updates by their set of changed columns (in
// column order), so each group updates a single fixed column list; order keeps
// the groups in first-seen order for a deterministic flush.
func groupBySignature(pending []pendingUpdate) (groups map[string][]pendingUpdate, order []string) {
	groups = map[string][]pendingUpdate{}
	for _, p := range pending {
		sig := strings.Join(assignmentColumns(p.assignments), "\x00")
		if _, ok := groups[sig]; !ok {
			order = append(order, sig)
		}
		groups[sig] = append(groups[sig], p)
	}
	return groups, order
}

// assignmentColumns returns the assignment column names in order.
func assignmentColumns(assignments []sql.Assignment) []string {
	cols := make([]string, len(assignments))
	for i, a := range assignments {
		cols[i] = a.Column.String()
	}
	return cols
}

// hasNullAssignment reports whether any assignment sets a column to a NULL bind
// value. Such rows are flushed individually rather than through a VALUES join,
// where an all-unknown VALUES column would have no inferable type.
func hasNullAssignment(assignments []sql.Assignment) bool {
	for _, a := range assignments {
		if v, ok := a.Value.(sql.ValueExpression); ok && v.Value == nil {
			return true
		}
	}
	return false
}

// hasValuerAssignment reports whether any assignment binds a value through a
// driver.Valuer — most notably a json-tagged column, which ColumnValues wraps
// for JSON encoding. Such a value reaches the driver as the opaque output of
// Value() (e.g. JSON bytes), which a VALUES source cannot carry as a typed
// column: the parameter is untyped there and the target column's type cannot be
// inferred (PostgreSQL rejects it), whereas a single-row UPDATE assigns straight
// to the column and infers the type. So these rows are flushed individually, the
// same per-row fallback hasNullAssignment uses.
func hasValuerAssignment(assignments []sql.Assignment) bool {
	for _, a := range assignments {
		if v, ok := a.Value.(sql.ValueExpression); ok {
			if _, ok := v.Value.(stddriver.Valuer); ok {
				return true
			}
		}
	}
	return false
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

// normalize prepares a column value for change-detection comparison, so a later
// in-place mutation of the value the snapshot holds is still detected: a pointer
// dereferences to an independent copy of its pointee, and a slice/map value is
// deep-copied — a bare copy of the slice/map header still aliases the same
// backing array/buckets, so an in-place mutation through the field (e.g.
// tags[0] = "x", or meta["k"] = "v" on a JSON-tagged column) would otherwise be
// invisible to reflect.DeepEqual. A value implementing driver.Valuer (notably a
// JSON-tagged column's internal wrapper, or a custom slice/map type with its own
// Value method) is normalized through Value() instead: the driver representation
// it returns is a fresh, immutable value each call (e.g. freshly marshaled JSON
// bytes) that never aliases the live field, and it is what actually gets
// written, so it is the more precise snapshot even beneath a pointer receiver.
// A nil pointer/slice/map normalizes to nil/unchanged.
func normalize(v any) any {
	// A nil pointer normalizes to nil before the Valuer check: calling Value on
	// a nil *T whose Value method has a value receiver (e.g. *sql.NullString)
	// panics, and database/sql itself treats such a nil pointer as SQL NULL.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil
	}
	if valuer, ok := v.(stddriver.Valuer); ok {
		if dv, err := valuer.Value(); err == nil {
			return dv
		}
		// Value() erroring on the current value is surprising but not this
		// function's concern to report; fall through so the snapshot still
		// reflects something rather than silently pretending nothing changed.
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return normalize(rv.Elem().Interface())
	case reflect.Slice:
		if rv.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		reflect.Copy(cp, rv)
		return cp.Interface()
	case reflect.Map:
		if rv.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		for iter := rv.MapRange(); iter.Next(); {
			cp.SetMapIndex(iter.Key(), iter.Value())
		}
		return cp.Interface()
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
