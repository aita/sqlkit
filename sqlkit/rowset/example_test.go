package rowset

import "fmt"

func ExampleRowSet_Scan_slice() {
	type User struct {
		ID    int    `sqlkit:"id"`
		Email string `db:"email"`
	}

	rows := newRowSet(
		[]Field{{Name: "id"}, {Name: "email"}},
		[]Row{newRow(map[string]any{"id": 1, "email": "user@example.test"})},
	)

	var users []User
	if err := rows.Scan(&users); err != nil {
		panic(err)
	}
	fmt.Println(users[0].ID, users[0].Email)
	// Output: 1 user@example.test
}

func ExampleRowSet_Scan_struct() {
	type User struct {
		ID int `sqlkit:"id"`
	}

	rows := newRowSet(
		[]Field{{Name: "id"}},
		[]Row{newRow(map[string]any{"id": 7})},
	)

	var user User
	if err := rows.Scan(&user); err != nil {
		panic(err)
	}
	fmt.Println(user.ID)
	// Output: 7
}
