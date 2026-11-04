package uow

import (
	"context"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/meta"
)

// BeforeFlushHook is invoked at the start of a Flush, before any statement is
// emitted, with a ChangeSet describing the work about to run. Returning an error
// aborts the flush before it writes anything (on a nested scope nothing reaches
// the savepoint; on a session-backed root scope the surrounding session rolls
// back). Use it to stamp audit columns, enforce invariants, or observe what is
// changing — mutating a pointer returned by ChangeSet.Dirty here is picked up by
// the diff the flush then computes.
type BeforeFlushHook interface {
	BeforeFlush(ctx context.Context, c *ChangeSet) error
}

// AfterFlushHook is invoked after a Flush has succeeded — its statements emitted,
// staged work cleared, and managed snapshots refreshed — with a ChangeSet of the
// rows the flush wrote. Returning an error propagates from Flush, but it does
// not undo the flush: the rows are written (inside the surrounding transaction,
// if any) and are no longer staged, so a retried Flush emits nothing. To discard
// the work on an AfterFlush error, roll back the surrounding session or, on a
// nested scope, call Rollback.
type AfterFlushHook interface {
	AfterFlush(ctx context.Context, c *ChangeSet) error
}

// WithFlushHook registers an observer of the flush lifecycle. The hook may
// implement BeforeFlushHook, AfterFlushHook, or both; each is optional and
// discovered by type assertion, mirroring how the compiler probes dialect
// capabilities. Multiple hooks run in registration order, and the registration
// is inherited by nested scopes.
func WithFlushHook(h any) Option {
	return func(s *Scope) { s.hooks = append(s.hooks, h) }
}

// ChangeSet is a view of the work a Flush performs, passed to flush hooks. In
// BeforeFlush it is live: it describes the staged work about to run, and to
// stamp a column on the rows about to change you mutate the pointers Dirty
// returns. In AfterFlush it is a snapshot of what the flush wrote, taken after
// the BeforeFlush hooks ran, so it still reports the rows even though the
// staged work has been cleared by then.
type ChangeSet struct {
	scope *Scope
	// frozen, when non-nil, is the snapshot an AfterFlush ChangeSet reports,
	// keyed by table qualified name, instead of the live scope.
	frozen map[string]*tableChanges
}

// tableChanges is one table's part of a ChangeSet snapshot.
type tableChanges struct {
	dirty    []any
	inserted []any
	removed  [][]any
}

// Dirty returns the managed entity pointers of table whose columns changed since
// they were loaded or attached — the rows the flush will UPDATE. A row also
// staged for removal is excluded. Mutating a returned pointer in BeforeFlush is
// reflected in the UPDATE the flush emits.
func (c *ChangeSet) Dirty(table sqlkit.Table) []any {
	if c.frozen != nil {
		if tc := c.frozen[tableKey(table)]; tc != nil {
			return append([]any(nil), tc.dirty...)
		}
		return nil
	}
	ts := c.tableState(table)
	if ts == nil {
		return nil
	}
	var out []any
	for _, tr := range ts.managed {
		if c.scope.entityDirty(ts, tr) {
			out = append(out, tr.ptr)
		}
	}
	return out
}

// Inserted returns the rows staged for INSERT on table (pointers and values, in
// staging order). Mutating a pointer row in BeforeFlush is reflected in the
// INSERT; a value row is a copy and cannot be changed through the result.
func (c *ChangeSet) Inserted(table sqlkit.Table) []any {
	if c.frozen != nil {
		if tc := c.frozen[tableKey(table)]; tc != nil {
			return append([]any(nil), tc.inserted...)
		}
		return nil
	}
	ts := c.tableState(table)
	if ts == nil {
		return nil
	}
	return append([]any(nil), ts.inserts...)
}

// Removed returns the primary keys staged for DELETE on table, in staging order.
func (c *ChangeSet) Removed(table sqlkit.Table) [][]any {
	if c.frozen != nil {
		if tc := c.frozen[tableKey(table)]; tc != nil {
			return append([][]any(nil), tc.removed...)
		}
		return nil
	}
	ts := c.tableState(table)
	if ts == nil {
		return nil
	}
	out := make([][]any, len(ts.removed))
	for i, r := range ts.removed {
		out[i] = append([]any(nil), r.pk...)
	}
	return out
}

// Empty reports whether the flush has (in AfterFlush: had) no work to do.
func (c *ChangeSet) Empty() bool {
	if c.frozen != nil {
		for _, tc := range c.frozen {
			if len(tc.dirty) > 0 || len(tc.inserted) > 0 || len(tc.removed) > 0 {
				return false
			}
		}
		return true
	}
	return !c.scope.hasPending()
}

// tableState looks up the staged state for table without creating it, so a hook
// querying a table with no pending work gets nil rather than registering it.
func (c *ChangeSet) tableState(table sqlkit.Table) *tableState {
	return c.scope.tables[tableKey(table)]
}

// tableKey is the scope's map key for table.
func tableKey(table sqlkit.Table) string {
	info := table.Info()
	return meta.TableKey{Schema: info.Schema, Name: info.Name, Checksum: info.Checksum}.QualifiedName()
}

// captureChanges snapshots the staged work as a frozen ChangeSet for the
// AfterFlush hooks, or returns nil when no AfterFlush hook is registered (so a
// flush without one does no extra work). Explicit updates (Update) are reported
// with the dirty rows, since both are the rows the flush UPDATEs.
func (s *Scope) captureChanges() *ChangeSet {
	if !s.hasAfterFlushHook() {
		return nil
	}
	live := &ChangeSet{scope: s}
	frozen := map[string]*tableChanges{}
	for _, name := range s.order {
		ts := s.tables[name]
		tc := &tableChanges{
			dirty:    append(live.Dirty(ts.table), ts.updates...),
			inserted: live.Inserted(ts.table),
			removed:  live.Removed(ts.table),
		}
		frozen[name] = tc
	}
	return &ChangeSet{scope: s, frozen: frozen}
}

func (s *Scope) hasAfterFlushHook() bool {
	for _, h := range s.hooks {
		if _, ok := h.(AfterFlushHook); ok {
			return true
		}
	}
	return false
}

// runBeforeFlush invokes every registered BeforeFlushHook in order, stopping at
// the first error.
func (s *Scope) runBeforeFlush(ctx context.Context) error {
	if len(s.hooks) == 0 {
		return nil
	}
	cs := &ChangeSet{scope: s}
	for _, h := range s.hooks {
		if bh, ok := h.(BeforeFlushHook); ok {
			if err := bh.BeforeFlush(ctx, cs); err != nil {
				return err
			}
		}
	}
	return nil
}

// runAfterFlush invokes every registered AfterFlushHook in order with the
// snapshot of the flushed work, stopping at the first error. cs is nil when no
// AfterFlush hook is registered.
func (s *Scope) runAfterFlush(ctx context.Context, cs *ChangeSet) error {
	if cs == nil {
		return nil
	}
	for _, h := range s.hooks {
		if ah, ok := h.(AfterFlushHook); ok {
			if err := ah.AfterFlush(ctx, cs); err != nil {
				return err
			}
		}
	}
	return nil
}
