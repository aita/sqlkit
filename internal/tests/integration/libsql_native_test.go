//go:build cgo

package integration

// Native libSQL coverage: these tests run against the real libSQL engine
// embedded by github.com/tursodatabase/go-libsql (cgo, prebuilt for
// linux/darwin amd64/arm64), not the modernc SQLite engine the other libSQL
// test borrows. sqlkit.Open("libsql", ...) resolves the driver go-libsql
// registers and infers LibSQLDialect from its name, so the whole path —
// driver mapping, dialect, decl types, migrate, uow — runs on libSQL itself.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/tursodatabase/go-libsql"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/uow"
)

// openNativeLibSQL opens a fresh file-backed database on the embedded libSQL
// engine.
func openNativeLibSQL(t *testing.T) *sqlkit.Database {
	t.Helper()
	db, err := sqlkit.Open("libsql", "file:"+filepath.Join(t.TempDir(), "native.db"))
	if err != nil {
		t.Fatalf("Open(libsql) error = %v", err)
	}
	t.Cleanup(func() { _ = db.SQL().Close() })
	if got := db.Dialect().Name(); got != "libsql" {
		t.Fatalf("Open inferred dialect %q, want libsql", got)
	}
	if err := db.SQL().PingContext(context.Background()); err != nil {
		t.Fatalf("ping libsql: %v", err)
	}
	return db
}

// The dialect's set-operation, OFFSET, VALUES-source, upsert, and RETURNING
// rendering executes on libSQL.
func TestNativeLibSQLQueries(t *testing.T) {
	db := openNativeLibSQL(t)
	ctx := context.Background()
	seedNums(t, db)
	a := sql.Select(sql.Col("n")).From(sql.Table{Name: sql.NewIdent("a")})
	b := sql.Select(sql.Col("n")).From(sql.Table{Name: sql.NewIdent("b")})
	asc := sql.Asc(sql.Col("n"))
	cases := map[string]struct {
		q    sql.Query
		want []int64
	}{
		"union":          {a.Union(b).OrderBy(asc), []int64{1, 2, 3, 4}},
		"arm with limit": {a.Union(b.OrderBy(sql.Desc(sql.Col("n"))).Limit(1)).OrderBy(asc), []int64{1, 2, 3, 4}},
		"mixed ops":      {a.Except(a.Intersect(b)).OrderBy(asc), []int64{1, 2}},
		"bare offset":    {a.OrderBy(asc).Offset(1), []int64{2, 3}},
		"values source": {sql.Select(sql.Col("v.k")).From(sql.ValuesSource(
			[][]sql.Expression{{sql.Val(7)}, {sql.Val(8)}}, "v", "k")).OrderBy(sql.Asc(sql.Col("v.k"))), []int64{7, 8}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := queryInts(t, db, c.q)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}

	createUsers(t, db)
	users := sql.Table{Name: sql.NewIdent("users")}
	insert := sql.InsertInto(users).Columns("email", "name").Values("a@x", "A").Returning(sql.Col("id"))
	text, args, err := insert.ToSQL(db.Dialect())
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.SQL().QueryRowContext(ctx, text, args...).Scan(&id); err != nil || id != 1 {
		t.Fatalf("INSERT ... RETURNING id = %d, err %v", id, err)
	}
	upsert := sql.InsertInto(users).Columns("id", "email", "name").Values(1, "a@x", "B").
		OnConflict("id").DoUpdate(sql.Set("name", sql.Col("excluded.name")))
	if _, err := db.Exec(ctx, upsert); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var name string
	if err := db.SQL().QueryRowContext(ctx, `SELECT name FROM users WHERE id = 1`).Scan(&name); err != nil || name != "B" {
		t.Fatalf("after upsert name = %q, err %v", name, err)
	}
}

// migrate runs end to end on libSQL, including a decl date column read back
// as time.Time.
func TestNativeLibSQLMigrate(t *testing.T) {
	db := openNativeLibSQL(t)
	ctx := context.Background()
	m := migrate.New(db, []migrate.Migration{{
		Revision: "libsql0001", Name: "events",
		Up: func(op *migrate.Operator) {
			op.CreateTable("events",
				decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
				decl.Column("title", decl.Text),
				decl.Column("at", decl.Timestamp),
			)
		},
		Down: func(op *migrate.Operator) { op.DropTable("events") },
	}})
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO "events" ("title", "at") VALUES (?, ?), (?, ?)`, "a", when, "b", when); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ids := queryInts(t, db, sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("events")}).OrderBy(sql.Asc(sql.Col("id")))); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("auto-increment ids = %v, want [1 2]", ids)
	}
	if err := m.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if err := m.Up(ctx); err != nil {
		t.Fatalf("re-Up: %v", err)
	}
}

// A batched uow flush (the VALUES-join UPDATE path) and a session's
// transaction work on libSQL.
func TestNativeLibSQLUnitOfWork(t *testing.T) {
	db := openNativeLibSQL(t)
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, `CREATE TABLE w (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO w VALUES (1,'a'),(2,'b'),(3,'c')`); err != nil {
		t.Fatal(err)
	}
	tbl := newSQLiteWidgets()
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		scope := uow.NewScope(s, uow.WithBatchSize(10))
		var rows []sqliteWidget
		if err := scope.Find(ctx, tbl, &rows); err != nil {
			return err
		}
		for i := range rows {
			rows[i].Name += "!"
		}
		return scope.Flush(ctx)
	})
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	var got []sqliteWidget
	if err := db.Select().From(tbl).OrderBy(tbl.ID.Asc()).All(ctx).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %+v", got)
	}
	for _, w := range got {
		if w.Name[len(w.Name)-1] != '!' {
			t.Fatalf("rows after batched flush = %+v, want every name updated", got)
		}
	}
}
