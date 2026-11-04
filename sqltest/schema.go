package sqltest

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/meta"
)

// applySchema resets md's tables on db: it drops them foreign-key-safely (so an
// empty database is a no-op and declaration order never matters), then executes
// the CREATE SQL the meta model renders for db's dialect. DB.ApplySchema and New
// drive it.
func applySchema(t testing.TB, db *sqlkit.Database, md meta.Metadata) {
	t.Helper()
	dropTables(t, db, md)
	createText, err := md.CreateSQL(db.Dialect())
	if err != nil {
		t.Fatalf("sqltest: render schema: %v", err)
	}
	ctx := context.Background()
	for _, stmt := range splitStatements(createText) {
		if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("sqltest: apply schema statement %q: %v", stmt, err)
		}
	}
}

// truncate deletes every row from the named tables foreign-key-safely, whatever
// order they were declared in: on MySQL it disables foreign-key checks for the
// duration, and on PostgreSQL it issues a single TRUNCATE ... CASCADE so a row
// referenced from another table (or a cycle between the listed tables) never
// blocks the delete. With no tables it clears every table in md. DB.Truncate
// drives it.
func truncate(t testing.TB, db *sqlkit.Database, md meta.Metadata, tables []sqlkit.Table) {
	t.Helper()
	names := truncateNames(md, tables)
	if len(names) == 0 {
		return
	}
	ctx := context.Background()
	exec := func(stmt string) {
		if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("sqltest: truncate: %v (%s)", err, stmt)
		}
	}
	dialect := db.Dialect()
	if isMySQL(dialect) {
		// MySQL TRUNCATE cannot touch a table referenced by a foreign key, so
		// disable the checks and clear each table individually.
		exec("SET FOREIGN_KEY_CHECKS = 0")
		defer exec("SET FOREIGN_KEY_CHECKS = 1")
		for _, name := range names {
			exec("TRUNCATE TABLE " + dialect.QuoteIdent(name))
		}
		return
	}
	// PostgreSQL truncates all tables in one CASCADE statement: order does not
	// matter and dependent rows in tables not listed are cleared too.
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = dialect.QuoteIdent(name)
	}
	exec("TRUNCATE TABLE " + strings.Join(quoted, ", ") + " CASCADE")
}

// truncateNames resolves the qualified names to clear: the given table bindings,
// or every table in md when none are named.
func truncateNames(md meta.Metadata, tables []sqlkit.Table) []string {
	if len(tables) == 0 {
		names := make([]string, len(md.Tables))
		for i, tbl := range md.Tables {
			names[i] = qualifiedName(tbl)
		}
		return names
	}
	names := make([]string, len(tables))
	for i, tbl := range tables {
		names[i] = tbl.Info().QualifiedName()
	}
	return names
}

// dropTables removes md's tables foreign-key-safely, independent of the order
// they were declared in. The drops are issued directly rather than via
// Metadata.DropSQL because that drops foreign-key constraints with ALTER TABLE
// first, which fails on the not-yet-created tables of a fresh database.
//
// Plain DROP TABLE in reverse declaration order only works when that order
// happens to be a topological sort of the foreign keys; it breaks on tables
// declared parent-after-child, on cross-schema references, and on cycles. So the
// drop is made order-independent per dialect: PostgreSQL drops each table with
// CASCADE (which also drops the foreign-key constraints pointing at it), and
// MySQL disables foreign-key checks for the duration.
func dropTables(t testing.TB, db *sqlkit.Database, md meta.Metadata) {
	t.Helper()
	ctx := context.Background()
	exec := func(stmt string) {
		if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("sqltest: drop tables: %v (%s)", err, stmt)
		}
	}
	mysql := isMySQL(db.Dialect())
	if mysql {
		exec("SET FOREIGN_KEY_CHECKS = 0")
		defer exec("SET FOREIGN_KEY_CHECKS = 1")
	}
	for i := len(md.Tables) - 1; i >= 0; i-- {
		stmt := "DROP TABLE IF EXISTS " + db.Dialect().QuoteIdent(qualifiedName(md.Tables[i]))
		if !mysql {
			stmt += " CASCADE"
		}
		exec(stmt)
	}
}

// isMySQL reports whether the dialect is MySQL, which needs FOREIGN_KEY_CHECKS
// toggled rather than CASCADE to drop or truncate foreign-key-linked tables.
func isMySQL(d sqlkit.Dialect) bool { return d.Name() == "mysql" }

func qualifiedName(tbl meta.Table) string {
	if tbl.Schema.Name == "" {
		return tbl.Name
	}
	return tbl.Schema.Name + "." + tbl.Name
}

// splitStatements breaks rendered DDL into individual statements. The generated
// DDL never contains a semicolon inside a string literal or identifier, so a
// plain split on ";" is sufficient.
func splitStatements(ddlText string) []string {
	var out []string
	for _, part := range strings.Split(ddlText, ";") {
		if stmt := strings.TrimSpace(part); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
