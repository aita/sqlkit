package sql

import "testing"

// TestExtensionCoverage exercises the extension builders' options and the
// Stmt/buildStmt/setPos plumbing.
func TestExtensionCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustCompile := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mustCompile(CreateExtension("pgcrypto").IfNotExists().Schema("public"))
	mustCompile(DropExtension("pgcrypto").IfExists().Cascade())

	_ = CreateExtension("x").Stmt()
	_ = CreateExtension("x").buildStmt()
	_ = DropExtension("x").Stmt()
	_ = DropExtension("x").buildStmt()
	for _, q := range []Query{
		CreateExtension("x"), CreateExtension("x").Stmt(),
		DropExtension("x"), DropExtension("x").Stmt(),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}
