package integration

// SQLite execution coverage for the dialect's shape fixes: each case compiles a
// builder for SQLiteDialect and runs it on the real engine, so SQL SQLite
// rejects (parenthesized set-operation arms, a bare OFFSET, an aliased VALUES
// list) fails here rather than only in production.

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/uow"
)

// queryInts runs q on db and returns the first column of every row.
func queryInts(t *testing.T, db *sqlkit.Database, q sql.Query) []int64 {
	t.Helper()
	text, args, err := q.ToSQL(db.Dialect())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	rows, err := db.SQL().QueryContext(context.Background(), text, args...)
	if err != nil {
		t.Fatalf("query %s: %v", text, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func seedNums(t *testing.T, db *sqlkit.Database) {
	t.Helper()
	for _, s := range []string{
		`CREATE TABLE a (n INTEGER)`, `INSERT INTO a VALUES (1),(2),(3)`,
		`CREATE TABLE b (n INTEGER)`, `INSERT INTO b VALUES (3),(4)`,
	} {
		if _, err := db.SQL().Exec(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteSetOperationsAndOffset(t *testing.T) {
	db := openSQLite(t)
	seedNums(t, db)
	a := sql.Select(sql.Col("n")).From(sql.Table{Name: sql.NewIdent("a")})
	b := sql.Select(sql.Col("n")).From(sql.Table{Name: sql.NewIdent("b")})
	asc := sql.Asc(sql.Col("n"))
	cases := map[string]struct {
		q    sql.Query
		want []int64
	}{
		"union":           {a.Union(b).OrderBy(asc), []int64{1, 2, 3, 4}},
		"union all chain": {a.UnionAll(b).UnionAll(b).OrderBy(asc), []int64{1, 2, 3, 3, 3, 4, 4}},
		"arm with limit":  {a.Union(b.OrderBy(sql.Desc(sql.Col("n"))).Limit(1)).OrderBy(asc), []int64{1, 2, 3, 4}},
		"except":          {a.Except(b).OrderBy(asc), []int64{1, 2}},
		"mixed ops":       {a.Except(a.Intersect(b)).OrderBy(asc), []int64{1, 2}},
		"select offset":   {a.OrderBy(asc).Offset(1), []int64{2, 3}},
		"union offset":    {a.Union(b).OrderBy(asc).Offset(2), []int64{3, 4}},
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
}

// migrate runs end to end on SQLite: the tracking table and a decl-declared
// table with an auto-increment primary key.
func TestSQLiteMigrateUpDown(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()
	m := migrate.New(db, []migrate.Migration{{
		Revision: "sqlite0001", Name: "widgets",
		Up: func(op *migrate.Operator) {
			op.CreateTable("widgets",
				decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
				decl.Column("name", decl.Text),
				decl.Column("made_on", decl.Date, decl.Nullable()),
			)
		},
		Down: func(op *migrate.Operator) { op.DropTable("widgets") },
	}})
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO "widgets" ("name") VALUES ('a'), ('b')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ids := queryInts(t, db, sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("widgets")}).OrderBy(sql.Asc(sql.Col("id")))); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("auto-increment ids = %v, want [1 2]", ids)
	}
	if err := m.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if err := m.Up(ctx); err != nil {
		t.Fatalf("re-Up: %v", err)
	}
}

type sqliteWidgets struct {
	info sqlkit.TableInfo
	ID   sqlkit.Column[int64]
	Name sqlkit.Column[string]
}

func newSQLiteWidgets() sqliteWidgets {
	info := sqlkit.TableInfo{Name: "w"}
	return sqliteWidgets{info: info, ID: sqlkit.NewColumn[int64](info, "id"), Name: sqlkit.NewColumn[string](info, "name")}
}
func (t sqliteWidgets) Info() sqlkit.TableInfo             { return t.info }
func (t sqliteWidgets) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t sqliteWidgets) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Name} }
func (t sqliteWidgets) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t sqliteWidgets) Defaulted() []sqlkit.AnyColumn      { return nil }

type sqliteWidget struct {
	ID   int64
	Name string
}

// A batched uow flush merges single-PK updates into one VALUES-join UPDATE;
// on SQLite that VALUES list must render as a UNION ALL derived table.
func TestSQLiteUnitOfWorkBatchedUpdate(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, `CREATE TABLE w (id INTEGER PRIMARY KEY, name TEXT NOT NULL); INSERT INTO w VALUES (1,'a'),(2,'b'),(3,'c')`); err != nil {
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
	for _, w := range got {
		if w.Name[len(w.Name)-1] != '!' {
			t.Fatalf("rows after batched flush = %+v, want every name updated", got)
		}
	}
}

// A VALUES derived table larger than SQLite's 500-term compound limit (a
// batched uow UPDATE of hundreds of rows renders one) must still execute.
func TestSQLiteLargeValuesSource(t *testing.T) {
	db := openSQLite(t)
	rows := make([][]sql.Expression, 2000)
	for i := range rows {
		rows[i] = []sql.Expression{sql.Val(i)}
	}
	q := sql.Select(sql.CountAll()).From(sql.ValuesSource(rows, "v", "k"))
	if got := queryInts(t, db, q); len(got) != 1 || got[0] != 2000 {
		t.Fatalf("count = %v, want [2000]", got)
	}

	ctx := context.Background()
	if _, err := db.SQL().ExecContext(ctx, `CREATE TABLE big (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 600; i++ {
		if _, err := db.SQL().ExecContext(ctx, `INSERT INTO big VALUES (?, 'n')`, i); err != nil {
			t.Fatal(err)
		}
	}
	tbl := newSQLiteWidgets()
	tbl.info.Name = "big"
	tbl = sqliteWidgets{info: tbl.info, ID: sqlkit.NewColumn[int64](tbl.info, "id"), Name: sqlkit.NewColumn[string](tbl.info, "name")}
	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		scope := uow.NewScope(s, uow.WithAutoChunk(true))
		var all []sqliteWidget
		if err := scope.Find(ctx, tbl, &all); err != nil {
			return err
		}
		for i := range all {
			all[i].Name = "m"
		}
		return scope.Flush(ctx)
	})
	if err != nil {
		t.Fatalf("batched flush of 600 rows: %v", err)
	}
}

// A non-literal column DEFAULT must be parenthesized for SQLite to accept the
// CREATE TABLE, and must still apply on insert.
func TestSQLiteExpressionDefault(t *testing.T) {
	db := openSQLite(t)
	ctx := context.Background()
	ddl, _, err := sql.CreateTable(sql.Table{Name: sql.NewIdent("dflt")}).
		Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.EngineType{Name: "INTEGER"}}).
		Column(sql.ColumnDef{Name: sql.NewIdent("tag"), Type: sql.EngineType{Name: "TEXT"}, Default: sql.Func("lower", sql.Raw("'ABC'"))}).
		ToSQL(sql.SQLiteDialect{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, ddl); err != nil {
		t.Fatalf("create %s: %v", ddl, err)
	}
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO dflt (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	var tag string
	if err := db.SQL().QueryRowContext(ctx, `SELECT tag FROM dflt`).Scan(&tag); err != nil || tag != "abc" {
		t.Fatalf("tag = %q, %v; want abc", tag, err)
	}
}
