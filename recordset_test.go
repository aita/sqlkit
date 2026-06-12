package sqlkit

import (
	"fmt"
	"testing"
)

func TestRecordsCanBeRanged(t *testing.T) {
	rs := newRecordSet(
		[]Field{{Name: "id", GoType: "int"}},
		[][]any{{1}, {2}},
	)

	var values []int
	for record, err := range rs.Records() {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		value, ok := record.Get("id")
		if !ok {
			t.Fatal("missing id field")
		}
		values = append(values, value.(int))
	}

	if len(values) != 2 || values[0] != 1 || values[1] != 2 {
		t.Fatalf("Records() = %#v, want [1 2]", values)
	}
}

func TestRecordSetScanStructSlice(t *testing.T) {
	type user struct {
		ID    int    `sqlkit:"id"`
		Email string `sqlkit:"email"`
	}

	rs := newRecordSet(
		[]Field{{Name: "id", GoType: "int"}, {Name: "email", GoType: "string"}},
		[][]any{
			{1, "one@example.test"},
			{2, "two@example.test"},
		},
	)

	var users []user
	if err := rs.Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(users) != 2 || users[0].ID != 1 || users[1].Email != "two@example.test" {
		t.Fatalf("Scan() = %#v, want mapped users", users)
	}
}

func TestRecordSetScanStruct(t *testing.T) {
	type user struct {
		ID int `sqlkit:"id"`
	}

	rs := newRecordSet(
		[]Field{{Name: "id", GoType: "int"}},
		[][]any{{42}},
	)

	var got user
	if err := rs.Scan(&got); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("Scan() = %#v, want ID 42", got)
	}
}

func TestRecordScanStruct(t *testing.T) {
	type user struct {
		Email string `sqlkit:"email"`
	}

	record := Record{columns: newRecordColumns([]string{"email"}), values: []any{"record@example.test"}}
	var got user
	if err := record.Scan(&got); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got.Email != "record@example.test" {
		t.Fatalf("Scan() = %#v, want mapped email", got)
	}
}

func ExampleRecordSet_Scan_slice() {
	type User struct {
		ID    int    `sqlkit:"id"`
		Email string `sqlkit:"email"`
	}

	records := newRecordSet(
		[]Field{{Name: "id"}, {Name: "email"}},
		[][]any{{1, "user@example.test"}},
	)

	var users []User
	if err := records.Scan(&users); err != nil {
		panic(err)
	}
	fmt.Println(users[0].ID, users[0].Email)
	// Output: 1 user@example.test
}

func ExampleRecordSet_Scan_struct() {
	type User struct {
		ID int `sqlkit:"id"`
	}

	records := newRecordSet(
		[]Field{{Name: "id"}},
		[][]any{{7}},
	)

	var user User
	if err := records.Scan(&user); err != nil {
		panic(err)
	}
	fmt.Println(user.ID)
	// Output: 7
}
