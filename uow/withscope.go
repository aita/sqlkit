package uow

import (
	"context"

	"github.com/aita/sqlkit"
)

// WithScope opens a session on db, runs fn against a Unit of Work bound to it,
// and on success flushes the scope and commits the session; on error or panic
// the session rolls back and nothing is committed. It is the Unit-of-Work
// counterpart of Database.WithSession, folding the NewScope and final Flush into
// the callback boundary so a transactional unit of work reads as one block:
//
//	err := uow.WithScope(ctx, db, func(s *uow.Scope) error {
//		var u User
//		if _, err := s.Get(ctx, app.Users, &u, 1); err != nil {
//			return err
//		}
//		u.Name = "renamed"           // mutate in place
//		s.Add(app.Users, &newUser)   // stage an insert
//		return nil                   // WithScope flushes, then commits
//	})
//
// The scope shares the session's transaction, so its loads and its flush run
// together and nested scopes use savepoints within fn. opts configure the scope
// (WithAutoflush, WithFlushHook, WithBatchSize, …). fn may call Flush itself for
// intermediate rounds; WithScope's closing Flush then emits only what remains.
func WithScope(ctx context.Context, db *sqlkit.Database, fn func(*Scope) error, opts ...Option) error {
	return db.WithSession(ctx, func(s *sqlkit.Session) error {
		scope := NewScope(s, opts...)
		if err := fn(scope); err != nil {
			return err
		}
		return scope.Flush(ctx)
	})
}
