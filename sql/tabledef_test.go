package sql

import "testing"

func usersDef() TableDef {
	return TableDef{
		Table:      Table{Schema: NewIdent("public"), Name: NewIdent("users")},
		Columns:    []string{"id", "email", "name"},
		PrimaryKey: []string{"id"},
		Defaulted:  []string{"id"},
	}
}

func TestTableDefSelectRows(t *testing.T) {
	assertSQL(t, usersDef().SelectRows(),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users"`,
		nil)
}

func TestTableDefInsertRowsOmitsZeroDefaultedColumns(t *testing.T) {
	insert, err := usersDef().InsertRows([][]any{
		{int64(0), "a@example.com", "Alice"},
		{int64(0), "b@example.com", "Bob"},
	})
	if err != nil {
		t.Fatalf("InsertRows() error = %v", err)
	}
	assertSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", "Alice", "b@example.com", "Bob"})
}

func TestTableDefInsertRowsKeepsExplicitDefaultedColumn(t *testing.T) {
	insert, err := usersDef().InsertRows([][]any{{int64(42), "a@example.com", "Alice"}})
	if err != nil {
		t.Fatalf("InsertRows() error = %v", err)
	}
	assertSQL(t, insert,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3)`,
		[]any{int64(42), "a@example.com", "Alice"})
}

func TestTableDefInsertRowsRejectsShortRow(t *testing.T) {
	if _, err := usersDef().InsertRows([][]any{{int64(1)}}); err == nil {
		t.Fatal("InsertRows() with short row succeeded, want error")
	}
}
