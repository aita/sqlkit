package migrate

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
)

// The migration lock serializes concurrent Migrators against the same tracking
// table: without it, two Migrator instances (e.g. two pods mid-rollout) can both
// read "revision X not applied" and both apply it, double-running
// non-idempotent DDL/DML. It is a session-level advisory lock (PostgreSQL
// pg_advisory_lock, MySQL GET_LOCK) taken on one connection pinned from the
// pool — not a transaction-scoped lock — so it spans the several transactions a
// run opens (one per migration, per the DisableTx contract) and is still
// released by the engine if the process dies mid-migration.
//
// The whole run executes on that same pinned connection (sqlkit's
// Database.OnConn), so the lock costs no extra connection: a pool limited to
// one connection (SetMaxOpenConns(1)) works instead of deadlocking on itself.
//
// Locking is automatic for PostgreSQL and MySQL and a no-op otherwise: a
// dialect without an advisory-lock primitive, or a database with no *sql.DB
// behind it (WrapExecutor), runs unlocked exactly as before.

// withLock runs fn holding the migration lock, passing a copy of m whose
// database is bound to the locked connection. Without a locking primitive it
// runs fn on m unlocked.
func (m *Migrator) withLock(ctx context.Context, fn func(*Migrator) error) (err error) {
	dialect := m.db.Dialect().Name()
	sqlDB := m.db.SQL()
	if (dialect != "postgres" && dialect != "mysql") || sqlDB == nil {
		return fn(m)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquiring migration lock: %w", err)
	}

	lockSQL, unlockSQL, arg := lockStatements(dialect, m.table.QualifiedName())
	if err := queryOne(ctx, conn, lockSQL, arg); err != nil {
		_ = conn.Close()
		return fmt.Errorf("migrate: acquiring migration lock: %w", err)
	}
	defer func() {
		if relErr := releaseLock(ctx, conn, unlockSQL, arg); relErr != nil && err == nil {
			err = relErr
		}
	}()

	locked := *m
	locked.db = m.db.OnConn(conn)
	return fn(&locked)
}

// lockStatements returns the dialect's lock and unlock statements and their
// shared argument for the lock named key.
func lockStatements(dialect, key string) (lockSQL, unlockSQL string, arg any) {
	if dialect == "mysql" {
		return "SELECT GET_LOCK(?, -1)", "SELECT RELEASE_LOCK(?)", mysqlLockName(key)
	}
	return "SELECT pg_advisory_lock($1)", "SELECT pg_advisory_unlock($1)", advisoryLockKey(key)
}

// releaseLock unlocks and returns the pinned connection to the pool. The unlock
// runs on a context that ignores the caller's cancellation, since a cancelled
// run must still release. If the unlock fails anyway, the connection is
// discarded rather than returned to the pool: closing the session is what
// releases a session-level lock, whereas a pooled connection still holding it
// would block every later Migrator.
func releaseLock(ctx context.Context, conn *stdsql.Conn, unlockSQL string, arg any) error {
	unlockErr := queryOne(context.WithoutCancel(ctx), conn, unlockSQL, arg)
	if unlockErr != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	closeErr := conn.Close()
	if unlockErr != nil {
		return fmt.Errorf("migrate: releasing migration lock: %w", unlockErr)
	}
	if closeErr != nil && !errors.Is(closeErr, stdsql.ErrConnDone) {
		return fmt.Errorf("migrate: releasing migration lock: %w", closeErr)
	}
	return nil
}

// queryOne runs a lock/unlock statement, which returns a single value, as a
// query and drains its row so the connection is ready for the next statement.
func queryOne(ctx context.Context, conn *stdsql.Conn, query string, arg any) error {
	rows, err := conn.QueryContext(ctx, query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	var discard any
	if rows.Next() {
		if err := rows.Scan(&discard); err != nil {
			return err
		}
	}
	return rows.Err()
}

// advisoryLockKey hashes name to the int64 pg_advisory_lock takes, giving
// every Migrator on the same tracking table the same key while keeping
// Migrators on different tables (WithTableName, WithSchema) independent.
func advisoryLockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

// mysqlLockName clamps name to the 64-character limit MySQL's GET_LOCK
// enforces on lock names, hashing it down when it would overflow.
func mysqlLockName(name string) string {
	const maxLen = 64
	if len(name) <= maxLen {
		return name
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%x", h.Sum64())
}
