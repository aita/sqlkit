package migrate

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"hash/fnv"
)

// migratorLock serializes concurrent Migrators against the same tracking
// table: without it, two Migrator instances (e.g. two pods mid-rollout) can
// both read "revision X not applied" and both apply it, double-running
// non-idempotent DDL/DML. It wraps a session-level advisory lock (PostgreSQL
// pg_advisory_lock, MySQL GET_LOCK) held on one pinned physical connection —
// not a transaction-scoped lock — so it survives across the several separate
// transactions applyPending opens (one per migration, per the DisableTx
// contract in migration.go) and is still released by the engine if the
// process dies mid-migration.
//
// Locking is automatic for the two dialects that support it (PostgreSQL,
// MySQL) and a silent no-op otherwise: a dialect without an advisory-lock
// primitive, or a database with no real *sql.DB behind it (WrapExecutor with
// a custom terminal, or the testutil.Fake driver's default DB() wrapping),
// cannot take a connection out of the pool for this purpose. There is
// nothing to opt into or out of — the two supported dialects are always
// protected, matching how existing dialect-shaped behavior in this package
// (e.g. timestampType, autoIncrIDType) branches on Dialect.Name() rather than
// exposing an Option.
type migratorLock struct {
	conn      *stdsql.Conn
	unlockSQL string
	unlockArg any
}

// lockKey derives the advisory-lock identity from the tracking table's
// qualified name, so Migrators pointed at different tables (WithTableName,
// WithSchema) do not contend with each other, while every Migrator sharing
// one table does.
func (m *Migrator) lockKey() string {
	return m.table.QualifiedName()
}

// acquireLock takes the advisory lock for the Migrator's tracking table, or
// returns a nil *migratorLock when the dialect or database has no locking
// primitive available. The lock is held until release is called.
func (m *Migrator) acquireLock(ctx context.Context) (*migratorLock, error) {
	dialect := m.db.Dialect().Name()
	if dialect != "postgres" && dialect != "mysql" {
		return nil, nil
	}
	sqlDB := m.db.SQL()
	if sqlDB == nil {
		return nil, nil
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: acquiring migration lock: %w", err)
	}

	key := m.lockKey()
	var lockSQL, unlockSQL string
	var lockArg, unlockArg any
	if dialect == "mysql" {
		name := mysqlLockName(key)
		lockSQL, lockArg = "SELECT GET_LOCK(?, -1)", name
		unlockSQL, unlockArg = "SELECT RELEASE_LOCK(?)", name
	} else {
		id := advisoryLockKey(key)
		lockSQL, lockArg = "SELECT pg_advisory_lock($1)", id
		unlockSQL, unlockArg = "SELECT pg_advisory_unlock($1)", id
	}

	if err := execQuery(ctx, conn, lockSQL, lockArg); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate: acquiring migration lock: %w", err)
	}
	return &migratorLock{conn: conn, unlockSQL: unlockSQL, unlockArg: unlockArg}, nil
}

// release unlocks and returns the pinned connection to the pool. It is safe
// to call on a nil *migratorLock (the no-locking case).
func (l *migratorLock) release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	unlockErr := execQuery(ctx, l.conn, l.unlockSQL, l.unlockArg)
	closeErr := l.conn.Close()
	if unlockErr != nil {
		return fmt.Errorf("migrate: releasing migration lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("migrate: releasing migration lock: %w", closeErr)
	}
	return nil
}

// execQuery runs a lock/unlock statement as a query rather than an exec:
// pg_advisory_lock/pg_advisory_unlock and GET_LOCK/RELEASE_LOCK are all
// value-returning SELECTs, not DML/DDL, so QueryContext is the correct call
// (ExecContext works against a real driver too, but some drivers/frameworks
// reject Exec-ing a SELECT). The single result row is read so the connection
// is left ready for the next statement.
func execQuery(ctx context.Context, conn *stdsql.Conn, query string, arg any) error {
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
