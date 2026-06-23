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

// AfterFlushHook is invoked after a Flush has emitted its statements and
// refreshed managed snapshots. Returning an error propagates from Flush; note a
// Database-backed scope auto-commits each statement, so an AfterFlush error
// cannot undo work already committed — bind the scope to a session for that.
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

// ChangeSet is a read-only view of the work a Flush performs, passed to flush
// hooks: in BeforeFlush it describes what is about to run, in AfterFlush what
// just ran (by which point the staged work is cleared). Its accessors report
// the rows staged for a given table; to stamp a column on the rows about to
// change, mutate the pointers Dirty returns from within BeforeFlush.
type ChangeSet struct{ scope *Scope }

// Dirty returns the managed entity pointers of table whose columns changed since
// they were loaded or attached — the rows the flush will UPDATE. A row also
// staged for removal is excluded. Mutating a returned pointer in BeforeFlush is
// reflected in the UPDATE the flush emits.
func (c *ChangeSet) Dirty(table sqlkit.Table) []any {
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
	ts := c.tableState(table)
	if ts == nil {
		return nil
	}
	return append([]any(nil), ts.inserts...)
}

// Removed returns the primary keys staged for DELETE on table, in staging order.
func (c *ChangeSet) Removed(table sqlkit.Table) [][]any {
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

// Empty reports whether the flush has no work to do.
func (c *ChangeSet) Empty() bool { return !c.scope.hasPending() }

// tableState looks up the staged state for table without creating it, so a hook
// querying a table with no pending work gets nil rather than registering it.
func (c *ChangeSet) tableState(table sqlkit.Table) *tableState {
	info := table.Info()
	key := meta.TableKey{Schema: info.Schema, Name: info.Name, Checksum: info.Checksum}
	return c.scope.tables[key.QualifiedName()]
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

// runAfterFlush invokes every registered AfterFlushHook in order, stopping at the
// first error.
func (s *Scope) runAfterFlush(ctx context.Context) error {
	if len(s.hooks) == 0 {
		return nil
	}
	cs := &ChangeSet{scope: s}
	for _, h := range s.hooks {
		if ah, ok := h.(AfterFlushHook); ok {
			if err := ah.AfterFlush(ctx, cs); err != nil {
				return err
			}
		}
	}
	return nil
}
