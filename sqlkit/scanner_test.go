package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"

	"github.com/sqlkit-dev/sqlkit/internal/sqltest"
	"github.com/sqlkit-dev/sqlkit/sqlkit"
)

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
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
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
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"name"}, Rows: [][]driver.Value{{"Alice"}}}, nil
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

func TestInsertValuesPassesValuerFields(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(struct {
		Email upperString
		Name  stdsql.NullString
	}{
		Email: upperString{value: "X@EXAMPLE.COM"},
		Name:  stdsql.NullString{}, // invalid -> NULL via Valuer
	})

	sqlText, args, err := insert.ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2)`
	if sqlText != want {
		t.Fatalf("SQL = %q, want %q", sqlText, want)
	}
	// database/sql converts the args via driver.Valuer at execution time;
	// verify what the driver actually receives.
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	q := db.Insert(Users).Values(struct {
		Email upperString
		Name  stdsql.NullString
	}{Email: upperString{value: "X@EXAMPLE.COM"}})
	if _, err := q.Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	got := fake.Execs()[0].Args
	if got[0] != "x@example.com" || got[1] != nil {
		t.Fatalf("driver args = %#v, want lowered email and NULL name", got)
	}
	_ = args
}

func TestSqlkitTagIsTheOnlyTag(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
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
