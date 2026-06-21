package sql

import "testing"

// TestDDLBuilderPlumbing exercises the accessor "plumbing" every DDL builder and
// statement shares — Stmt(), buildStmt(), and setPos() — which the feature tests
// in ddl_test.go don't all reach. These are uniform across the DDL types, so a
// single table keeps them covered.

// ddlBuilder is the shared shape of every DDL fluent builder.
type ddlBuilder interface {
	buildStmt() Query
	setPos(int) Query
}

func TestDDLBuilderPlumbing(t *testing.T) {
	view := Select(Col("id")).From(orgs)

	// Stmt() returns a distinct concrete type per builder, so call each directly.
	_ = CreateSchema("s").Stmt()
	_ = DropSchema("s").Stmt()
	_ = CreateTable(users).Stmt()
	_ = DropTable(users).Stmt()
	_ = CreateView(users, view).Stmt()
	_ = DropView(users).Stmt()
	_ = CreateIndex("i", users, "id").Stmt()
	_ = AlterTable(users).Stmt()

	// buildStmt() and the builder's setPos() are uniform: drive them via the
	// shared interface.
	builders := []ddlBuilder{
		CreateSchema("s"),
		DropSchema("s"),
		CreateTable(users),
		DropTable(users),
		CreateView(users, view),
		DropView(users),
		CreateIndex("i", users, "id"),
		AlterTable(users),
	}
	for _, b := range builders {
		if q := b.buildStmt(); q == nil {
			t.Fatalf("%T.buildStmt() = nil", b)
		}
		if q := b.setPos(5); q == nil {
			t.Fatalf("%T.setPos() = nil", b)
		}
	}

	// Every DDL statement value (including the two that have no builder,
	// DropIndex/DropForeignKey) carries setPos via the Query plumbing.
	stmts := []Query{
		CreateSchema("s").Stmt(),
		DropSchema("s").Stmt(),
		CreateTable(users).Stmt(),
		DropTable(users).Stmt(),
		CreateView(users, view).Stmt(),
		DropView(users).Stmt(),
		CreateIndex("i", users, "id").Stmt(),
		AlterTable(users).Stmt(),
		DropIndex("i", users),
		DropForeignKey(users, "fk"),
	}
	for _, s := range stmts {
		sp, ok := s.(interface{ setPos(int) Query })
		if !ok {
			t.Fatalf("%T has no setPos", s)
		}
		if sp.setPos(7) == nil {
			t.Fatalf("%T.setPos() = nil", s)
		}
	}
}
