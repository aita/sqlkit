package sqlkit_test

import (
	"errors"
	"testing"

	"github.com/aita/sqlkit"
)

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
