package integration

// SQLite integration coverage. Unlike the postgres/mysql suite (eachEngine,
// which shares testCatalog and needs a service container), these tests stand
// alone against the pure-Go modernc.org/sqlite driver on a throwaway file DB, so
// they always run — no Docker, no DSN env. They prove the SQLiteDialect added to
// the root sql package works end to end against a real engine: AUTOINCREMENT
// DDL, INSERT ... RETURNING, ON CONFLICT upsert, the -> / ->> JSON operators,
// canonical-function remapping, and the REGEXP operator (via a host-registered
// regexp() function, which SQLite requires since it ships none).

import (
	"context"
	"database/sql/driver"
	"path/filepath"
	"regexp"
	"testing"

	sqlite "modernc.org/sqlite"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

func init() {
	// SQLite invokes regexp(pattern, value) for the expression `value REGEXP
	// pattern`. Register one so sql.Regexp executes; available to connections
	// opened afterward.
	err := sqlite.RegisterScalarFunction("regexp", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		pattern, _ := args[0].(string)
		value, _ := args[1].(string)
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		if re.MatchString(value) {
			return int64(1), nil
		}
		return int64(0), nil
	})
	if err != nil {
		panic("integration: register sqlite regexp(): " + err.Error())
	}
}

// openSQLite opens a fresh file-backed SQLite database for one test.
func openSQLite(t *testing.T) *sqlkit.Database {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "sqlite.db")
	db, err := sqlkit.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("Open(sqlite) error = %v", err)
	}
	t.Cleanup(func() { _ = db.SQL().Close() })
	if got := db.Dialect().Name(); got != "sqlite" {
		t.Fatalf("Open inferred dialect %q, want sqlite", got)
	}
	return db
}

// createUsers creates a users table with an auto-increment primary key.
func createUsers(t *testing.T, db *sqlkit.Database) {
	t.Helper()
	create := sql.CreateTable(sql.Table{Name: "users"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.EngineType{Name: "integer"}, NotNull: true, AutoIncrement: true}).
		Column(sql.ColumnDef{Name: "email", Type: sql.EngineType{Name: "text"}, NotNull: true}).
		Column(sql.ColumnDef{Name: "name", Type: sql.EngineType{Name: "text"}}).
		PrimaryKey("id")
	if _, err := db.Exec(context.Background(), create); err != nil {
		t.Fatalf("CREATE TABLE error = %v", err)
	}
}

func TestSQLiteCreateInsertReturningSelect(t *testing.T) {
	db := openSQLite(t)
	createUsers(t, db)
	ctx := context.Background()

	// INSERT ... RETURNING id (SQLite >= 3.35).
	insert := sql.InsertInto(sql.Table{Name: "users"}).
		Columns("email", "name").Values("a@x.com", "Alice").Returning(sql.Col("id"))
	text, args, err := insert.ToSQL(db.Dialect())
	if err != nil {
		t.Fatalf("insert ToSQL() error = %v", err)
	}
	var id int64
	if err := db.SQL().QueryRowContext(ctx, text, args...).Scan(&id); err != nil {
		t.Fatalf("INSERT ... RETURNING error = %v", err)
	}
	if id != 1 {
		t.Fatalf("RETURNING id = %d, want 1", id)
	}

	// Read it back.
	sel := sql.Select(sql.Col("email")).From(sql.Table{Name: "users"}).
		Where(sql.Eq(sql.Col("id"), sql.Int(1)))
	text, args, err = sel.ToSQL(db.Dialect())
	if err != nil {
		t.Fatalf("select ToSQL() error = %v", err)
	}
	var email string
	if err := db.SQL().QueryRowContext(ctx, text, args...).Scan(&email); err != nil {
		t.Fatalf("SELECT error = %v", err)
	}
	if email != "a@x.com" {
		t.Fatalf("email = %q, want a@x.com", email)
	}
}

