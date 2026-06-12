package rowset

import "testing"

func TestRowsCanBeRanged(t *testing.T) {
	rs := newRowSet(
		[]Field{{Name: "id", GoType: "int"}},
		[]Row{newRow(map[string]any{"id": 1}), newRow(map[string]any{"id": 2})},
	)

	var values []int
	for row, err := range rs.Rows() {
		if err != nil {
			t.Fatalf("Rows() error = %v", err)
		}
		value, ok := row.Get("id")
		if !ok {
			t.Fatal("missing id field")
		}
		values = append(values, value.(int))
	}

	if len(values) != 2 || values[0] != 1 || values[1] != 2 {
		t.Fatalf("Rows() = %#v, want [1 2]", values)
	}
}

func TestRowSetScanStructSlice(t *testing.T) {
	type user struct {
		ID    int    `sqlkit:"id"`
		Email string `sqlkit:"email"`
	}

	rs := newRowSet(
		[]Field{{Name: "id", GoType: "int"}, {Name: "email", GoType: "string"}},
		[]Row{
			newRow(map[string]any{"id": 1, "email": "one@example.test"}),
			newRow(map[string]any{"id": 2, "email": "two@example.test"}),
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

func TestRowSetScanStruct(t *testing.T) {
	type user struct {
		ID int `sqlkit:"id"`
	}

	rs := newRowSet(
		[]Field{{Name: "id", GoType: "int"}},
		[]Row{newRow(map[string]any{"id": 42})},
	)

	var got user
	if err := rs.Scan(&got); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("Scan() = %#v, want ID 42", got)
	}
}

func TestRowScanStruct(t *testing.T) {
	type user struct {
		Email string `sqlkit:"email"`
	}

	row := newRow(map[string]any{"email": "row@example.test"})
	var got user
	if err := row.Scan(&got); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got.Email != "row@example.test" {
		t.Fatalf("Scan() = %#v, want mapped email", got)
	}
}
