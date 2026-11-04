package sqltest

import (
	"context"
	stdsql "database/sql"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
)

// NewRaw wraps a standard *sql.DB as a throwaway test database for code that
// does not use a sqlkit catalog (meta.Metadata) — a project on database/sql,
// sqlx, or pgx that only wants sqltest's schema lifecycle, isolation, and
// seeding helpers. The schema is applied from raw DDL with SetupSchemaSQL or
// ApplySchemaSQL rather than from a rendered catalog; everything else (Tx,
// Snapshot, the table-name seeding helpers) works the same.
//
//	tdb := sqltest.NewRaw(openTestDB(t), sqlkit.PostgreSQLDialect{})
//	tdb.SetupSchemaSQL(t, `
//		CREATE TABLE users (id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL);
//		CREATE TABLE posts (id BIGSERIAL PRIMARY KEY, user_id BIGINT REFERENCES users(id));
//	`)
//	// ... seed and exercise the code under test ...
//
// dialect is sqlkit.PostgreSQLDialect{} or sqlkit.MySQLDialect{}; it selects the
// foreign-key-safe teardown strategy (PostgreSQL CASCADE, MySQL foreign-key
// checks toggled) and the identifier quoting used to drop the tables. db must be
// opened against a disposable test database — a container, a CI service
// database, or a scratch schema — never production: constructing a DB is the
// safety bar exactly as with New.
//
// The metadata-driven methods (SetupSchema, ApplySchema, Truncate with no
// arguments) have no schema to act on a raw DB; use SetupSchemaSQL/ApplySchemaSQL
// and TruncateTables instead.
func NewRaw(db *stdsql.DB, dialect sqlkit.Dialect) *DB {
	return &DB{db: sqlkit.WrapDB(db, sqlkit.WithDialect(dialect)), raw: true}
}

// SetupSchemaSQL applies the given DDL now and drops the tables it creates on
// t.Cleanup, leaving the database empty — the raw-DDL counterpart of
// SetupSchema. Each argument may hold several semicolon-separated statements,
// and several arguments may be passed (e.g. one embedded .sql file per
// argument); they run in order. Before creating, it drops any table the DDL
// declares (foreign-key-safely), so a leftover table from a crashed run does
// not fail the CREATE.
//
// Only the names of tables created by CREATE TABLE statements are tracked for
// teardown; other objects (types, sequences, functions, tables created through
// constructs this does not recognise) are not dropped automatically. Add their
// teardown with a t.Cleanup of your own when needed.
func (d *DB) SetupSchemaSQL(t testing.TB, ddl ...string) {
	t.Helper()
	names := d.applySchemaSQL(t, ddl)
	t.Cleanup(func() { dropTableNames(t, d.db, names) })
}

// ApplySchemaSQL applies the given DDL with no teardown registered — the raw-DDL
// counterpart of ApplySchema. It is SetupSchemaSQL without the automatic drop on
// cleanup: reach for it to reset the schema again within a test, or when
// something else owns the teardown. The names of the created tables are recorded
// so a later SetupSchemaSQL, TruncateTables, or manual drop can find them.
func (d *DB) ApplySchemaSQL(t testing.TB, ddl ...string) {
	t.Helper()
	d.applySchemaSQL(t, ddl)
}

// applySchemaSQL splits the DDL into statements, drops the tables they declare
// (so re-applying is idempotent and a leftover table never blocks the CREATE),
// executes them in order, and records the created table names for teardown. It
// returns the names this call created so its own cleanup drops exactly those,
// while d.tables accumulates every table across calls for TruncateTables.
func (d *DB) applySchemaSQL(t testing.TB, ddl []string) []string {
	t.Helper()
	mysql := isMySQL(d.db.Dialect())
	var stmts []scriptStmt
	for _, text := range ddl {
		stmts = append(stmts, splitScript(text, mysql)...)
	}
	names := createdTableNames(stmts, mysql)
	dropTableNames(t, d.db, names)
	ctx := context.Background()
	for _, stmt := range stmts {
		if _, err := d.db.SQL().ExecContext(ctx, stmt.text); err != nil {
			t.Fatalf("sqltest: apply schema statement %q: %v", stmt.text, err)
		}
	}
	for _, name := range names {
		if !slices.Contains(d.tables, name) {
			d.tables = append(d.tables, name)
		}
	}
	return names
}

// TruncateTables deletes every row from the named tables foreign-key-safely,
// whatever order they are given in — the raw-DDL counterpart of Truncate, which
// needs a meta.Metadata to resolve sqlkit.Table bindings. Called with no names
// it clears every table the raw schema created (the tables ApplySchemaSQL or
// SetupSchemaSQL recorded).
func (d *DB) TruncateTables(t testing.TB, names ...string) {
	t.Helper()
	if len(names) == 0 {
		names = d.tables
	}
	truncateTableNames(t, d.db, names)
}

// createTableRE matches the table name of a CREATE TABLE statement, tolerating
// the IF NOT EXISTS clause and the TEMP/TEMPORARY and UNLOGGED qualifiers. The
// name is captured up to the first whitespace or opening parenthesis, so a
// quoted, back-quoted, bracketed, or schema-qualified name comes through whole
// and is unquoted by createdTableNames.
var createTableRE = regexp.MustCompile(`(?is)\bCREATE\s+(?:(?:GLOBAL|LOCAL)\s+)?(?:TEMP(?:ORARY)?\s+|UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([^\s(]+)`)

// createdTableNames extracts, in order, the qualified names of the tables the
// statements create, matching on each statement's comment-free code so a
// comment mentioning "create table" is never mistaken for the real one.
// Statements that are not CREATE TABLE are ignored.
func createdTableNames(stmts []scriptStmt, mysql bool) []string {
	var names []string
	for _, stmt := range stmts {
		m := createTableRE.FindStringSubmatch(stmt.code)
		if m == nil {
			continue
		}
		names = append(names, normalizeIdent(m[1], mysql))
	}
	return names
}

// normalizeIdent turns a possibly dot-qualified identifier as written in DDL
// into the bare schema.table form QuoteIdent expects. Quoting characters
// ("...", `...`, [...]) are removed; on PostgreSQL an unquoted part is folded to
// lower case, as the server does, so CREATE TABLE Users is dropped as "users"
// rather than as a "Users" that does not exist.
func normalizeIdent(s string, mysql bool) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		quoted := len(p) >= 2 && strings.ContainsRune("\"`[", rune(p[0]))
		p = strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "").Replace(p)
		if !quoted && !mysql {
			p = strings.ToLower(p)
		}
		parts[i] = p
	}
	return strings.Join(parts, ".")
}
