package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

func TestQueryRejectsUnsupportedQueryType(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	var out []User
	err := db.RawSQL(context.Background(), 42).Scan(&out)
	if err == nil || !strings.Contains(err.Error(), "must be a SQL string or sql.Query") {
		t.Fatalf("Scan() error = %v, want unsupported-query-type error", err)
	}
}

// upperString is a custom database/sql Scanner / driver.Valuer pair.
type upperString struct {
	value string
}

func (u *upperString) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		u.value = ""
		return nil
	case string:
		u.value = strings.ToUpper(v)
		return nil
	case []byte:
		u.value = strings.ToUpper(string(v))
		return nil
	default:
		return fmt.Errorf("upperString: cannot scan %T", src)
	}
}

func (u upperString) Value() (driver.Value, error) { return strings.ToLower(u.value), nil }

func TestScanIntoSQLNullAndCustomScannerFields(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "a@example.com", "Alice"},
					{int64(2), "b@example.com", nil},
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var rows []struct {
		ID    int64
		Email upperString       // custom Scanner
		Name  stdsql.NullString // stdlib Scanner; NULL-aware
	}
	err := db.Select().From(Users).All(context.Background()).Scan(&rows)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if rows[0].Email.value != "A@EXAMPLE.COM" {
		t.Fatalf("custom scanner value = %q", rows[0].Email.value)
	}
	if !rows[0].Name.Valid || rows[0].Name.String != "Alice" {
		t.Fatalf("row 0 name = %#v", rows[0].Name)
	}
	if rows[1].Name.Valid {
		t.Fatalf("row 1 name = %#v, want invalid for NULL", rows[1].Name)
	}
}

func TestScannerStructFieldIsNotFlattened(t *testing.T) {
	// sql.NullString is a struct, but it implements Scanner, so it maps as a
	// single column rather than being flattened into String/Valid fields.
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"name"}, Rows: [][]driver.Value{{"Alice"}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var row struct {
		Name stdsql.NullString
	}
	if err := db.RawSQL(context.Background(), "SELECT name FROM users").Scan(&row); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if !row.Name.Valid || row.Name.String != "Alice" {
		t.Fatalf("name = %#v", row.Name)
	}
}

