package uow

import (
	"context"

	"github.com/aita/sqlkit"
)

// WithAutoflush makes the scope flush its pending work before each Get and Find,
// so a read observes the scope's own staged inserts/updates/deletes and the
// dirty changes of its managed entities — the autoflush behavior of an ORM
// session. Without it a read runs against the database as last flushed and does
// not see work still buffered on the scope.
//
// The flush runs only when there is pending work, so reads on an idle scope cost
// nothing extra. On a session-backed scope it shares the transaction (and a
// nested scope flushes within its savepoint); on a Database-backed scope each
// flushed statement auto-commits, as it would on an explicit Flush. The setting
// is inherited by nested scopes.
func WithAutoflush(enabled bool) Option {
	return func(s *Scope) { s.autoflush = enabled }
}

// maybeAutoflush flushes pending work ahead of a read when autoflush is on. It is
// a no-op when autoflush is off or nothing is staged, so an idle read is free.
func (s *Scope) maybeAutoflush(ctx context.Context) error {
	if !s.autoflush || !s.hasPending() {
		return nil
	}
	return s.Flush(ctx)
}

// hasPending reports whether the scope holds work a Flush would emit: any staged
// insert, explicit update, or removal, or a managed entity whose columns changed
// since it was loaded.
func (s *Scope) hasPending() bool {
	for _, name := range s.order {
		ts := s.tables[name]
		if len(ts.inserts) > 0 || len(ts.updates) > 0 || len(ts.removed) > 0 {
			return true
		}
		for _, tr := range ts.managed {
			if s.entityDirty(ts, tr) {
				return true
			}
		}
	}
	return false
}

// entityDirty reports whether a managed entity has a column change a Flush would
// write. A row also staged for removal is not dirty — it will be deleted, not
// updated. Change detection mirrors flushManaged: the raw values drive the diff
// while the write-ready values supply what would be emitted.
func (s *Scope) entityDirty(ts *tableState, tr *tracked) bool {
	if ts.removedIdx[pkString(ts.key, tr.pk)] {
		return false
	}
	names, vals, err := sqlkit.ColumnValues(ts.table, tr.ptr)
	if err != nil {
		return false
	}
	_, wvals, err := sqlkit.ColumnWriteValues(ts.table, tr.ptr)
	if err != nil {
		return false
	}
	return len(diffAssignments(ts.table, tr, names, vals, wvals)) > 0
}
