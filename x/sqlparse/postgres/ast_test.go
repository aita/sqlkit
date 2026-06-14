package postgres_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse/postgres"
)

// assertAST parses input and checks that the produced query is structurally
// identical (reflect.DeepEqual, unexported fields included) to the query built
// directly with the sql builders — proving the parser targets the expected AST,
// not merely an SQL string that happens to recompile the same way.
func assertAST(t *testing.T, input string, want sql.Query) {
	t.Helper()
	got, err := postgres.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AST mismatch for %q\n got: %#v\nwant: %#v", input, got, want)
	}
}

func TestASTSelectColumns(t *testing.T) {
	assertAST(t, "SELECT id, name FROM users",
		sql.Select(sql.Col("id"), sql.Col("name")).From(sql.Table{Name: "users"}))
}

func TestASTSelectStar(t *testing.T) {
	assertAST(t, "SELECT * FROM users",
		sql.Select(sql.Star()).From(sql.Table{Name: "users"}))
}

func TestASTAliasedColumnAndSource(t *testing.T) {
	assertAST(t, "SELECT u.id AS uid FROM public.users u",
		sql.Select(sql.As(sql.Col("u.id"), "uid")).
			From(sql.SourceAlias(sql.Table{Schema: "public", Name: "users"}, "u")))
}

func TestASTWhereEq(t *testing.T) {
	assertAST(t, "SELECT id FROM users WHERE id = 1",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.Eq(sql.Col("id"), 1)))
}

func TestASTWhereAnd(t *testing.T) {
	assertAST(t, "SELECT id FROM users WHERE a = 1 AND b = 2",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.And(sql.Eq(sql.Col("a"), 1), sql.Eq(sql.Col("b"), 2))))
}

func TestASTAggregate(t *testing.T) {
	assertAST(t, "SELECT COUNT(*) FROM orders",
		sql.Select(sql.CountAll()).From(sql.Table{Name: "orders"}))
}

func TestASTJoin(t *testing.T) {
	assertAST(t, "SELECT id FROM a JOIN b ON a.x = b.y",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "a"}).
			Join(sql.Table{Name: "b"}, sql.Eq(sql.Col("a.x"), sql.Col("b.y"))))
}

func TestASTOrderLimit(t *testing.T) {
	assertAST(t, "SELECT id FROM users ORDER BY id DESC LIMIT 10",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			OrderBy(sql.Desc(sql.Col("id"))).Limit(10))
}

func TestASTInList(t *testing.T) {
	assertAST(t, "SELECT id FROM t WHERE id IN (1, 2)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "t"}).
			Where(sql.In(sql.Col("id"), 1, 2)))
}

func TestASTForUpdate(t *testing.T) {
	assertAST(t, "SELECT id FROM users FOR UPDATE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).ForUpdate())
}

func TestASTForUpdateOfSkipLocked(t *testing.T) {
	assertAST(t, "SELECT id FROM users FOR UPDATE OF users SKIP LOCKED",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			ForUpdate(sql.LockOf("users"), sql.SkipLocked()))
}

func TestASTInsertValues(t *testing.T) {
	assertAST(t, "INSERT INTO t (id, name) VALUES (1, 'a')",
		sql.InsertInto(sql.Table{Name: "t"}).Columns("id", "name").Values(1, "a"))
}

func TestASTUpdate(t *testing.T) {
	assertAST(t, "UPDATE users SET name = 'x' WHERE id = 1",
		sql.Update(sql.Table{Name: "users"}).
			Set(sql.Set("name", "x")).Where(sql.Eq(sql.Col("id"), 1)))
}

func TestASTDelete(t *testing.T) {
	assertAST(t, "DELETE FROM users WHERE id = 1",
		sql.DeleteFrom(sql.Table{Name: "users"}).Where(sql.Eq(sql.Col("id"), 1)))
}

func TestASTDropTable(t *testing.T) {
	assertAST(t, "DROP TABLE IF EXISTS users",
		sql.DropTable(sql.Table{Name: "users"}).IfExists())
}