func TestSqlkitTagIsTheOnlyTag(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email"},
				Rows:    [][]driver.Value{{int64(1), "a@example.com"}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	// db/json tags are no longer consulted: this maps only by field name, so
	// the "email" column has no destination and Scan fails.
	var rows []struct {
		ID   int64
		Addr string `db:"email" json:"email"`
	}
	err := db.Select(Users.ID, Users.Email).From(Users).All(context.Background()).Scan(&rows)
	if err == nil || !strings.Contains(err.Error(), `no field`) {
		t.Fatalf("Scan() error = %v, want unmapped-column error", err)
	}

	// The sqlkit tag works.
	var ok []struct {
		ID   int64
		Addr string `sqlkit:"email"`
	}
	if err := db.Select(Users.ID, Users.Email).From(Users).All(context.Background()).Scan(&ok); err != nil {
		t.Fatalf("Scan() with sqlkit tag error = %v", err)
	}
	if ok[0].Addr != "a@example.com" {
		t.Fatalf("ok[0] = %#v", ok[0])
	}
}

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

func newAuthorFake() *testutil.Fake {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	return &testutil.Fake{
		QueryFunc: func(sqlText string, args []any) (testutil.Result, error) {
			return testutil.Result{
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
		From(sql.Table{Name: sql.NewIdent("users")})
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

	empty := &testutil.Fake{}
	emptyDB := sqlkit.WrapDB(empty.DB())
	if err := emptyDB.RawSQL(context.Background(), "SELECT ...").Scan(&author); !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("Scan() on empty result = %v, want ErrNoRows", err)
	}
}

func TestQueryRejectsArgsWithBuiltQuery(t *testing.T) {
	db := sqlkit.WrapDB(newAuthorFake().DB())
	query := sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")})
	var authors []authorRow
	if err := db.RawSQL(context.Background(), query, 1, 2).Scan(&authors); err == nil {
		t.Fatal("RawSQL(builtQuery, args...) succeeded, want error")
	}
}

func TestSessionQueryRunsInTransaction(t *testing.T) {
	fake := &testutil.Fake{}
	fake.QueryFunc = func(sqlText string, args []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"email"}, Rows: [][]driver.Value{{"a@example.com"}}}, nil
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

// TestScanRowsScansExternallyExecutedQuery exercises the standalone-builder
// path: a typed unbound query compiled with ToSQL, executed through a plain
// database/sql handle (standing in for sqlx, a pgx stdlib pool, ...), and
// scanned with ScanRows.
func TestScanRowsScansExternallyExecutedQuery(t *testing.T) {
	fake := newAuthorFake()
	db := fake.DB() // plain *sql.DB, no sqlkit executor

	info := sqlkit.TableInfo{Name: "users"}
	userID := sqlkit.NewColumn[int64](info, "user_id")
	query := sqlkit.Select(
		userID,
		sqlkit.NewColumn[string](info, "email"),
		sqlkit.NewColumn[*string](info, "name"),
		sqlkit.NewColumn[time.Time](info, "created_at"),
	).From(info).Where(userID.Gt(0)).OrderBy(userID.Asc())

	sqlText, args, err := query.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `SELECT "users"."user_id", "users"."email", "users"."name", "users"."created_at"` +
		` FROM "users" WHERE ("users"."user_id" > $1) ORDER BY "users"."user_id"`
	if sqlText != want {
		t.Fatalf("ToSQL() = %q, want %q", sqlText, want)
	}
	if len(args) != 1 || args[0] != int64(0) {
		t.Fatalf("args = %#v, want [0]", args)
	}

	rows, err := db.QueryContext(context.Background(), sqlText, args...)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()

	var authors []authorRow
	if err := sqlkit.ScanRows(rows, &authors); err != nil {
		t.Fatalf("ScanRows() error = %v", err)
	}
	if len(authors) != 2 || authors[0].UserID != 1 || *authors[0].Nickname != "Alice" {
		t.Fatalf("authors = %#v", authors)
	}
}

func TestScanRowsSingleDestReportsNoRows(t *testing.T) {
	empty := &testutil.Fake{}
	rows, err := empty.DB().QueryContext(context.Background(), "SELECT ...")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()

	var author authorRow
	if err := sqlkit.ScanRows(rows, &author); !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("ScanRows() on empty result = %v, want ErrNoRows", err)
	}
}

// TestPrimaryKeyValues covers extracting a row's primary-key values in the
// table's PrimaryKey order, accepting both a struct and a pointer to one.
func TestPrimaryKeyValues(t *testing.T) {
	got, err := sqlkit.PrimaryKeyValues(Users, User{ID: 7})
	if err != nil {
		t.Fatalf("PrimaryKeyValues() error = %v", err)
	}
	if len(got) != 1 || got[0] != int64(7) {
		t.Fatalf("PrimaryKeyValues() = %#v, want [7]", got)
	}

	// A pointer to the row maps the same way.
	got, err = sqlkit.PrimaryKeyValues(Users, &User{ID: 9})
	if err != nil {
		t.Fatalf("PrimaryKeyValues(ptr) error = %v", err)
	}
	if got[0] != int64(9) {
		t.Fatalf("PrimaryKeyValues(ptr) = %#v, want [9]", got)
	}
}

// TestPrimaryKeyValuesMissingField covers the error when the row struct has no
// field mapping to a primary-key column.
func TestPrimaryKeyValuesMissingField(t *testing.T) {
	type noID struct {
		Email string
	}
	_, err := sqlkit.PrimaryKeyValues(Users, noID{Email: "a@x"})
	if !errors.Is(err, sqlkit.ErrMissingPrimaryKey) {
		t.Fatalf("PrimaryKeyValues() error = %v, want ErrMissingPrimaryKey", err)
	}
}

// TestPrimaryKeyValuesNilPointer covers the nil-pointer row error.
func TestPrimaryKeyValuesNilPointer(t *testing.T) {
	if _, err := sqlkit.PrimaryKeyValues(Users, (*User)(nil)); err == nil {
		t.Fatal("PrimaryKeyValues(nil) should error")
	}
}

// TestColumnValues covers mapping a row to its table columns in declaration
// order, skipping columns the struct provides no field for.
func TestColumnValues(t *testing.T) {
	name := "Alice"
	names, values, err := sqlkit.ColumnValues(Users, User{ID: 1, Email: "a@x", Name: &name})
	if err != nil {
		t.Fatalf("ColumnValues() error = %v", err)
	}
	wantNames := []string{"id", "email", "name"}
	if len(names) != len(wantNames) {
		t.Fatalf("ColumnValues() names = %#v, want %#v", names, wantNames)
	}
	for i, want := range wantNames {
		if names[i] != want {
			t.Fatalf("ColumnValues() names[%d] = %q, want %q", i, names[i], want)
		}
	}
	if values[0] != int64(1) || values[1] != "a@x" {
		t.Fatalf("ColumnValues() values = %#v", values)
	}
}

// TestColumnValuesSkipsUnmappedColumns covers a row struct that only provides a
// subset of the table's columns: the others are skipped.
func TestColumnValuesSkipsUnmappedColumns(t *testing.T) {
	type idOnly struct {
		ID int64
	}
	names, values, err := sqlkit.ColumnValues(Users, idOnly{ID: 3})
	if err != nil {
		t.Fatalf("ColumnValues() error = %v", err)
	}
	if len(names) != 1 || names[0] != "id" || values[0] != int64(3) {
		t.Fatalf("ColumnValues() = %#v / %#v, want only id=3", names, values)
	}
}

// TestSetColumns covers writing values back into the row's mapped fields,
// skipping columns the struct has no field for.
func TestSetColumns(t *testing.T) {
	var row User
	err := sqlkit.SetColumns(&row, []string{"id", "email", "missing"}, []any{int64(5), "z@x", "ignored"})
	if err != nil {
		t.Fatalf("SetColumns() error = %v", err)
	}
	if row.ID != 5 || row.Email != "z@x" {
		t.Fatalf("SetColumns() row = %#v, want id=5 email=z@x", row)
	}
}

// TestSetColumnsConverts covers the convertible-but-not-assignable path: an int
// value stored into an int64 field is converted.
func TestSetColumnsConverts(t *testing.T) {
	var row User
	if err := sqlkit.SetColumns(&row, []string{"id"}, []any{int(8)}); err != nil {
		t.Fatalf("SetColumns() error = %v", err)
	}
	if row.ID != 8 {
		t.Fatalf("SetColumns() row.ID = %d, want 8", row.ID)
	}
}

// TestSetColumnsErrors covers the argument-validation and type-mismatch paths.
func TestSetColumnsErrors(t *testing.T) {
	var row User
	if err := sqlkit.SetColumns(&row, []string{"id"}, []any{}); err == nil {
		t.Fatal("SetColumns() with mismatched lengths should error")
	}
	if err := sqlkit.SetColumns(row, []string{"id"}, []any{int64(1)}); err == nil {
		t.Fatal("SetColumns() into a non-pointer should error")
	}
	if err := sqlkit.SetColumns(&row, []string{"id"}, []any{"not-an-int"}); err == nil {
		t.Fatal("SetColumns() with an inconvertible value should error")
	}
}
