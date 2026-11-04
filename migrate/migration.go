// Package migrate runs database schema migrations written with callbacks that
// receive an Operator. A Migration's Up and Down functions call
// op.CreateTable / op.AddColumn / op.DropColumn / op.CreateIndex /
// op.CreateForeignKey / ... to perform the change; columns are declared with the
// decl DSL (portable types and options), and the migrate package never asks
// the author to touch the sql package directly. The Migrator applies pending
// migrations inside a transaction and records every apply and rollback in an
// append-only history table (schema_migrations by default).
//
// Migrations form a DAG: each carries a Revision id and the DownRevision it
// builds on (empty for the first). The Migrator resolves the
// apply order from those links, not from declaration order. Keep each migration
// in its own file and register it with a Registry:
//
//	var _ = Registry.Add(migrate.Migration{
//		Revision:     "8f3a1c", // unique id; migrate.NewRevision() generates one
//		DownRevision: "",       // the revision this builds on ("" = first)
//		Name:         "create_tags",
//		Up: func(op *migrate.Operator) {
//			op.CreateTable("tags",
//				decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
//				decl.Column("name", decl.VarChar(64), decl.Unique()),
//			)
//		},
//		Down: func(op *migrate.Operator) { op.DropTable("tags") },
//	})
//
//	m := Registry.NewMigrator(db)
//	err := m.Up(ctx)
//
// Transactions: each migration applies in one transaction so its operations and
// its history row commit together. PostgreSQL DDL is transactional, so a failed
// migration rolls back cleanly. MySQL commits DDL implicitly and cannot roll a
// partly-applied migration back; treat MySQL migrations as non-atomic. Set
// DisableTx for a migration that must not run in a transaction (e.g. PostgreSQL
// CREATE INDEX CONCURRENTLY).
package migrate

import (
	"context"
	"crypto/rand"
	stdsql "database/sql"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/aita/sqlkit/sql"
)

// Execer is the execution surface the Operator runs statements against. Both
// *sqlkit.Database (auto-commit) and *sqlkit.Session (a transaction) satisfy it.
type Execer interface {
	Exec(ctx context.Context, query sql.Query) (stdsql.Result, error)
}

// Direction is whether a history event applied a migration or rolled it back.
type Direction string

// The two migration directions recorded in the history table.
const (
	DirectionUp   Direction = "up"
	DirectionDown Direction = "down"
)

// MigrationEvent is one recorded apply or rollback from the history table.
type MigrationEvent struct {
	Revision  string
	Name      string
	Direction Direction
	AppliedAt time.Time
}

// Migration is one schema change in the migration DAG. Revision is its unique
// id; DownRevision is the revision it builds on ("" for the first migration). A
// merge migration builds on several revisions at once: set DownRevisions to the
// heads it joins (it takes precedence over DownRevision). Up and Down receive an
// Operator and perform the change with its operations (op.CreateTable, etc.);
// data backfills use op.Exec / op.Execute. Down may be nil for a migration that
// cannot be rolled back.
type Migration struct {
	Revision      string
	DownRevision  string
	DownRevisions []string // multiple parents (merge); overrides DownRevision
	Name          string
	Up            func(*Operator)
	Down          func(*Operator)

	// DisableTx runs the migration outside a transaction (auto-commit). Needed
	// for statements a transaction rejects, e.g. CREATE INDEX CONCURRENTLY.
	DisableTx bool
}

// parents returns the revisions this migration builds on (empty for a root).
func (mig Migration) parents() []string {
	if len(mig.DownRevisions) > 0 {
		return mig.DownRevisions
	}
	if mig.DownRevision != "" {
		return []string{mig.DownRevision}
	}
	return nil
}

// Registry collects migrations declared across several files into one set, so a
// project can keep each migration in its own file and register it there rather
// than maintaining a single growing slice. Declare a package-level registry and
// have each file add to it with a package-level var (Add returns the registry,
// so the blank identifier discards it). Migrator order does not depend on
// registration order — it is resolved from each migration's Revision/
// DownRevision DAG links:
//
//	// migrations/migrations.go
//	var Registry = migrate.NewRegistry()
//
//	// migrations/0001_create_users.go
//	var _ = Registry.Add(migrate.Migration{Revision: "0001", Name: "create_users", Up: ...})
//
//	// main.go
//	m := migrations.Registry.NewMigrator(db)
//	err := m.Up(ctx)
//
// A Registry is safe for concurrent Add.
type Registry struct {
	mu         sync.Mutex
	migrations []Migration
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Add appends migrations to the registry and returns it, so it can be used as a
// package-level initializer (var _ = Registry.Add(...)).
func (r *Registry) Add(migrations ...Migration) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrations = append(r.migrations, migrations...)
	return r
}

// Migrations returns a copy of the registered migrations.
func (r *Registry) Migrations() []Migration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Migration(nil), r.migrations...)
}

// NewRevision returns a new random revision id (12 hex chars), a convenient
// collision-resistant value for Migration.Revision.
func NewRevision() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail; fall back to a time-based id.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