func TestSQLiteUpsert(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()
	// A unique email so ON CONFLICT has a target to arbitrate.
	create := sql.CreateTable(sql.Table{Name: "users"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.EngineType{Name: "integer"}, NotNull: true, AutoIncrement: true}).
		Column(sql.ColumnDef{Name: "email", Type: sql.EngineType{Name: "text"}, NotNull: true}).
		Column(sql.ColumnDef{Name: "name", Type: sql.EngineType{Name: "text"}}).
		PrimaryKey("id")
	if _, err := db.Exec(ctx, create); err != nil {
		t.Fatalf("CREATE TABLE error = %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, `CREATE UNIQUE INDEX uq_users_email ON "users" ("email")`); err != nil {
		t.Fatalf("CREATE INDEX error = %v", err)
	}

	upsert := func(name string) {
		ins := sql.InsertInto(sql.Table{Name: "users"}).
			Columns("email", "name").Values("a@x.com", name).
			OnConflict("email").DoUpdate(sql.Set("name", sql.Excluded("name")))
		text, args, err := ins.ToSQL(db.Dialect())
		if err != nil {
			t.Fatalf("upsert ToSQL() error = %v", err)
		}
		if _, err := db.SQL().ExecContext(ctx, text, args...); err != nil {
			t.Fatalf("upsert exec error = %v", err)
		}
	}
	upsert("Alice")
	upsert("Alice Updated") // conflict on email -> DO UPDATE name

	var name string
	if err := db.SQL().QueryRowContext(ctx, `SELECT "name" FROM "users" WHERE "email" = ?`, "a@x.com").Scan(&name); err != nil {
		t.Fatalf("SELECT error = %v", err)
	}
	if name != "Alice Updated" {
		t.Fatalf("name = %q, want Alice Updated (upsert should have updated)", name)
	}
}

func TestSQLiteJSONAccess(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, `CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, data TEXT)`); err != nil {
		t.Fatalf("CREATE TABLE error = %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO events (data) VALUES (?)`, `{"name":"alice","tags":["x","y"]}`); err != nil {
		t.Fatalf("INSERT error = %v", err)
	}

	// data ->> 'name'
	sel := sql.Select(sql.JSONGetText(sql.Col("data"), "name")).From(sql.Table{Name: "events"})
	text, args, err := sel.ToSQL(db.Dialect())
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	var name string
	if err := db.SQL().QueryRowContext(ctx, text, args...).Scan(&name); err != nil {
		t.Fatalf("JSON ->> error = %v (sql=%q)", err, text)
	}
	if name != "alice" {
		t.Fatalf("data ->> 'name' = %q, want alice", name)
	}

	// data ->> ['tags', 1]
	sel = sql.Select(sql.JSONGetText(sql.Col("data"), "tags", 1)).From(sql.Table{Name: "events"})
	text, args, _ = sel.ToSQL(db.Dialect())
	var tag string
	if err := db.SQL().QueryRowContext(ctx, text, args...).Scan(&tag); err != nil {
		t.Fatalf("JSON array index error = %v (sql=%q)", err, text)
	}
	if tag != "y" {
		t.Fatalf("data ->> tags[1] = %q, want y", tag)
	}
}

func TestSQLiteFuncRemap(t *testing.T) {
	db := openSQLite(t)
	// Now() must remap to CURRENT_TIMESTAMP, which SQLite evaluates.
	sel := sql.Select(sql.As(sql.Now(), "ts"))
	text, args, err := sel.ToSQL(db.Dialect())
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT CURRENT_TIMESTAMP AS "ts"`; text != want {
		t.Fatalf("Now() compiled to %q, want %q", text, want)
	}
	var ts string
	if err := db.SQL().QueryRowContext(context.Background(), text, args...).Scan(&ts); err != nil {
		t.Fatalf("CURRENT_TIMESTAMP exec error = %v", err)
	}
	if ts == "" {
		t.Fatal("CURRENT_TIMESTAMP returned empty")
	}
}

func TestSQLiteRegexp(t *testing.T) {
	db := openSQLite(t)
	createUsers(t, db)
	ctx := context.Background()
	for _, name := range []string{"alice", "bob"} {
		if _, err := db.SQL().ExecContext(ctx, `INSERT INTO "users" ("email", "name") VALUES (?, ?)`, name+"@x.com", name); err != nil {
			t.Fatalf("INSERT error = %v", err)
		}
	}

	// name REGEXP '^a' matches only alice (uses the registered regexp() function).
	sel := sql.Select(sql.Col("name")).From(sql.Table{Name: "users"}).
		Where(sql.Regexp(sql.Col("name"), sql.Val("^a"))).OrderBy(sql.Asc(sql.Col("name")))
	text, args, err := sel.ToSQL(db.Dialect())
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	rows, err := db.SQL().QueryContext(ctx, text, args...)
	if err != nil {
		t.Fatalf("REGEXP query error = %v (sql=%q)", err, text)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan error = %v", err)
		}
		got = append(got, n)
	}
	if len(got) != 1 || got[0] != "alice" {
		t.Fatalf("REGEXP '^a' matched %v, want [alice]", got)
	}
}
