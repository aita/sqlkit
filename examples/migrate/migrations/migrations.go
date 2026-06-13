// Package migrations holds the example's database migrations, each authored with
// the sqldef DSL. Each migration lives in its own file and registers itself with
// the package-level Registry in an init function, so adding a migration is
// dropping in a new file — no central list to edit.
//
// The migrations are applied with the sqlkit binary, which compiles this package
// and runs it — no app code of its own:
//
//	export DATABASE_URL=postgres://localhost/app
//	go run github.com/aita/sqlkit/cmd/sqlkit migrate up --migrations ./migrations
//
// The Migrator resolves the apply order from the revision DAG, so registration
// order does not matter; the files are numbered only for human readability.
package migrations

import "github.com/aita/sqlkit/migrate"

// Registry collects every migration declared in this package. The sqlkit binary
// (or your own program) builds a Migrator from it with Registry.NewMigrator(db).
var Registry = migrate.NewRegistry()
