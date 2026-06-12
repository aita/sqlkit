package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

type timestamps struct {
	CreatedAt time.Time
}

type authorRow struct {
	UserID   int64
	Email    string
	Nickname *string `sqlkit:"name"`
	Ignored  string  `sqlkit:"-"`
	timestamps
}

func newAuthorFake() *sqltest.Fake {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	return &sqltest.Fake{
		QueryFunc: func(sqlText string, args []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"user_id", "email", "name", "created_at"},
				Rows: [][]driver.Value{
					{int64(1), "a@example.com", "Alice", now},
					{int64(2), "b@example.com", nil, now},
				},
			}, nil
		},
	}
}

func TestQueryScansStructsLikeSqlx(t *testing.T) {
	fake := newAuthorFake()
	db := sqlkit.WrapDB(fake.DB())

	var authors []authorRow
	err := db.RawSQL(context.Background(),
		"SELECT user_id, email, name, created_at FROM users WHERE id > $1", 0).Scan(&authors)
	if err != nil {
		t.Fatalf("RawSQL().Scan() error = %v", err)
	}
	if len(authors) != 2 {
		t.Fatalf("rows = %d, want 2", len(authors))
	}
	// user_id matched UserID via snake_case, name via the db tag, and
	// created_at via the embedded struct.
	if authors[0].UserID != 1 || *authors[0].Nickname != "Alice" || authors[0].CreatedAt.IsZero() {
		t.Fatalf("row 0 = %#v", authors[0])
	}
	if authors[1].Nickname != nil {
		t.Fatalf("row 1 nickname = %v, want nil for NULL", *authors[1].Nickname)
	}

	queries := fake.Queries()
	if len(queries) != 1 || queries[0].Args[0] != int64(0) {
		t.Fatalf("raw SQL not passed through: %#v", queries)
	}
}

func TestQueryScansPointerSliceAndAcceptsBuiltQuery(t *testing.T) {
	fake := newAuthorFake()
	db := sqlkit.WrapDB(fake.DB())

	query := sql.Select(sql.Col("user_id"), sql.Col("email"), sql.Col("name"), sql.Col("created_at")).
		From(sql.Table{Name: "users"})
	var authors []*authorRow
	if err := db.RawSQL(context.Background(), query).Scan(&authors); err != nil {
		t.Fatalf("RawSQL().Scan() error = %v", err)
	}
	if len(authors) != 2 || authors[0].Email != "a@example.com" {
		t.Fatalf("rows = %#v", authors)
	}
}

func TestQueryScansOneRowAndReportsNoRows(t *testing.T) {
	fake := newAuthorFake()
	db := sqlkit.WrapDB(fake.DB())

	var author authorRow
	if err := db.RawSQL(context.Background(), "SELECT ... WHERE id = $1", 1).Scan(&author); err != nil {
		t.Fatalf("RawSQL().Scan(&struct) error = %v", err)
	}
	if author.UserID != 1 {
		t.Fatalf("author = %#v", author)
	}

	empty := &sqltest.Fake{}
	emptyDB := sqlkit.WrapDB(empty.DB())
	if err := emptyDB.RawSQL(context.Background(), "SELECT ...").Scan(&author); !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("Scan() on empty result = %v, want ErrNoRows", err)
	}
}

func TestQueryRejectsArgsWithBuiltQuery(t *testing.T) {
	db := sqlkit.WrapDB(newAuthorFake().DB())
	query := sql.Select(sql.Col("id")).From(sql.Table{Name: "users"})
	var authors []authorRow
	if err := db.RawSQL(context.Background(), query, 1, 2).Scan(&authors); err == nil {
		t.Fatal("RawSQL(builtQuery, args...) succeeded, want error")
	}
}

func TestSessionQueryRunsInTransaction(t *testing.T) {
	fake := &sqltest.Fake{}
	fake.QueryFunc = func(sqlText string, args []any) (sqltest.Result, error) {
		return sqltest.Result{Columns: []string{"email"}, Rows: [][]driver.Value{{"a@example.com"}}}, nil
	}
	session := newSession(t, fake)

	var authors []authorRow
	if err := session.RawSQL(context.Background(), "SELECT email FROM users").Scan(&authors); err != nil {
		t.Fatalf("RawSQL().Scan() error = %v", err)
	}
	if len(authors) != 1 || authors[0].Email != "a@example.com" {
		t.Fatalf("authors = %#v", authors)
	}
}
